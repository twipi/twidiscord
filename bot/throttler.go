package bot

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/puzpuzpuz/xsync/v3"
)

type messageThrottlers struct {
	throttlers *xsync.MapOf[discord.ChannelID, *messageThrottler]
	logger     *slog.Logger
	wg         sync.WaitGroup

	send      func(discord.ChannelID, []discord.MessageID)
	batchSize int
}

func newMessageThrottlers(batchSize int, logger *slog.Logger, send func(discord.ChannelID, []discord.MessageID)) *messageThrottlers {
	return &messageThrottlers{
		throttlers: xsync.NewMapOf[discord.ChannelID, *messageThrottler](),
		logger:     logger,
		send:       send,
		batchSize:  batchSize,
	}
}

func (ts *messageThrottlers) forChannel(id discord.ChannelID) *messageThrottler {
	v, _ := ts.throttlers.LoadOrCompute(id, func() *messageThrottler {
		return newMessageThrottler(ts, id)
	})
	return v
}

type messageThrottler struct {
	*messageThrottlers
	stop atomic.Pointer[chan struct{}]

	queueMu sync.Mutex
	queue   []discord.MessageID

	chID discord.ChannelID
}

func newMessageThrottler(ts *messageThrottlers, chID discord.ChannelID) *messageThrottler {
	return &messageThrottler{
		messageThrottlers: ts,
		chID:              chID,
	}
}

// AddMessage adds a message to the queue. The message will be dispatched after
// the delay time.
func (t *messageThrottler) AddMessage(id discord.MessageID, delayDuration time.Duration) {
	t.queueMu.Lock()

	// Check for overflowing queue. If we overflow, then we'll send them off
	// right away.
	var overflow []discord.MessageID
	if len(t.queue) >= t.batchSize {
		overflow = t.queue
		t.queue = []discord.MessageID{id}
	} else {
		t.queue = append(t.queue, id)
	}

	t.queueMu.Unlock()

	t.tryStartJob(delayDuration)

	if len(overflow) > 0 {
		t.wg.Add(1)
		go func() {
			t.send(t.chID, overflow)
			t.wg.Done()
		}()
	}
}

// DelaySending adds into the current delay time. It delays the callback to
// allow the queue to accumulate more messages.
func (t *messageThrottler) DelaySending(delayDuration time.Duration) {
	// Exit if we have nothing in the queue.
	t.queueMu.Lock()
	queueLen := len(t.queue)
	t.queueMu.Unlock()

	if queueLen == 0 {
		return
	}

	t.tryStartJob(delayDuration)
}

func (t *messageThrottler) tryStartJob(delay time.Duration) {
	stop := make(chan struct{}, 1)
	if old := t.stop.Swap(&stop); old != nil {
		// Stop the old job.
		select {
		case *old <- struct{}{}:
			t.logger.Debug("stopped throttler job")
		default:
			t.logger.Debug("throttler job is already stopped, starting a new job")
		}
	}

	t.wg.Add(1)
	go func() {
		defer t.wg.Done()

		timer := time.NewTimer(delay)
		defer timer.Stop()

		t.logger.Debug(
			"started throttler job",
			"channel_id", t.chID,
			"delay", delay)

		for {
			select {
			case <-stop:
				return

			case <-timer.C:
				// Steal the queue.
				t.queueMu.Lock()
				queue := t.queue
				t.queue = nil
				t.queueMu.Unlock()

				t.logger.Debug(
					"sending queued messages",
					"channel_id", t.chID,
					"message_ids", queue)

				// Do the action.
				t.send(t.chID, queue)
				return
			}
		}
	}()
}
