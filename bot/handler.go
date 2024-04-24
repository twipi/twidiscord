package bot

import (
	"context"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/ningen/v3"
	"github.com/diamondburned/twidiscord/store"
	"github.com/twipi/twipi/twisms"
)

var hostname string

func init() {
	h, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	} else {
		hostname = h
	}
}

// Session is a Discord SMS gateway session.
type Session struct {
	Account store.Account

	sms     twisms.MessageSender
	store   store.AccountStore
	discord *ningen.State

	logger   *slog.Logger
	logAttrs atomic.Pointer[slog.Attr]

	sessions struct {
		sync.Mutex
		ourID    string
		sessions []gateway.UserSession
	}
	throttlers *messageThrottlers
}

type messageFragment struct {
	content string
}

// NewSession creates a new session.
func NewSession(store store.AccountStore, sms twisms.MessageSender, logger *slog.Logger) *Session {
	account := store.Account()

	id := gateway.DefaultIdentifier(account.DiscordToken)
	id.Capabilities = 253 // magic constant from reverse-engineering
	id.Presence = &gateway.UpdatePresenceCommand{
		Status: discord.IdleStatus,
		AFK:    true,
	}
	id.Properties = gateway.IdentifyProperties{
		OS:      runtime.GOOS,
		Device:  "twipi/" + hostname,
		Browser: "twidiscord",
	}

	logger = logger.With(
		"user_number", account.UserNumber,
		"server_number", account.ServerNumber)

	s := &Session{
		Account: account,
		sms:     sms,
		store:   store,
		logger:  logger,
		discord: ningen.NewWithIdentifier(id),
	}

	emptyGroup := slog.Group("user")
	s.logAttrs.Store(&emptyGroup)

	return s
}

// Start starts the handler.
func (s *Session) Start(ctx context.Context) error {
	s.discord = s.discord.WithContext(ctx)
	s.bindDiscord()

	s.throttlers = newMessageThrottlers(15,
		s.logger.With("component", "message_throttler"),
		func(chID discord.ChannelID, ids []discord.MessageID) {
			s.sendMessageIDs(ctx, chID, ids)
		},
	)
	defer s.throttlers.wg.Wait()

	return s.discord.Connect(ctx)
}
