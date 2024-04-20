package sqlite

import (
	"context"
	"database/sql"
	"time"

	_ "embed"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/twidiscord/store"
	"github.com/diamondburned/twidiscord/store/sqlite/queries"
	"github.com/pkg/errors"
	"libdb.so/lazymigrate"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var sqliteSchema string

const pragma = `
PRAGMA strict = ON;
PRAGMA journal_mode = WAL;
`

// SQLite is a SQLite database.
type SQLite struct {
	q  *queries.Queries
	db *sql.DB
}

var _ store.Store = (*SQLite)(nil)

// New creates a new SQLite database.
func New(ctx context.Context, uri string) (*SQLite, error) {
	sqlDB, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, errors.Wrap(err, "failed to open SQLite database")
	}

	if _, err := sqlDB.ExecContext(ctx, pragma); err != nil {
		return nil, errors.Wrap(err, "failed to set SQLite pragmas")
	}

	if err := lazymigrate.Migrate(ctx, sqlDB, sqliteSchema); err != nil {
		return nil, errors.Wrap(err, "failed to migrate SQLite database")
	}

	return &SQLite{
		q:  queries.New(sqlDB),
		db: sqlDB,
	}, nil
}

func (s *SQLite) Close() error {
	return s.db.Close()
}

func (s *SQLite) Account(ctx context.Context, userNumber string) (store.AccountStore, error) {
	v, err := s.q.Account(ctx, string(userNumber))
	if err != nil {
		return nil, sqliteErr(err)
	}

	return &accountStore{
		q: s.q,
		account: store.Account{
			UserNumber:   userNumber,
			ServerNumber: v.ServerNumber,
			DiscordToken: v.DiscordToken,
		},
	}, nil
}

func (s *SQLite) Accounts(ctx context.Context) ([]store.Account, error) {
	rows, err := s.q.Accounts(ctx)
	if err != nil {
		return nil, sqliteErr(err)
	}

	accs := make([]store.Account, len(rows))
	for i, v := range rows {
		accs[i] = store.Account{
			UserNumber:   v.UserNumber,
			ServerNumber: v.ServerNumber,
			DiscordToken: v.DiscordToken,
		}
	}

	return accs, nil
}

func (s *SQLite) SetAccount(ctx context.Context, info store.Account) error {
	err := s.q.SetAccount(ctx, queries.SetAccountParams{
		UserNumber:   string(info.UserNumber),
		ServerNumber: string(info.ServerNumber),
		DiscordToken: info.DiscordToken,
	})
	return sqliteErr(err)
}

type accountStore struct {
	q       *queries.Queries
	account store.Account
}

var _ store.AccountStore = (*accountStore)(nil)

func (s *accountStore) Account() store.Account {
	return s.account
}

func (s *accountStore) NumberIsMuted(ctx context.Context) bool {
	v, _ := s.q.NumberIsMuted(ctx, s.account.UserNumber)
	return v != 0
}

func (s *accountStore) UnmuteNumber(ctx context.Context) error {
	err := s.q.SetNumberMuted(ctx, queries.SetNumberMutedParams{
		UserNumber: s.account.UserNumber,
		Muted:      0,
	})
	return sqliteErr(err)
}

func (s *accountStore) MuteNumber(ctx context.Context, until time.Time) error {
	var untilInt64 int64
	if !until.IsZero() {
		untilInt64 = until.Unix()
	}

	err := s.q.SetNumberMuted(ctx, queries.SetNumberMutedParams{
		UserNumber: s.account.UserNumber,
		Muted:      1,
		Until:      untilInt64,
	})
	return sqliteErr(err)
}

func (s *accountStore) ChannelNickname(ctx context.Context, chID discord.ChannelID) (string, error) {
	nickname, err := s.q.ChannelNickname(ctx, queries.ChannelNicknameParams{
		UserNumber: string(s.account.UserNumber),
		ChannelID:  int64(chID),
	})
	if err != nil {
		return "", sqliteErr(err)
	}
	return nickname, nil
}

func (s *accountStore) ChannelFromNickname(ctx context.Context, nickname string) (discord.ChannelID, error) {
	id, err := s.q.ChannelFromNickname(ctx, queries.ChannelFromNicknameParams{
		UserNumber: string(s.account.UserNumber),
		Nickname:   nickname,
	})
	if err != nil {
		return 0, sqliteErr(err)
	}
	return discord.ChannelID(id), nil
}

func (s *accountStore) SetChannelNickname(ctx context.Context, chID discord.ChannelID, nickname string) error {
	err := s.q.SetChannelNickname(ctx, queries.SetChannelNicknameParams{
		UserNumber: string(s.account.UserNumber),
		ChannelID:  int64(chID),
		Nickname:   nickname,
	})
	return sqliteErr(err)
}

func sqliteErr(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	return err
}
