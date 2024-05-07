package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"

	_ "embed"

	"github.com/twipi/twidiscord/bot"
	"github.com/twipi/twidiscord/store"
	"github.com/pkg/errors"
	"github.com/puzpuzpuz/xsync/v3"
	"github.com/twipi/pubsub"
	"github.com/twipi/twipi/proto/out/twicmdcfgpb"
	"github.com/twipi/twipi/proto/out/twicmdproto"
	"github.com/twipi/twipi/proto/out/twismsproto"
	"github.com/twipi/twipi/twicmd"
	"github.com/twipi/twipi/twisms"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/encoding/prototext"
)

//go:embed service.txtpb
var servicePrototext []byte

var service = (func() *twicmdproto.Service {
	service := new(twicmdproto.Service)
	if err := prototext.Unmarshal(servicePrototext, service); err != nil {
		panic(fmt.Sprintf("failed to unmarshal service proto: %v", err))
	}
	return service
})()

// Service is the main handler that binds Twipi and Discord.
type Service struct {
	store     store.Store
	accCh     chan store.Account
	sendCh    chan *twismsproto.Message
	sendSub   pubsub.Subscriber[*twismsproto.Message]
	knownBots *xsync.MapOf[string, startedBot]
	logger    *slog.Logger
}

type startedBot struct {
	*bot.Session
	stop context.CancelFunc
}

var (
	_ twicmd.Service             = (*Service)(nil)
	_ twicmd.ConfigurableService = (*Service)(nil)
	_ twisms.MessageSubscriber   = (*Service)(nil)
)

// NewService creates a new handler with the given twipi server and config.
func NewService(s store.Store, logger *slog.Logger) *Service {
	return &Service{
		store:     s,
		accCh:     make(chan store.Account),
		sendCh:    make(chan *twismsproto.Message),
		knownBots: xsync.NewMapOf[string, startedBot](),
		logger:    logger,
	}
}

// AddAccount adds an account to the handler. It blocks until the account is added.
// [Start] must be called before this function.
func (s *Service) AddAccount(ctx context.Context, account store.Account) error {
	select {
	case s.accCh <- account:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SubscribeMessages implements [twisms.MessageSubscriber].
func (s *Service) SubscribeMessages(ch chan<- *twismsproto.Message, filters *twismsproto.MessageFilters) {
	s.sendSub.Subscribe(ch, func(msg *twismsproto.Message) bool {
		return twisms.FilterMessage(filters, msg)
	})
}

// UnsubscribeMessages implements [twisms.MessageSubscriber].
func (s *Service) UnsubscribeMessages(ch chan<- *twismsproto.Message) {
	s.sendSub.Unsubscribe(ch)
}

// Name implements [twicmd.Service].
func (s *Service) Name() string {
	return service.Name
}

// Service implements [twicmd.Service].
func (s *Service) Service(ctx context.Context) (*twicmdproto.Service, error) {
	return service, nil
}

// Execute implements [twicmd.Service].
func (s *Service) Execute(ctx context.Context, req *twicmdproto.ExecuteRequest) (*twicmdproto.ExecuteResponse, error) {
	bb, ok := s.knownBots.Load(req.Message.From)
	if !ok {
		return twicmd.StatusResponse("your account is not ready yet"), nil
	}
	return bb.Execute(ctx, req)
}

// Start connects all the accounts. It blocks until ctx is canceled.
func (s *Service) Start(ctx context.Context) error {
	errg, ctx := errgroup.WithContext(ctx)

	errg.Go(func() error {
		return s.sendSub.Listen(ctx, s.sendCh)
	})

	errg.Go(func() error {
		for {
			var account store.Account
			select {
			case <-ctx.Done():
				return nil
			case account = <-s.accCh:
			}

			if oldBot, ok := s.knownBots.LoadAndDelete(account.UserNumber); ok {
				oldBot.stop()
			}

			accountStore, err := s.store.Account(ctx, account.UserNumber)
			if err != nil {
				s.logger.Error(
					"failed to load account from database",
					"user_number", account.UserNumber,
					"err", err)
				continue
			}

			accountBot := bot.NewSession(
				accountStore,
				s,
				s.logger.With("module", "bot"))

			actx, acancel := context.WithCancel(ctx)

			s.knownBots.Store(account.UserNumber, startedBot{
				Session: accountBot,
				stop:    acancel,
			})

			errg.Go(func() error {
				s.startAccount(actx, accountBot)
				return nil
			})
		}
	})

	errg.Go(func() error {
		accounts, err := s.store.Accounts(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to load accounts")
		}

		for _, account := range accounts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case s.accCh <- account:
				// ok
			}
		}

		return nil
	})

	return errg.Wait()
}

func (s *Service) startAccount(ctx context.Context, b *bot.Session) error {
	const maxRetries = 3

	if err := b.Start(ctx); err != nil && ctx.Err() == nil {
		s.logger.Error(
			"failed to connect to Discord for user",
			"user_number", b.Account.UserNumber,
			"err", err)

		s.SendMessage(ctx, &twismsproto.Message{
			From: b.Account.ServerNumber,
			To:   b.Account.UserNumber,
			Body: &twismsproto.MessageBody{
				Text: &twismsproto.TextBody{
					Text: fmt.Sprintf("Sorry, we couldn't connect to Discord: %v", err),
				},
			},
		})
	}

	return ctx.Err()
}

// SendMessage sends a message through the Service's message channel.
// Messages sent here will go through the MessageSubscriber.
func (s *Service) SendMessage(ctx context.Context, msg *twismsproto.Message) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.sendCh <- msg:
		return nil
	}
}

func (s *Service) SendingNumber() (string, float64) {
	return "", math.Inf(+1)
}

// ConfigurationValues implements [twicmd.ConfigurableService].
func (s *Service) ConfigurationValues(ctx context.Context, req *twicmdcfgpb.OptionsRequest) (*twicmdcfgpb.OptionsResponse, error) {
	values := make([]*twicmdcfgpb.OptionValue, 0, len(optionFuncs))
	for _, opt := range optionFuncs {
		v, err := opt(s, ctx, req.PhoneNumber)
		if err != nil {
			s.logger.Error(
				"failed to get configuration value",
				"user_number", req.PhoneNumber,
				"err", err)
			return nil, errors.New("failed to get configuration value")
		}
		values = append(values, v)
	}
	return &twicmdcfgpb.OptionsResponse{Values: values}, nil
}

// ApplyConfigurationValues implements [twicmd.ConfigurableService].
func (s *Service) ApplyConfigurationValues(context.Context, *twicmdcfgpb.ApplyRequest) (*twicmdcfgpb.ApplyResponse, error) {
	return nil, fmt.Errorf("not implemented")
}
