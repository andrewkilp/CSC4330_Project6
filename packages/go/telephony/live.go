package telephony

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/conversation"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/telephony/media"
)

// Calls is the in-process registry of live provider work for each call: the
// telephony provider and provider call id that placed it, its outbound media stream,
// and its conversation loop. It holds no durable state. Call controls (#31) apply a
// committed decision here, so every method is idempotent and safe to repeat after a
// retried request or a restart of the control path.
//
// End leaves a tombstone. Anything that attaches to an ended call later (a start
// that was still dialing, a media stream that connects late, a loop) is shut down
// immediately instead of being registered, so no audio or generation outlives end.
type Calls struct {
	mu    sync.Mutex
	calls map[string]*liveCall
	ended map[string]struct{}
	// HangupTimeout bounds a provider hang-up. Zero means 5 s.
	HangupTimeout time.Duration
}

type liveCall struct {
	provider   providers.Telephony
	providerID string
	media      providers.MediaStream
	loop       *conversation.Loop
	playback   *media.Playback
	ctx        context.Context
	cancel     context.CancelFunc
}

// maxTombstones bounds memory for a long-running single instance. Beyond it the
// oldest information is not needed: durable call state already rejects new work.
const maxTombstones = 10000

func NewCalls() *Calls {
	return &Calls{calls: map[string]*liveCall{}, ended: map[string]struct{}{}}
}

// ErrCallEnded reports an attempt to attach work to a call that has ended.
var ErrCallEnded = errors.New("call has ended")

func (c *Calls) entryLocked(callID string) *liveCall {
	e := c.calls[callID]
	if e == nil {
		ctx, cancel := context.WithCancel(context.Background())
		e = &liveCall{ctx: ctx, cancel: cancel}
		c.calls[callID] = e
	}
	return e
}

func (c *Calls) isEndedLocked(callID string) bool {
	_, ok := c.ended[callID]
	return ok
}

func (c *Calls) hangupTimeout() time.Duration {
	if c.HangupTimeout > 0 {
		return c.HangupTimeout
	}
	return 5 * time.Second
}

// Started records the provider that placed a call. If the call was ended while the
// provider was still dialing, it hangs up at once and returns ErrCallEnded.
func (c *Calls) Started(callID string, provider providers.Telephony, providerCallID string) error {
	if provider == nil || providerCallID == "" {
		return providers.ErrInvalid
	}
	c.mu.Lock()
	if c.isEndedLocked(callID) {
		c.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), c.hangupTimeout())
		defer cancel()
		_ = provider.End(ctx, providerCallID)
		return ErrCallEnded
	}
	e := c.entryLocked(callID)
	e.provider, e.providerID = provider, providerCallID
	c.mu.Unlock()
	return nil
}

// AttachMedia registers the call's outbound stream, closing any previous one. A
// stream for an ended call is closed instead.
func (c *Calls) AttachMedia(callID string, stream providers.MediaStream) error {
	if stream == nil {
		return providers.ErrInvalid
	}
	c.mu.Lock()
	if c.isEndedLocked(callID) {
		c.mu.Unlock()
		_ = stream.Close()
		return ErrCallEnded
	}
	e := c.entryLocked(callID)
	previous := e.media
	e.media = stream
	c.mu.Unlock()
	if previous != nil && previous != stream {
		_ = previous.Close()
	}
	return nil
}

// DetachMedia forgets the stream if it is still the registered one (after stop).
func (c *Calls) DetachMedia(callID string, stream providers.MediaStream) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.calls[callID]; e != nil && e.media == stream {
		e.media = nil
	}
}

// Media returns the registered outbound stream.
func (c *Calls) Media(callID string) (providers.MediaStream, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.calls[callID]; e != nil && e.media != nil {
		return e.media, true
	}
	return nil, false
}

