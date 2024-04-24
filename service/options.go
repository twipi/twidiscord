package service

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/twidiscord/bot"
	"github.com/twipi/twipi/proto/out/twicmdcfgpb"
)

type optionFunc func(s *Service, ctx context.Context, phoneNumber string) (*twicmdcfgpb.OptionValue, error)

var optionFuncs = []optionFunc{
	(*Service).optionDiscordToken,
	(*Service).optionNicknames,
}

func (s *Service) optionDiscordToken(ctx context.Context, phoneNumber string) (*twicmdcfgpb.OptionValue, error) {
	account, err := s.store.Account(ctx, phoneNumber)
	if err != nil {
		return nil, fmt.Errorf("no account found")
	}

	return &twicmdcfgpb.OptionValue{
		Id: "discord_token",
		Value: &twicmdcfgpb.OptionValue_String_{
			String_: account.Account().DiscordToken,
		},
	}, nil
}

func (s *Service) optionNicknames(ctx context.Context, phoneNumber string) (*twicmdcfgpb.OptionValue, error) {
	bot, ok := s.knownBots.Load(phoneNumber)
	if !ok {
		return nil, fmt.Errorf("account not ready, try again later")
	}

	botID, err := bot.Me()
	if err != nil {
		return nil, fmt.Errorf("failed to get account ID: %w", err)
	}

	account, err := s.store.Account(ctx, phoneNumber)
	if err != nil {
		return nil, fmt.Errorf("no account found")
	}

	logger := s.logger.With(
		"user_number", phoneNumber,
		"bot.id", botID.ID,
		"bot.tag", botID.Tag,
	)

	channelNicks, err := account.ChannelNicknames(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get channel nicknames: %w", err)
	}

	guilds := make(map[discord.GuildID]*discord.Guild)
	channels := make([]channelNickItem, 0, len(channelNicks))

	for chID, nick := range channelNicks {
		item := channelNickItem{
			Nickname:  nick,
			ChannelID: chID,
		}

		ch, err := bot.Session.Cabinet.Channel(chID)
		if err != nil {
			logger.Warn(
				"could not get channel",
				"channel_id", chID)
		} else {
			item.Channel = ch
		}

		if ch.GuildID.IsValid() {
			_, ok := guilds[ch.GuildID]
			if ok {
				item.Guild = guilds[ch.GuildID]
			} else {
				g, err := bot.Session.Cabinet.Guild(ch.GuildID)
				if err != nil {
					logger.Warn(
						"could not get guild for channel",
						"channel_id", ch.GuildID,
						"guild_id", ch.GuildID)
				} else {
					guilds[g.ID] = g
					item.Guild = g
				}
			}
		}
	}

	slices.SortFunc(channels, func(a, b channelNickItem) int {
		as := a.sortString()
		bs := b.sortString()
		return strings.Compare(as, bs)
	})

	values := make([]string, len(channels))
	for i, c := range channels {
		values[i] = c.itemString()
	}

	return &twicmdcfgpb.OptionValue{
		Id: "nicknames",
		Value: &twicmdcfgpb.OptionValue_StringList{
			StringList: &twicmdcfgpb.StringListValue{
				Values: values,
			},
		},
	}, nil
}

type channelNickItem struct {
	Nickname  string
	ChannelID discord.ChannelID
	Channel   *discord.Channel
	Guild     *discord.Guild
}

func (i channelNickItem) sortString() string {
	var s strings.Builder
	if i.Channel != nil {
		s.WriteString(bot.ChannelName(i.Channel, true))
	}
	if i.Guild != nil {
		s.WriteString(i.Guild.Name)
	}
	s.WriteString(i.Nickname)
	return s.String()
}

func (i channelNickItem) itemString() string {
	var s strings.Builder
	s.WriteString(i.Nickname)
	s.WriteByte('\t')
	if i.Channel != nil {
		s.WriteString(bot.ChannelName(i.Channel, true))
	} else {
		s.WriteString(i.ChannelID.String())
	}
	s.WriteByte('\t')
	if i.Guild != nil {
		s.WriteString(i.Guild.Name)
	} else if i.Channel != nil && !i.Channel.GuildID.IsValid() {
		s.WriteString("")
	} else {
		s.WriteString("?")
	}
	return s.String()
}
