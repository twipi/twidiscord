// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.26.0
// source: queries.sql

package queries

import (
	"context"
)

const account = `-- name: Account :one
SELECT server_number, discord_token FROM accounts WHERE user_number = ? LIMIT 1
`

type AccountRow struct {
	ServerNumber string
	DiscordToken string
}

func (q *Queries) Account(ctx context.Context, userNumber string) (AccountRow, error) {
	row := q.db.QueryRowContext(ctx, account, userNumber)
	var i AccountRow
	err := row.Scan(&i.ServerNumber, &i.DiscordToken)
	return i, err
}

const accounts = `-- name: Accounts :many
SELECT user_number, server_number, discord_token FROM accounts
`

func (q *Queries) Accounts(ctx context.Context) ([]Account, error) {
	rows, err := q.db.QueryContext(ctx, accounts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Account
	for rows.Next() {
		var i Account
		if err := rows.Scan(&i.UserNumber, &i.ServerNumber, &i.DiscordToken); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const channelFromNickname = `-- name: ChannelFromNickname :one
SELECT channel_id FROM channel_nicknames WHERE user_number = ? AND nickname = ? LIMIT 1
`

type ChannelFromNicknameParams struct {
	UserNumber string
	Nickname   string
}

func (q *Queries) ChannelFromNickname(ctx context.Context, arg ChannelFromNicknameParams) (int64, error) {
	row := q.db.QueryRowContext(ctx, channelFromNickname, arg.UserNumber, arg.Nickname)
	var channel_id int64
	err := row.Scan(&channel_id)
	return channel_id, err
}

const channelNickname = `-- name: ChannelNickname :one
SELECT nickname FROM channel_nicknames WHERE user_number = ? AND channel_id = ? LIMIT 1
`

type ChannelNicknameParams struct {
	UserNumber string
	ChannelID  int64
}

func (q *Queries) ChannelNickname(ctx context.Context, arg ChannelNicknameParams) (string, error) {
	row := q.db.QueryRowContext(ctx, channelNickname, arg.UserNumber, arg.ChannelID)
	var nickname string
	err := row.Scan(&nickname)
	return nickname, err
}

const numberIsMuted = `-- name: NumberIsMuted :one
SELECT muted FROM numbers_muted
	WHERE user_number = ? AND (until = 0 OR until > NOW())
	LIMIT 1
`

func (q *Queries) NumberIsMuted(ctx context.Context, userNumber string) (int64, error) {
	row := q.db.QueryRowContext(ctx, numberIsMuted, userNumber)
	var muted int64
	err := row.Scan(&muted)
	return muted, err
}

const setAccount = `-- name: SetAccount :exec
REPLACE INTO accounts (user_number, server_number, discord_token) VALUES (?, ?, ?)
`

type SetAccountParams struct {
	UserNumber   string
	ServerNumber string
	DiscordToken string
}

func (q *Queries) SetAccount(ctx context.Context, arg SetAccountParams) error {
	_, err := q.db.ExecContext(ctx, setAccount, arg.UserNumber, arg.ServerNumber, arg.DiscordToken)
	return err
}

const setChannelNickname = `-- name: SetChannelNickname :exec
REPLACE INTO channel_nicknames (user_number, channel_id, nickname) VALUES (?, ?, ?)
`

type SetChannelNicknameParams struct {
	UserNumber string
	ChannelID  int64
	Nickname   string
}

func (q *Queries) SetChannelNickname(ctx context.Context, arg SetChannelNicknameParams) error {
	_, err := q.db.ExecContext(ctx, setChannelNickname, arg.UserNumber, arg.ChannelID, arg.Nickname)
	return err
}

const setNumberMuted = `-- name: SetNumberMuted :exec
REPLACE INTO numbers_muted (user_number, muted, until) VALUES (?, ?, ?)
`

type SetNumberMutedParams struct {
	UserNumber string
	Muted      int64
	Until      int64
}

func (q *Queries) SetNumberMuted(ctx context.Context, arg SetNumberMutedParams) error {
	_, err := q.db.ExecContext(ctx, setNumberMuted, arg.UserNumber, arg.Muted, arg.Until)
	return err
}