// AttachLoop registers the call's conversation loop. A loop for an ended call is
// moved to its terminal state instead.
func (c *Calls) AttachLoop(callID string, loop *conversation.Loop) error {
	if loop == nil || loop.CallID() != callID {
		return providers.ErrInvalid
	}
	c.mu.Lock()
	if c.isEndedLocked(callID) {
		c.mu.Unlock()
		_ = loop.Transition(conversation.Ended)
		return ErrCallEnded
	}
	c.entryLocked(callID).loop = loop
	c.mu.Unlock()
	return nil
}

// Loop returns the registered conversation loop.
func (c *Calls) Loop(callID string) (*conversation.Loop, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.calls[callID]; e != nil && e.loop != nil {
		return e.loop, true
	}
	return nil, false
}

// Context is cancelled when the call ends. Background work for the call (such as
// generation after resume) must use it.
func (c *Calls) Context(callID string) context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.isEndedLocked(callID) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}
	return c.entryLocked(callID).ctx
}

// Ended reports whether End has run for the call.
func (c *Calls) Ended(callID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isEndedLocked(callID)
}

func (c *Calls) snapshot(callID string) (loop *conversation.Loop, media providers.MediaStream, ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.calls[callID]; e != nil {
		return e.loop, e.media, e.ctx
	}
	return nil, nil, nil
}

// Pause cancels in-flight generation and synthesis and drops queued speech while
// keeping the line open. Already-spoken audio is unaffected.
func (c *Calls) Pause(ctx context.Context, callID string) error {
	c.takePlayback(callID)
	loop, media, _ := c.snapshot(callID)
	if loop != nil {
		// A loop that is not connected has nothing to cancel.
		if err := loop.Pause(); err != nil && !errors.Is(err, conversation.ErrInvalidTransition) {
			return err
		}
	}
	if media != nil {
		if _, err := media.Clear(ctx); err != nil && !errors.Is(err, providers.ErrClosed) {
			return fmt.Errorf("%w: clear queued audio", providers.ErrUnavailable)
		}
	}
	return nil
}

// Resume lets the assistant continue from the newest finalized turns and applied
// instructions. Generation runs in the background on the call's context, so the
// control request returns at once and end cancels it.
func (c *Calls) Resume(callID string) {
	loop, _, ctx := c.snapshot(callID)
	if loop == nil || ctx == nil {
		return
	}
	go func() { _ = loop.Resume(ctx) }()
}

