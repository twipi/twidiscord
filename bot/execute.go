package bot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/pkg/errors"
	"github.com/twipi/twipi/proto/out/twicmdproto"
	"github.com/twipi/twipi/twicmd"
	"github.com/xhit/go-str2duration/v2"
)

const (
	internalError = "something went wrong on our end :("
)

// Execute executes the given command.
func (s *Session) Execute(ctx context.Context, req *twicmdproto.ExecuteRequest) (*twicmdproto.ExecuteResponse, error) {
	switch req.Command.Command {
	case "message":
		return s.executeMessage(ctx, req), nil
	case "nick":
		return s.executeNick(ctx, req), nil
	case "guild_nick":
		return s.executeGuildNick(ctx, req), nil
	case "mute":
		return s.executeMute(ctx, req), nil
	case "unmute":
		return s.executeUnmute(ctx, req), nil
	case "notifications":
		return s.executeNotifications(ctx, req), nil
	default:
		return nil, errors.New("unknown command")
	}
}

func (s *Session) internalErrorResponse(req *twicmdproto.ExecuteRequest, err error) *twicmdproto.ExecuteResponse {
	s.logger.Error(
		"Internal error while executing command",
		"command", req.Command.Command,
		"err", err)
	return twicmd.StatusResponse(internalError)
}

func (s *Session) executeMessage(ctx context.Context, req *twicmdproto.ExecuteRequest) *twicmdproto.ExecuteResponse {
	args := twicmd.MapArguments(req.Command.Arguments)

	r, err := searchChannel(ctx, s.State, s.store, "", args["channel"])
	if err != nil {
		return twicmd.StatusResponse(err.Error())
	}

	_, err = s.State.SendMessage(r.Channel.ID, args["message"])
	if err != nil {
		return s.internalErrorResponse(req, err)
	}

	return nil
}

func (s *Session) executeNick(ctx context.Context, req *twicmdproto.ExecuteRequest) *twicmdproto.ExecuteResponse {
	args := twicmd.MapArguments(req.Command.Arguments)

	r, err := searchChannel(ctx, s.State, s.store, "", args["channel"])
	if err != nil {
		return twicmd.StatusResponse(err.Error())
	}

	if err := s.store.SetChannelNickname(ctx, r.Channel.ID, args["nickname"]); err != nil {
		return s.internalErrorResponse(req, err)
	}

	response := fmt.Sprintf(
		"Set %q to channel %q.",
		args["nickname"], chName(r.Channel, true))
	return twicmd.TextResponse(response)
}

func (s *Session) executeGuildNick(ctx context.Context, req *twicmdproto.ExecuteRequest) *twicmdproto.ExecuteResponse {
	args := twicmd.MapArguments(req.Command.Arguments)
	if args["guild"] == "" {
		return twicmd.StatusResponse("you must specify a guild")
	}

	r, err := searchChannel(ctx, s.State, s.store, args["guild"], args["channel"])
	if err != nil {
		return twicmd.StatusResponse(err.Error())
	}

	if err := s.store.SetChannelNickname(ctx, r.Channel.ID, args["nickname"]); err != nil {
		return s.internalErrorResponse(req, err)
	}

	response := fmt.Sprintf(
		"Set %q to channel %q in guild %q.",
		args["nickname"], chName(r.Channel, true), r.Guild.Name)
	return twicmd.TextResponse(response)
}

func (s *Session) executeMute(ctx context.Context, req *twicmdproto.ExecuteRequest) *twicmdproto.ExecuteResponse {
	args := twicmd.MapArguments(req.Command.Arguments)

	duration, err := str2duration.ParseDuration(args["duration"])
	if err != nil {
		return twicmd.StatusResponse("failed to parse duration")
	}

	until := time.Now().Add(duration)
	if err := s.store.MuteNumber(ctx, until); err != nil {
		return s.internalErrorResponse(req, err)
	}

	response := "Muted. No more messages will be sent from Discord"
	if until.IsZero() {
		response += "."
	} else {
		response += " for " + duration.String() + "."
	}
	return twicmd.TextResponse(response)
}

func (s *Session) executeUnmute(ctx context.Context, req *twicmdproto.ExecuteRequest) *twicmdproto.ExecuteResponse {
	if err := s.store.UnmuteNumber(ctx); err != nil {
		return s.internalErrorResponse(req, err)
	}

	repsonse := "Unmuted. You will receive messages again."
	return twicmd.TextResponse(repsonse)
}

func (s *Session) executeNotifications(_ context.Context, req *twicmdproto.ExecuteRequest) *twicmdproto.ExecuteResponse {
	dms, err := s.State.Cabinet.PrivateChannels()
	if err != nil {
		return s.internalErrorResponse(req, err)
	}

	type unreadChannel struct {
		discord.Channel
		UnreadCount int
	}

	var unreads []unreadChannel

	for _, dm := range dms {
		if s.State.ChannelIsMuted(dm.ID, true) {
			continue
		}

		count := s.State.ChannelCountUnreads(dm.ID)
		if count > 0 {
			unreads = append(unreads, unreadChannel{
				Channel:     dm,
				UnreadCount: count,
			})
		}
	}

	if len(unreads) == 0 {
		return twicmd.StatusResponse("No unread messages.")
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "You have %d unread channels:\n", len(unreads))
	for _, unread := range unreads {
		fmt.Fprintf(&buf, "%s (%d)\n", chName(&unread.Channel, true), unread.UnreadCount)
	}
	return twicmd.TextResponse(buf.String())
}
