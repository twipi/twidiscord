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
	"github.com/twipi/twidiscord/store"
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
	*ningen.State
	Account store.Account

	sms   twisms.MessageSender
	store store.AccountStore

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

	state := ningen.NewWithIdentifier(id)
	s := &Session{
		State:   state,
		Account: account,
		sms:     sms,
		store:   store,
		logger:  logger,
	}

	emptyGroup := slog.Group("user")
	s.logAttrs.Store(&emptyGroup)

	return s
}

// Start starts the handler.
func (s *Session) Start(ctx context.Context) error {
	s.State = s.State.WithContext(ctx)
	s.bindDiscord()

	s.throttlers = newMessageThrottlers(15,
		s.logger.With("component", "message_throttler"),
		func(chID discord.ChannelID, ids []discord.MessageID) {
			s.sendMessageIDs(ctx, chID, ids)
		},
	)
	defer s.throttlers.wg.Wait()

	return s.State.Connect(ctx)
}