// End stops everything for the call, exactly once: background work is cancelled,
// the loop becomes terminal, queued audio is dropped and the stream closed, and the
// provider call is hung up. Repeating End is a no-op. A hang-up failure is returned
// (wrapping providers.ErrUnavailable) after all local work has stopped; repeating
// End retries only the hang-up.
func (c *Calls) End(ctx context.Context, callID string) error {
	c.mu.Lock()
	e := c.calls[callID]
	delete(c.calls, callID)
	if len(c.ended) >= maxTombstones {
		for id := range c.ended {
			delete(c.ended, id)
			break
		}
	}
	c.ended[callID] = struct{}{}
	c.mu.Unlock()
	if e == nil {
		return nil
	}
	e.cancel()
	if e.playback != nil {
		e.playback.Clear()
	}
	if e.loop != nil {
		_ = e.loop.Transition(conversation.Ended)
	}
	if e.media != nil {
		_, _ = e.media.Clear(ctx)
		_ = e.media.Close()
	}
	if e.provider == nil {
		return nil
	}
	hangup, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.hangupTimeout())
	defer cancel()
	if err := e.provider.End(hangup, e.providerID); err != nil {
		// Keep the provider so a retried End can hang up again.
		c.mu.Lock()
		if c.calls[callID] == nil {
			c.calls[callID] = &liveCall{provider: e.provider, providerID: e.providerID, ctx: e.ctx, cancel: e.cancel}
		}
		c.mu.Unlock()
		return fmt.Errorf("%w: hang up", providers.ErrUnavailable)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Playback and remote interruption (#26).

// BeginSpeech registers the playback ledger of the response now being sent to the
// call. A previous ledger is frozen, so its outstanding marks become stale.
func (c *Calls) BeginSpeech(callID string, p *media.Playback) error {
	if p == nil {
		return providers.ErrInvalid
	}
	c.mu.Lock()
	if c.isEndedLocked(callID) {
		c.mu.Unlock()
		p.Clear()
		return ErrCallEnded
	}
	e := c.entryLocked(callID)
	previous := e.playback
	e.playback = p
	c.mu.Unlock()
	if previous != nil && previous != p {
		previous.Clear()
	}
	return nil
}

// Acknowledge applies a playback mark reported by the provider. It returns the
// response id and whether its playback is complete (the caller then calls
// Loop.CompletePlayback). A mark for audio that was cleared, or for any response other
// than the one playing, is media.ErrStaleMark and must be ignored.
func (c *Calls) Acknowledge(callID, mark string) (responseID string, complete bool, err error) {
	c.mu.Lock()
	e := c.calls[callID]
	var p *media.Playback
	if e != nil {
		p = e.playback
	}
	c.mu.Unlock()
	if p == nil {
		return "", false, media.ErrStaleMark
	}
	complete, err = p.Acknowledge(mark)
	if err != nil {
		return "", false, err
	}
	if complete {
		c.mu.Lock()
		if e := c.calls[callID]; e != nil && e.playback == p {
			e.playback = nil
		}
		c.mu.Unlock()
	}
	return p.ResponseID(), complete, nil
}

// Interruption describes speech cut off by the business.
type Interruption struct {
	ResponseID string
	// Spoken is what the business actually heard, at a word boundary. Record it as
	// the assistant's turn for ResponseID when it is not empty.
	Spoken string
	// PlayedBytes and DroppedBytes are the acknowledged and cleared outbound audio.
	PlayedBytes, DroppedBytes int
}

func (c *Calls) takePlayback(callID string) *media.Playback {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.calls[callID]
	if e == nil || e.playback == nil {
		return nil
	}
	p := e.playback
	e.playback = nil
	p.Clear()
	return p
}

// ObserveRemoteSpeech is the interruption detector. The selected recognizer (#20)
// streams interim results, so the first non-empty transcript update while the
// assistant's audio is still playing means the business has started talking. It then
// interrupts: the playback ledger is frozen (outstanding marks become stale), Twilio's
// buffer is cleared, and the response is cancelled with the words actually heard.
// It returns nil when nothing was playing. Call it before passing the update to the
// conversation loop, which would otherwise cancel the response without its spoken part.
func (c *Calls) ObserveRemoteSpeech(ctx context.Context, callID string, u providers.TranscriptUpdate) (*Interruption, error) {
	if strings.TrimSpace(u.Text) == "" {
		return nil, nil
	}
	c.mu.Lock()
	e := c.calls[callID]
	active := e != nil && e.playback != nil && e.playback.Active()
	c.mu.Unlock()
	if !active {
		return nil, nil
	}
	return c.Interrupt(ctx, callID)
}

// Interrupt stops the playing response now. It is idempotent: with nothing playing
// it returns nil.
func (c *Calls) Interrupt(ctx context.Context, callID string) (*Interruption, error) {
	p := c.takePlayback(callID)
	if p == nil {
		return nil, nil
	}
	played, queued := p.Progress()
	loop, stream, _ := c.snapshot(callID)
	out := &Interruption{ResponseID: p.ResponseID(), PlayedBytes: played}
	var clearErr error
	if stream != nil {
		dropped, err := stream.Clear(ctx)
		out.DroppedBytes = dropped
		if err != nil && !errors.Is(err, providers.ErrClosed) {
			clearErr = fmt.Errorf("%w: clear queued audio", providers.ErrUnavailable)
		}
	}
	if loop != nil {
		got, err := loop.InterruptPlayback(p.ResponseID(), played, queued)
		if err != nil && !errors.Is(err, conversation.ErrStaleGeneration) {
			return out, err
		}
		out.Spoken = got.Spoken
	}
	return out, clearErr
}
