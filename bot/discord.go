package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/arikawa/v3/utils/ws"
	"github.com/diamondburned/ningen/v3"
	"github.com/twipi/twipi/proto/out/twismsproto"
)

func (s *Session) bindDiscord() {
	s.discord.AddHandler(s.onMessageCreate)
	s.discord.AddHandler(s.onMessageUpdate)
	s.discord.AddHandler(s.onTypingStart)

	s.discord.AddHandler(func(r *gateway.ReadyEvent) {
		me, _ := s.discord.Me()

		s.sessions.Lock()
		s.sessions.sessions = r.Sessions
		s.sessions.ourID = r.SessionID
		s.sessions.Unlock()

		group := slog.Group(
			"user",
			"id", me.ID,
			"tag", me.Tag())
		s.logAttrs.Store(&group)

		s.logger.Info(
			"connected to Discord",
			*s.logAttrs.Load())
	})

	s.discord.AddHandler(func(ev *ws.CloseEvent) {
		s.logger.Warn(
			"disconnected from Discord",
			"code", ev.Code,
			*s.logAttrs.Load())
	})

	s.discord.AddHandler(func(err error) {
		s.logger.Error(
			"non-fatal error from Discord",
			"err", err,
			*s.logAttrs.Load())
	})

	s.discord.AddHandler(func(sessions *gateway.SessionsReplaceEvent) {
		s.sessions.Lock()
		s.sessions.sessions = []gateway.UserSession(*sessions)
		s.sessions.Unlock()
	})

	if os.Getenv("TWIDISCORD_DEBUG") != "" {
		s.bindDiscordDebug()
	}
}

func (s *Session) bindDiscordDebug() {
	ws.EnableRawEvents = true

	os.RemoveAll("/tmp/twidiscord-events")
	os.MkdirAll("/tmp/twidiscord-events", os.ModePerm)

	var serial uint64
	s.discord.AddHandler(func(ev *ws.RawEvent) {
		b, err := json.Marshal(ev)
		if err != nil {
			return
		}

		n := atomic.AddUint64(&serial, 1)
		if err := os.WriteFile(fmt.Sprintf("/tmp/twidiscord-events/%s-%d.json", ev.OriginalType, n), b, os.ModePerm); err != nil {
			return
		}
	})
}

// hasOtherSessions returns true if the current user has other sessions opened
// right now.
func (s *Session) hasOtherSessions() bool {
	s.sessions.Lock()
	defer s.sessions.Unlock()

	for _, session := range s.sessions.sessions {
		// Ignore our session or idle sessions.
		if session.SessionID == s.sessions.ourID || session.Status == discord.IdleStatus {
			continue
		}
		return true
	}

	return false
}

func (s *Session) isValidChannel(chID discord.ChannelID) bool {
	// Check if the channel is muted. Ignore muted channels.
	return !s.discord.ChannelIsMuted(chID, true)
}

func (s *Session) isValidMessage(msg *discord.Message) bool {
	me, err := s.discord.Cabinet.Me()
	if me == nil {
		s.logger.Error(
			"failed to get self user",
			"err", err,
			*s.logAttrs.Load())
		return false
	}

	// Ignore messages sent by the current user or a bot.
	if msg.Author.ID == me.ID || msg.Author.Bot {
		return false
	}

	// If the message is from a guild, ignore it if the message didn't actually
	// mention us.
	if msg.GuildID.IsValid() && s.discord.MessageMentions(msg)&ningen.MessageNotifies == 0 {
		return false
	}

	return true
}

func (s *Session) onMessageCreate(ev *gateway.MessageCreateEvent) {
	if !s.isValidChannel(ev.ChannelID) || !s.isValidMessage(&ev.Message) {
		return
	}

	throttler := s.throttlers.forChannel(ev.ChannelID)
	throttler.AddMessage(ev.ID, 5*time.Second)
}

func (s *Session) onMessageUpdate(ev *gateway.MessageUpdateEvent) {
	if !s.isValidChannel(ev.ChannelID) {
		return
	}

	msg, _ := s.discord.Cabinet.Message(ev.ChannelID, ev.ID)
	if msg == nil || !s.isValidMessage(msg) {
		return
	}

	throttler := s.throttlers.forChannel(ev.ChannelID)
	throttler.AddMessage(ev.ID, 5*time.Second)
}

