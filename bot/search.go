package bot

import (
	"context"
	"fmt"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/ningen/v3"
	"github.com/diamondburned/twidiscord/store"
	"github.com/pkg/errors"
	"github.com/sahilm/fuzzy"
)

type channelSearchResult struct {
	Channel *discord.Channel
	Guild   *discord.Guild
}

func searchChannel(ctx context.Context, state *ningen.State, account store.AccountStore, guildSearch, channelSearch string) (*channelSearchResult, error) {
	// Search for any channel nicknames first.
	if id, err := account.ChannelFromNickname(ctx, channelSearch); err == nil {
		channel, err := state.Offline().Channel(id)
		if err != nil {
			return nil, fmt.Errorf("failed to get nicknamed channel: %w", err)
		}

		var guild *discord.Guild
		if channel.GuildID.IsValid() {
			guild, err = state.Offline().Guild(channel.GuildID)
			if err != nil {
				return nil, fmt.Errorf("failed to get guild: %w", err)
			}
		}

		return &channelSearchResult{
			Channel: channel,
			Guild:   guild,
		}, nil
	}

	var guild *discord.Guild
	var channels []discord.Channel
	var err error

	if guildSearch != "" {
		// Permit searching guild channels.
		guilds, err := state.Offline().Guilds()
		if err != nil {
			return nil, fmt.Errorf("failed to get list of guilds: %w", err)
		}

		guild = matchGuild(guilds, guildSearch)
		if guild == nil {
			return nil, errors.New("no such guild")
		}

		channels, err = state.Offline().Channels(guild.ID, []discord.ChannelType{
			discord.GuildText,
			discord.GuildVoice,
			discord.GuildPublicThread,
			discord.GuildPrivateThread,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get list of channels: %w", err)
		}
	} else {
		channels, err = state.Offline().PrivateChannels()
		if err != nil {
			return nil, fmt.Errorf("failed to get list of private channels: %w", err)
		}
	}

	channel := matchChannel(channels, channelSearch)
	if channel == nil {
		return nil, errors.New("no such channel")
	}

	return &channelSearchResult{
		Channel: channel,
		Guild:   guild,
	}, nil
}

func matchGuild(guilds []discord.Guild, search string) *discord.Guild {
	matches := fuzzy.FindFromNoSort(search, fuzzyGuilds(guilds))
	bestMatch, ok := bestFuzzyMatch(matches)
	if !ok {
		return nil
	}
	return &guilds[bestMatch.Index]
}

type fuzzyGuilds []discord.Guild

var _ fuzzy.Source = fuzzyGuilds{}

func (g fuzzyGuilds) Len() int {
	return len(g)
}

func (g fuzzyGuilds) String(i int) string {
	return g[i].Name
}

func matchChannel(channels []discord.Channel, search string) *discord.Channel {
	matches := fuzzy.FindFromNoSort(search, fuzzyChannels(channels))
	bestMatch, ok := bestFuzzyMatch(matches)
	if !ok {
		return nil
	}
	return &channels[bestMatch.Index]
}

type fuzzyChannels []discord.Channel

var _ fuzzy.Source = fuzzyChannels{}

func (c fuzzyChannels) Len() int {
	return len(c)
}

func (c fuzzyChannels) String(i int) string {
	return chName(&c[i], false)
}

func bestFuzzyMatch(matches []fuzzy.Match) (fuzzy.Match, bool) {
	if len(matches) == 0 {
		return fuzzy.Match{}, false
	}

	iMax := 0
	for i, match := range matches[1:] {
		if match.Score > matches[iMax].Score {
			iMax = i + 1
		}
	}

	return matches[iMax], true
}
