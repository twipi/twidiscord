package bot

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/twidiscord/twidiscord"
	"github.com/diamondburned/twikit/twicli"
	"github.com/pkg/errors"
)

// Command implements twid.HandlerCommander.
func (h *Handler) Command() twicli.Command {
	return h.cmd
}

func (h *Handler) initCommand() {
	h.cmd = twicli.Command{
		Prefix: twicli.CombinePrefixes(
			twicli.NewSlashPrefix("discord"),
			twicli.NewNaturalPrefix("Discord"),
		),
		Action: twicli.Subcommands([]twicli.Command{
			{
				Prefix: twicli.NewWordPrefix("message", true),
				Action: h.sendMessage,
			},
			{
				Prefix: twicli.NewWordPrefix("mute", true),
				Action: h.sendMute,
			},
			{
				Prefix: twicli.NewWordPrefix("unmute", true),
				Action: h.sendUnmute,
			},
			{
				Prefix: twicli.NewWordPrefix("help", true),
				Action: h.sendHelp,
			},
			{
				Prefix: twicli.NewWordPrefix("summarize", true),
				Action: h.sendSummarize,
			},
		}),
	}
}

func (h *Handler) sendHelp(_ context.Context, src twicli.Message) error {
	return h.twipi.Client.ReplySMS(h.ctx, src.Message, "Usages:\n"+
		"Discord, message ^0 content\n"+
		"Discord, message ^0 the first part (...)\n"+
		"Discord, message ^0 the final part\n"+
		"Discord, message alieb Hello!\n"+
		"Discord, summarize\n"+
		"Discord, mute\n"+
		"Discord, unmute\n"+
		"Discord, help",
	)
}

func (h *Handler) sendMute(_ context.Context, src twicli.Message) error {
	if err := h.store.SetNumberMuted(h.ctx, h.TwilioNumber, true); err != nil {
		return err
	}

	return h.twipi.Client.ReplySMS(h.ctx, src.Message,
		"Muted. No more messages will be sent from Discord.")
}

func (h *Handler) sendUnmute(_ context.Context, src twicli.Message) error {
	if err := h.store.SetNumberMuted(h.ctx, h.TwilioNumber, false); err != nil {
		return err
	}

	return h.twipi.Client.ReplySMS(h.ctx, src.Message,
		"Unmuted. You will receive messages again.")
}

func (h *Handler) sendSummarize(_ context.Context, src twicli.Message) error {
	dms, err := h.discord.PrivateChannels()
	if err != nil {
		return err
	}

	type unreadChannel struct {
		discord.Channel
		UnreadCount int
	}

	var unreads []unreadChannel

	for _, dm := range dms {
		if h.discord.ChannelIsMuted(dm.ID, true) {
			continue
		}

		if count := h.discord.ChannelCountUnreads(dm.ID); count > 0 {
			unreads = append(unreads, unreadChannel{
				Channel:     dm,
				UnreadCount: count,
			})
		}
	}

	if len(unreads) == 0 {
		return h.twipi.Client.ReplySMS(h.ctx, src.Message, "No unread messages.")
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "You have %d unread channels:\n", len(unreads))
	for _, unread := range unreads {
		fmt.Fprintf(&buf, "%s (%d)\n", chName(unread.Channel), unread.UnreadCount)
	}

	return h.twipi.Client.ReplySMS(h.ctx, src.Message, buf.String())
}

var (
	tagSerialRe = regexp.MustCompile(`^\^(\d+)$`)
	// tagChIDRe   = regexp.MustCompile(`<#(\d+)>`)
	// tagUserIDRe = regexp.MustCompile(`<@!?(\d+)>`)
)

func (h *Handler) sendMessage(_ context.Context, src twicli.Message) error {
	ref, content, err := twicli.PopFirstWord(src.Body)
	if err != nil {
		return err
	}

	if strings.HasSuffix(content, "(...)") {
		if strings.HasSuffix(content, `\(...)`) {
			// The user escaped the ellipsis. Remove the escape.
			content = strings.TrimSuffix(content, `\(...)`) + "(...)"
		} else {
			// Store the message as a fragment.
			h.fragmentMu.Lock()
			h.fragments[ref] = messageFragment{
				content: content,
			}
			h.fragmentMu.Unlock()
			return nil
		}
	} else {
		// Check for previous fragments.
		h.fragmentMu.Lock()
		frag, ok := h.fragments[ref]
		if ok {
			content = frag.content + content
			delete(h.fragments, ref)
		}
		h.fragmentMu.Unlock()
	}

	chID, err := h.matchChReference(ref)
	if err != nil {
		return err
	}

	_, err = h.discord.SendMessage(chID, content)
	if err != nil {
		return err
	}

	return nil
}

func (h *Handler) matchChReference(str string) (discord.ChannelID, error) {
	me, err := h.discord.Me()
	if err != nil {
		return 0, err
	}

	if matches := tagSerialRe.FindStringSubmatch(str); matches != nil {
		n, err := strconv.Atoi(matches[1])
		if err != nil {
			return 0, errors.Wrap(err, "invalid serial")
		}

		chID, err := h.store.SerialToChannel(h.ctx, me.ID, n)
		if err != nil {
			if errors.Is(err, twidiscord.ErrNotFound) {
				return 0, errors.New("no such serial")
			}
			return 0, errors.Wrap(err, "failed to lookup given serial")
		}

		return chID, nil
	}

	dms, err := h.discord.Cabinet.PrivateChannels()
	if err != nil {
		return 0, errors.Wrap(err, "failed to list private channels")
	}

	if err := twicli.ValidatePattern(str); err != nil {
		return 0, errors.Wrap(err, "invalid channel name")
	}

	ch := matchDM(dms, str)
	if ch == nil {
		return 0, errors.New("no such channel")
	}

	return ch.ID, nil
}

func matchDM(dms []discord.Channel, str string) *discord.Channel {
	for i, dm := range dms {
		if twicli.PatternMatch(chName(dm), str) {
			return &dms[i]
		}
	}

	return nil
}