func (s *Session) onTypingStart(ev *gateway.TypingStartEvent) {
	if !s.isValidChannel(ev.ChannelID) {
		return
	}

	throttler := s.throttlers.forChannel(ev.ChannelID)
	throttler.DelaySending(10 * time.Second)
}

func (s *Session) sendMessageIDs(ctx context.Context, chID discord.ChannelID, ids []discord.MessageID) {
	logger := s.logger.With(*s.logAttrs.Load())

	if len(ids) == 0 {
		return
	}

	if !s.isValidChannel(chID) {
		return
	}

	// Check if we're muted or if we have any existing Discord sessions.
	if s.hasOtherSessions() {
		logger.Debug(
			"skipping sending messages because there are other sessions")
		return
	}

	if s.store.NumberIsMuted(ctx) {
		logger.Debug(
			"skipping sending messages because the number is muted")
		return
	}

	channel, err := s.discord.Cabinet.Channel(chID)
	if err != nil {
		logger.Error(
			"failed to get channel for sending",
			"err", err)
		return
	}

	guild, err := s.discord.Cabinet.Guild(channel.GuildID)
	if channel.GuildID.IsValid() && err != nil {
		logger.Error(
			"failed to get guild for sending",
			"err", err)
		return
	}

	// Ignore all of our efforts in keeping track of a list of IDs. We'll
	// actually just grab the earliest ID in this list.
	earliest := ids[0]

	msgs, err := s.discord.Messages(chID, 100)
	if err != nil {
		logger.Error(
			"failed to get messages for sending",
			"err", err)
		return
	}

	msgs = filterSlice(msgs, func(msg discord.Message) bool {
		return msg.ID >= earliest && s.isValidMessage(&msg)
	})
	if len(msgs) == 0 {
		return
	}

	var name string
	if nick, err := s.store.ChannelNickname(ctx, chID); err == nil {
		name = nick
	} else {
		name = chName(channel, true)
		if guild != nil {
			name = fmt.Sprintf("%s in %s", name, guild.Name)
		}
	}

	var body strings.Builder
	fmt.Fprintf(&body, "%s:\n", name)

	var lastAuthor discord.UserID

	// Iterate from earliest.
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := &msgs[i]

		// Only write the message author if it's different from the last one or
		// and we're not in a DM.
		if len(channel.DMRecipients) > 0 && lastAuthor != msg.Author.ID {
			fmt.Fprintf(&body, "%s:\n", msg.Author.DisplayOrUsername())
		}

		content := renderText(ctx, logger, s.discord, msg.Content, msg)
		body.WriteString(content)

		if len(msg.Embeds) > 0 {
			body.WriteString("\n[embed]")
		}

		if len(msg.Attachments) > 0 {
			if len(msg.Attachments) == 1 {
				fmt.Fprintf(&body, "\n[attached %s]", msg.Attachments[0].Filename)
			} else {
				fmt.Fprintf(&body, "\n[attached %d files]", len(msg.Attachments))
			}
		}

		if msg.EditedTimestamp.IsValid() {
			body.WriteString("*")
		}

		body.WriteByte('\n')
	}

	bodyFinal := strings.TrimSuffix(body.String(), "\n")

	message := &twismsproto.Message{
		From: s.Account.ServerNumber,
		To:   s.Account.UserNumber,
		Body: &twismsproto.MessageBody{
			Text: &twismsproto.TextBody{Text: bodyFinal},
		},
	}

	if err := s.sms.SendMessage(ctx, message); err != nil {
		logger.Error(
			"failed to send SMS",
			"err", err)
	}
}

func filterSlice[T any](slice []T, filter func(T) bool) []T {
	filtered := slice[:0]
	for _, v := range slice {
		if filter(v) {
			filtered = append(filtered, v)
		}
	}
	return filtered
}

func chName(ch *discord.Channel, short bool) string {
	if ch.Name != "" {
		return ch.Name
	}

	switch len(ch.DMRecipients) {
	case 0:
		return ch.ID.Mention()
	case 1:
		return ch.DMRecipients[0].Username
	default:
		recipientNames := make([]string, len(ch.DMRecipients))
		for i, recipient := range ch.DMRecipients {
			recipientNames[i] = recipient.Username
		}

		if short {
			const maxNames = 3
			names := strings.Join(recipientNames[:maxNames], ", ")
			if len(recipientNames) > maxNames {
				names += ", ..."
			}
			return names
		} else {
			return strings.Join(recipientNames, ", ")
		}
	}
}
