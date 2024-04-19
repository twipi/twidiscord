package bot

import (
	"sync"
	"time"

	"github.com/diamondburned/arikawa/v3/discord"
	"github.com/puzpuzpuz/xsync/v3"
)

type messageThrottlers struct {
	throttlers *xsync.MapOf[discord.ChannelID, *messageThrottler]
	wg         sync.WaitGroup

	send      func(discord.ChannelID, []discord.MessageID)
	batchSize int
}

func newMessageThrottlers(batchSize int, send func(discord.ChannelID, []discord.MessageID)) *messageThrottlers {
	return &messageThrottlers{
		throttlers: xsync.NewMapOf[discord.ChannelID, *messageThrottler](),
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
	queue   []discord.MessageID
	queueMu sync.Mutex
	timer   struct {
		sync.Mutex
		reset chan time.Duration
	}

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
	var overflow []discord.MessageID

	t.queueMu.Lock()
	// Check for overflowing queue. If we overflow, then we'll send them off
	// right away.
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
	t.timer.Lock()
	defer t.timer.Unlock()

	if t.timer.reset == nil {
		t.timer.reset = make(chan time.Duration, 1)
	}

	// Already started. Just send the delay over. We'll just try and send
	// the duration, however if that doesn't immediately work, we'll just
	// spawn a new goroutine to do our job.
	select {
	case t.timer.reset <- delay:
		return
	case <-t.timer.reset:
		// If this case ever hits, then the worker is probably waiting for
		// the mutex to unlock. We should be able to just spawn a new
		// goroutine with the current delay.
	}

	t.wg.Add(1)
	go func() {
		defer t.wg.Done()

		timer := time.NewTimer(delay)
		for {
			select {
			case d := <-t.timer.reset:
				if !timer.Stop() {
					<-timer.C
				}
				timer.Reset(d)

			case <-timer.C:
				// Steal the queue.
				t.queueMu.Lock()
				queue := t.queue
				t.queue = nil
				t.queueMu.Unlock()

				// Do the action.
				t.send(t.chID, queue)
				return
			}
		}
	}()
}
