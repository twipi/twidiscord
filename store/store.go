package store

import (
	"context"
	"errors"
	"time"

	"github.com/diamondburned/arikawa/v3/discord"
)

// ErrNotFound is returned by stores in case of a not found error.
var ErrNotFound = errors.New("not found")

type PhoneNumber = string

type Store interface {
	// Account returns an account store by its phone number.
	Account(context.Context, PhoneNumber) (AccountStore, error)
	// Accounts returns all accounts.
	Accounts(context.Context) ([]Account, error)
	// SetAccount sets an account.
	SetAccount(context.Context, Account) error
}

type AccountStore interface {
	// Account returns the account that the store is associated with.
	Account() Account

	// NumberIsMuted returns whether a number is muted or not.
	NumberIsMuted(context.Context) bool
	// MuteNumber mutes a number until the given time.
	MuteNumber(context.Context, time.Time) error
	// UnmuteNumber unmutes a number.
	UnmuteNumber(context.Context) error

	// ChannelNickname returns the nickname of a channel.
	ChannelNickname(context.Context, discord.ChannelID) (string, error)
	// ChannelFromNickname returns the channel ID from a nickname.
	ChannelFromNickname(context.Context, string) (discord.ChannelID, error)
	// SetChannelNickname sets the nickname of a channel.
	SetChannelNickname(context.Context, discord.ChannelID, string) error
}

type Account struct {
	UserNumber   PhoneNumber // key
	ServerNumber PhoneNumber
	DiscordToken string
}

// InternalError is returned by stores in case of an internal error.
type InternalError struct {
	Err error
}

func (e InternalError) Error() string {
	return "internal error: " + e.Err.Error()
}

func (e InternalError) Unwrap() error {
	return e.Err
}
