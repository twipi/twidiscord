package bot

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/diamondburned/arikawa/v3/gateway"
	"github.com/diamondburned/arikawa/v3/utils/ws"
	"github.com/diamondburned/twikit/logger"
	"github.com/diamondburned/twikit/twipi"
)

func (h *Handler) bindDiscord() {
	h.discord.AddHandler(h.onMessageCreate)
	h.discord.AddHandler(h.onMessageUpdate)
	h.discord.AddHandler(h.onTypingStart)

	var tag string
	h.discord.AddHandler(func(r *gateway.ReadyEvent) {
		me, _ := h.discord.Me()
		tag = me.Tag()

		h.sessions.Lock()
		h.sessions.sessions = r.Sessions
		h.sessions.ourID = r.SessionID
		h.sessions.Unlock()

		log := logger.FromContext(h.ctx)
		log.Printf("connected to Discord account %q", tag)
	})

	h.discord.AddHandler(func(closeEv *ws.CloseEvent) {
		log := logger.FromContext(h.ctx)
		log.Printf("disconnected from Discord account %q (code %d)", tag, closeEv.Code)
	})

	h.discord.AddHandler(func(err error) {
		log := logger.FromContext(h.ctx)
		log.Printf("non-fatal error from Discord account %q: %v", tag, err)
	})

	h.discord.AddHandler(func(sessions *gateway.SessionsReplaceEvent) {
		h.sessions.Lock()
		h.sessions.sessions = []gateway.UserSession(*sessions)
		h.sessions.Unlock()
	})

	if os.Getenv("TWIDISCORD_DEBUG") != "" {
		h.bindDiscordDebug()
	}
}

func (h *Handler) bindDiscordDebug() {
	ws.EnableRawEvents = true

	os.RemoveAll("/tmp/twidiscord-events")
	os.MkdirAll("/tmp/twidiscord-events", os.ModePerm)

	var serial uint64
	h.discord.AddHandler(func(ev *ws.RawEvent) {
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
func (h *Handler) hasOtherSessions() bool {
	h.sessions.Lock()
	defer h.sessions.Unlock()

	for _, session := range h.sessions.sessions {
		// Ignore our session or idle sessions.
		if session.SessionID == h.sessions.ourID || session.Status == discord.IdleStatus {
			continue
		}
		return true
	}

	return false
}

func (h *Handler) isValidChannel(chID discord.ChannelID) bool {
	// Check if the channel is muted. Ignore muted channels.
	return !h.discord.ChannelIsMuted(chID, true)
}

func (h *Handler) isValidMessage(msg *discord.Message) bool {
	me, _ := h.discord.Cabinet.Me()
	if me == nil {
		log := logger.FromContext(h.ctx)
		log.Println("failed to get self user")
		return false
	}

	// Ignore messages sent by the current user or a bot.
	if msg.Author.ID == me.ID || msg.Author.Bot {
		return false
	}

	if msg.GuildID.IsValid() {
		return false
	}

	return true
}

func (h *Handler) onMessageCreate(ev *gateway.MessageCreateEvent) {
	if !h.isValidChannel(ev.ChannelID) || !h.isValidMessage(&ev.Message) {
		return
	}

	throttler := h.messageThrottlers.forChannel(ev.ChannelID)
	throttler.AddMessage(ev.ID, 5*time.Second)
}

func (h *Handler) onMessageUpdate(ev *gateway.MessageUpdateEvent) {
	if !h.isValidChannel(ev.ChannelID) {
		return
	}

	msg, _ := h.discord.Cabinet.Message(ev.ChannelID, ev.ID)
	if msg == nil || !h.isValidMessage(msg) {
		return
	}

	throttler := h.messageThrottlers.forChannel(ev.ChannelID)
	throttler.AddMessage(ev.ID, 5*time.Second)
}

func (h *Handler) onTypingStart(ev *gateway.TypingStartEvent) {
	if !h.isValidChannel(ev.ChannelID) {
		return
	}

	throttler := h.messageThrottlers.forChannel(ev.ChannelID)
	throttler.DelaySending(10 * time.Second)
}

func (h *Handler) sendMessageIDs(chID discord.ChannelID, ids []discord.MessageID) {
	if len(ids) == 0 {
		return
	}

	// Check if we're muted or if we have any existing Discord sessions.
	if h.hasOtherSessions() || h.store.NumberIsMuted(h.ctx, h.TwilioNumber) {
		return
	}

	if !h.isValidChannel(chID) {
		return
	}

	// Ignore all of our efforts in keeping track of a list of IDs. We'll
	// actually just grab the earliest ID in this list.
	earliest := ids[0]

	msgs, err := h.discord.Messages(chID, 100)
	if err != nil {
		return
	}

	filtered := msgs[:0]
	for i, msg := range msgs {
		if msg.ID >= earliest && h.isValidMessage(&msgs[i]) {
			filtered = append(filtered, msg)
		}
	}

	if len(filtered) == 0 {
		return
	}

	me, _ := h.discord.Cabinet.Me()
	if me == nil {
		return
	}

	serial, err := h.store.ChannelToSerial(h.ctx, me.ID, chID)
	if err != nil {
		log := logger.FromContext(h.ctx)
		log.Printf("twidiscord: failed to get serial for %s: %v", chID, err)
		return
	}

	var body strings.Builder
	fmt.Fprintf(&body, "^%d: %s: ", serial, filtered[0].Author.Tag())

	// Iterate from earliest.
	for i := len(filtered) - 1; i >= 0; i-- {
		msg := &filtered[i]
		body.WriteString(renderText(h.ctx, h.discord, msg.Content, msg))

		if len(msg.Embeds) > 0 {
			if len(msg.Embeds) == 1 {
				body.WriteString("\n[1 embed]")
			} else {
				fmt.Fprintf(&body, "\n[%d embeds]", len(msg.Embeds))
			}
		}

		for _, attachment := range msg.Attachments {
			fmt.Fprintf(&body, "\n[attached %s]", attachment.Filename)
		}

		if msg.EditedTimestamp.IsValid() {
			body.WriteString("*")
		}

		body.WriteByte('\n')
	}

	err = h.twipi.Client.SendSMS(h.ctx, twipi.Message{
		From: h.TwilioNumber,
		To:   h.UserNumber,
		Body: strings.TrimSuffix(body.String(), "\n"),
	})
	if err != nil {
		log := logger.FromContext(h.ctx)
		log.Println("cannot send SMS on message:", err)
		return
	}
}
