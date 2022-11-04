package twidiscord_module

import (
	"context"
	"fmt"
	"net/http"

	"github.com/diamondburned/twidiscord/bot"
	"github.com/diamondburned/twidiscord/store"
	"github.com/diamondburned/twidiscord/twidiscord"
	"github.com/diamondburned/twidiscord/web/routes"
	"github.com/diamondburned/twikit/logger"
	"github.com/diamondburned/twikit/twid"
	"github.com/diamondburned/twikit/twipi"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

func init() {
	twid.Register(Module)
}

// Module is the twidiscord module.
var Module = twid.Module{
	Name: "discord",
	New: func() twid.Handler {
		return NewEmptyHandler()
	},
}

// Handler is the main handler that binds Twipi and Discord.
type Handler struct {
	twipi  *twipi.ConfiguredServer
	config twidiscord.Config
	store  twidiscord.Storer

	accCh chan twidiscord.Account
	msgCh chan twipi.Message
}

var (
	_ twid.Handler        = (*Handler)(nil)
	_ twid.TwipiHandler   = (*Handler)(nil)
	_ twid.HTTPCommander  = (*Handler)(nil)
	_ twid.MessageHandler = (*Handler)(nil)
)

// NewEmptyHandler creates a new empty Handler. It is used to initialize its
// dependencies later.
func NewEmptyHandler() *Handler {
	return &Handler{
		accCh: make(chan twidiscord.Account),
		msgCh: make(chan twipi.Message),
	}
}

// NewHandler creates a new handler with the given twipi server and config.
func NewHandler(twipisrv *twipi.ConfiguredServer, cfg twidiscord.Config) *Handler {
	h := NewEmptyHandler()
	h.config = cfg
	h.BindTwipi(twipisrv)
	return h
}

// Config returns the local configuration instance for this module. It
// implements twid.Handler.
func (h *Handler) Config() any {
	return &h.config
}

// BindTwipi implements twid.TwipiBinder.
func (h *Handler) BindTwipi(twipisrv *twipi.ConfiguredServer) {
	h.twipi = twipisrv
}

// HTTPHandler implements twid.HTTPCommander.
func (h *Handler) HTTPHandler() http.Handler {
	return routes.Mount(h.twipi, h.config, (*accountAdder)(h))
}

// HTTPPrefix implements twid.HTTPCommander.
func (h *Handler) HTTPPrefix() string {
	return "/discord"
}

type accountAdder Handler

func (a *accountAdder) AddAccount(ctx context.Context, account twidiscord.Account) error {
	if err := a.store.SetAccount(ctx, account); err != nil {
		return err
	}

	select {
	case a.accCh <- account:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// HandleMessage implements twid.MessageHandler.
func (h *Handler) HandleMessage(ctx context.Context, msg twipi.Message) {
	select {
	case h.msgCh <- msg:
		// ok
	case <-ctx.Done():
		log := logger.FromContext(ctx)
		log.Println("context done before message can be handled:", ctx.Err())
	}
}

// Start connects all the accounts. It blocks until ctx is canceled.
func (h *Handler) Start(ctx context.Context) error {
	if h.twipi == nil {
		return errors.New("twipi server not set")
	}

	db, err := store.Open(ctx, h.config.Discord.DatabaseURI.String(), false)
	if err != nil {
		return errors.Wrap(err, "failed to open database")
	}
	defer store.Close(db)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ctx = logger.WithLogPrefix(ctx, "twidiscord:")
	h.store = db

	var errg errgroup.Group

	errg.Go(func() error {
		type activeBot struct {
			*bot.Handler
			cancel context.CancelFunc
		}

		knownBots := make(map[twipi.PhoneNumber]activeBot)

		for {
			select {
			case <-ctx.Done():
				return nil

			case account := <-h.accCh:
				if oldBot, ok := knownBots[account.UserNumber]; ok {
					// Cancel the old bot.
					oldBot.cancel()
				}

				actx, acancel := context.WithCancel(ctx)

				accountBot := bot.NewHandler(h.twipi, account, h.config, h.store)
				accountBot.UseContext(actx)

				knownBots[account.UserNumber] = activeBot{
					Handler: accountBot,
					cancel:  acancel,
				}

				errg.Go(func() error {
					h.startAccount(actx, accountBot)
					return nil
				})

			case message := <-h.msgCh:
				bot, ok := knownBots[message.From]
				if !ok {
					continue // no bot for this number
				}

				errg.Go(func() error {
					cmd := bot.Command()
					cmd.DoAndReply(ctx, h.twipi.Client, message)
					return nil
				})
			}
		}
	})

	errg.Go(func() error {
		accounts, err := h.store.Accounts(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to load accounts")
		}

		for _, account := range accounts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case h.accCh <- account:
				// ok
			}
		}

		return nil
	})

	return errg.Wait()
}

func (h *Handler) startAccount(ctx context.Context, b *bot.Handler) {
	ctx = logger.WithLogPrefix(ctx, "discord: "+string(b.UserNumber))
	b.UseContext(ctx)

	if err := b.Connect(); err != nil {
		log := logger.FromContext(ctx)
		log.Printf("failed to connect to Discord for user %s: %v", b.UserNumber, err)

		// Tell the user that we failed to connect.
		h.twipi.Client.SendSMS(ctx, twipi.Message{
			From: b.TwilioNumber,
			To:   b.UserNumber,
			Body: fmt.Sprintf("Sorry, we couldn't connect to Discord: %v", err),
		})
	}

	log := logger.FromContext(ctx)
	log.Printf("disconnected from Discord for user %s", b.UserNumber)
}
