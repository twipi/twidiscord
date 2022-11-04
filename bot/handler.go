package bot

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/ningen/v3"
	"github.com/diamondburned/twidiscord/twidiscord"
	"github.com/diamondburned/twikit/twicli"
	"github.com/diamondburned/twikit/twipi"
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

// Handler is a handler for a single Discord account.
type Handler struct {
	twidiscord.Account
	twipi   *twipi.ConfiguredServer
	discord *ningen.State
	config  twidiscord.Config
	store   twidiscord.Storer

	fragmentMu sync.Mutex
	fragments  map[string]messageFragment

	sessions struct {
		sync.Mutex
		ourID    string
		sessions []gateway.UserSession
	}

	messageThrottlers messageThrottlers

	ctx context.Context
	cmd twicli.Command
}

type messageFragment struct {
	content string
}

// NewHandler creates a new AccountHandler.
func NewHandler(twipisrv *twipi.ConfiguredServer, account twidiscord.Account, cfg twidiscord.Config, store twidiscord.Storer) *Handler {
	id := gateway.DefaultIdentifier(account.DiscordToken)
	id.Capabilities = 253 // magic constant from reverse-engineering
	id.Presence = &gateway.UpdatePresenceCommand{
		Status: discord.IdleStatus,
		AFK:    true,
	}
	id.Properties = gateway.IdentifyProperties{
		OS:      runtime.GOOS,
		Device:  fmt.Sprintf("twikit/%s", hostname),
		Browser: "twidiscord",
	}

	h := &Handler{
		Account:   account,
		twipi:     twipisrv,
		discord:   ningen.NewWithIdentifier(id),
		config:    cfg,
		store:     store,
		fragments: make(map[string]messageFragment),
		ctx:       context.Background(),
	}

	h.bindDiscord()
	h.initCommand()

	h.messageThrottlers = *newMessageThrottlers(messageThrottleConfig{
		max: 15,
		do:  h.sendMessageIDs,
	})

	return h
}

// UseContext sets the context for this handler.
func (h *Handler) UseContext(ctx context.Context) {
	h.ctx = ctx
	h.discord = h.discord.WithContext(ctx)
}

// Context returns the context of this handler.
func (h *Handler) Context() context.Context {
	return h.ctx
}

// Connect stays connected for as long as the context set with UseContext
// remains valid.
func (h *Handler) Connect() error {
	return h.discord.Connect(h.ctx)
}

func chName(ch discord.Channel) string {
	if ch.Name != "" {
		return ch.Name
	}

	log.Printf("seeing channel %#v", ch)

	switch len(ch.DMRecipients) {
	case 0:
		return ch.ID.Mention()
	case 1:
		return ch.DMRecipients[0].Username
	default:
		var buf string
		for i := 0; i < len(ch.DMRecipients) && i < 3; i++ {
			buf += ch.DMRecipients[i].Username + ", "
		}
		if len(ch.DMRecipients) > 3 {
			buf += "..."
		} else {
			buf = strings.TrimSuffix(buf, ", ")
		}
		return buf
	}
}
