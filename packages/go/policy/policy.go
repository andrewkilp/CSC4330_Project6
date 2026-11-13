// Package policy enforces the server's real-call limits from docs/contracts.md: a
// maximum call duration, one concurrent real-call permit, per-provider timeouts and an
// estimate-only usage report for usage.updated. It contains no provider SDKs, network
// code or credentials and never places a call by itself. The call lifecycle (#11, #31)
// composes it: Acquire (or StartReal) on the explicit start action, Connected when the
// call is answered, Record* as usage is reported, and Release on any terminal state.
package policy

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrConcurrentCall rejects a real-call start while another real call holds the
	// permit. Handlers map it to 409 conflict.
	ErrConcurrentCall = errors.New("another real call is already active")
	// ErrProviderTimeout reports that a provider operation exceeded its configured
	// timeout. Handlers map it to 503 provider_unavailable.
	ErrProviderTimeout = errors.New("provider timed out")
	// ErrReleased reports use of a permit after Release.
	ErrReleased = errors.New("call permit released")
	// ErrInvalid reports a malformed request to this package.
	ErrInvalid = errors.New("invalid call policy request")
)

// Hooks connect a permit to the call lifecycle. Hooks run synchronously from timer
// callbacks; they must return promptly and must not call Connected. They may call
// Release, Usage and Record*.
type Hooks struct {
	// End hangs up the real call. Required. It runs with the telephony timeout when
	// the duration limit is reached, and best-effort when StartReal fails. It must be
	// idempotent and tolerate a call the provider never created.
	End func(ctx context.Context) error
	// Usage publishes usage.updated. Optional. Calls are serialized and elapsed time
	// never goes backward. One already in flight may finish after Release returns.
	Usage func(Usage)
	// LimitReached reports the result of the limit hang-up. Optional. On success the
	// permit has already been released; on failure the permit stays held because the
	// call may still be live, and the lifecycle must end it and call Release.
	LimitReached func(endErr error)
}

// Policy is safe for concurrent use. One Policy serves the whole API instance.
type Policy struct {
	cfg   Config
	clock Clock

	mu     sync.Mutex
	active *Permit
}

// New applies defaults and validates cfg. A nil clock is the system clock.
func New(cfg Config, clock Clock) (*Policy, error) {
	cfg = cfg.withDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if clock == nil {
		clock = SystemClock{}
	}
	return &Policy{cfg: cfg, clock: clock}, nil
}

// Config returns the effective configuration, with defaults applied.
func (p *Policy) Config() Config { return p.cfg }

// ProviderTimeLimit is the duration to request from the telephony provider as an
// independent backstop (for Twilio, the call's TimeLimit), in whole seconds.
func (p *Policy) ProviderTimeLimit() int { return int(p.cfg.MaxCallDuration / time.Second) }

// Active reports the call id holding the real-call permit, if any.
func (p *Policy) Active() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active == nil {
		return "", false
	}
	return p.active.callID, true
}

func (p *Policy) timeout(provider Provider) (time.Duration, bool) {
	switch provider {
	case Telephony:
		return p.cfg.Timeouts.Telephony, true
	case STT:
		return p.cfg.Timeouts.STT, true
	case LLM:
		return p.cfg.Timeouts.LLM, true
	case TTS:
		return p.cfg.Timeouts.TTS, true
	}
	return 0, false
}

// Run calls fn with a context cancelled after the provider's configured timeout. If fn
// fails after the timeout fired, the error wraps ErrProviderTimeout instead of the
// provider's own error. A nil error from fn is returned as nil.
func (p *Policy) Run(ctx context.Context, provider Provider, fn func(context.Context) error) error {
	d, ok := p.timeout(provider)
	if !ok || fn == nil || ctx == nil {
		return fmt.Errorf("%w: provider operation", ErrInvalid)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	timer := p.clock.AfterFunc(d, func() { cancel(ErrProviderTimeout) })
	defer func() {
		timer.Stop()
		cancel(nil)
	}()
	err := fn(ctx)
	if err != nil && errors.Is(context.Cause(ctx), ErrProviderTimeout) {
		return fmt.Errorf("%w: %s", ErrProviderTimeout, provider)
	}
	return err
}

// Acquire takes the single real-call permit for callID and starts the duration
// deadline. It does not dial. It fails with ErrConcurrentCall while another permit is
// held.
func (p *Policy) Acquire(callID string, hooks Hooks) (*Permit, error) {
	if callID == "" || len(callID) > 100 || hooks.End == nil {
		return nil, fmt.Errorf("%w: permit", ErrInvalid)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active != nil {
		return nil, ErrConcurrentCall
	}
	perm := &Permit{policy: p, callID: callID, hooks: hooks, acquiredAt: p.clock.Now()}
	perm.mu.Lock()
	perm.deadline = p.clock.AfterFunc(p.cfg.MaxCallDuration-p.cfg.EndMargin, perm.limit)
	perm.mu.Unlock()
	p.active = perm
	return perm, nil
}

// StartReal acquires the permit and runs start (the telephony dial) under the
// telephony timeout. If start fails, End is attempted best-effort, because a timed-out
// request may still have created a call, and the permit is released before returning.
func (p *Policy) StartReal(ctx context.Context, callID string, hooks Hooks, start func(context.Context) error) (*Permit, error) {
	if start == nil {
		return nil, fmt.Errorf("%w: start", ErrInvalid)
	}
	perm, err := p.Acquire(callID, hooks)
	if err != nil {
		return nil, err
	}
	if err := p.Run(ctx, Telephony, start); err != nil {
		_ = p.Run(context.WithoutCancel(ctx), Telephony, hooks.End)
		perm.Release()
		return nil, err
	}
	return perm, nil
}

// Permit is one real call's hold on the concurrency slot plus its duration deadline
// and usage meter.
type Permit struct {
	policy *Policy
	callID string
	hooks  Hooks

	emitMu sync.Mutex // serializes Usage hook calls

	mu          sync.Mutex
	acquiredAt  time.Time
	connectedAt time.Time
	connected   bool
	limited     bool
	released    bool
	meter       Meter
	deadline    Timer
	ticker      Timer
	final       Usage
}

func (perm *Permit) CallID() string { return perm.callID }

// Deadline is when the limit hang-up begins: MaxCallDuration minus EndMargin after
// Acquire.
func (perm *Permit) Deadline() time.Time {
	c := perm.policy.cfg
	return perm.acquiredAt.Add(c.MaxCallDuration - c.EndMargin)
}

// Limited reports whether the duration limit has fired.
func (perm *Permit) Limited() bool {
	perm.mu.Lock()
	defer perm.mu.Unlock()
	return perm.limited
}

// Connected marks the call answered. Elapsed time and per-minute charges start here;
// the duration deadline does not move. Usage is published now and every
// UsageInterval until Release. Calling it again is a no-op.
func (perm *Permit) Connected() error {
	perm.mu.Lock()
	if perm.released {
		perm.mu.Unlock()
		return ErrReleased
	}
	if perm.connected {
		perm.mu.Unlock()
		return nil
	}
	perm.connected = true
	perm.connectedAt = perm.policy.clock.Now()
	perm.mu.Unlock()
	perm.publish()
	perm.mu.Lock()
	if !perm.released {
		perm.ticker = perm.policy.clock.AfterFunc(perm.policy.cfg.UsageInterval, perm.tick)
	}
	perm.mu.Unlock()
	return nil
}

// RecordLLM adds tokens reported by the model provider for one generation, including
// cancelled or regenerated suggestions, which are still billed.
func (perm *Permit) RecordLLM(inputTokens, outputTokens int64) error {
	if inputTokens < 0 || outputTokens < 0 {
		return fmt.Errorf("%w: token count", ErrInvalid)
	}
	perm.mu.Lock()
	defer perm.mu.Unlock()
	if perm.released {
		return ErrReleased
	}
	perm.meter.LLMInputTokens += inputTokens
	perm.meter.LLMOutputTokens += outputTokens
	return nil
}

// RecordTTS adds characters actually sent for synthesis.
func (perm *Permit) RecordTTS(characters int64) error {
	if characters < 0 {
		return fmt.Errorf("%w: character count", ErrInvalid)
	}
	perm.mu.Lock()
	defer perm.mu.Unlock()
	if perm.released {
		return ErrReleased
	}
	perm.meter.TTSCharacters += characters
	return nil
}

// Usage returns the current estimate, or the final one after Release.
func (perm *Permit) Usage() Usage {
	perm.mu.Lock()
	defer perm.mu.Unlock()
	if perm.released {
		return perm.final
	}
	return perm.usageLocked()
}

// Release frees the permit, stops its timers and freezes usage. It is idempotent and
// returns the final usage for the terminal usage.updated event.
func (perm *Permit) Release() Usage {
	perm.mu.Lock()
	if !perm.released {
		perm.final = perm.usageLocked()
		perm.released = true
		perm.deadline.Stop()
		if perm.ticker != nil {
			perm.ticker.Stop()
		}
	}
	final := perm.final
	perm.mu.Unlock()
	perm.policy.mu.Lock()
	if perm.policy.active == perm {
		perm.policy.active = nil
	}
	perm.policy.mu.Unlock()
	return final
}

func (perm *Permit) usageLocked() Usage {
	var elapsed time.Duration
	if perm.connected {
		elapsed = perm.policy.clock.Now().Sub(perm.connectedAt)
	}
	return Usage{
		ElapsedSeconds:   int64(max(elapsed, 0) / time.Second),
		EstimatedCostUSD: Estimate(perm.policy.cfg.Rates, perm.meter, elapsed),
		EstimateOnly:     true,
	}
}

func (perm *Permit) publish() {
	if perm.hooks.Usage == nil {
		return
	}
	perm.emitMu.Lock()
	defer perm.emitMu.Unlock()
	perm.mu.Lock()
	if perm.released {
		perm.mu.Unlock()
		return
	}
	u := perm.usageLocked()
	perm.mu.Unlock()
	perm.hooks.Usage(u)
}

func (perm *Permit) tick() {
	perm.publish()
	perm.mu.Lock()
	defer perm.mu.Unlock()
	if !perm.released {
		perm.ticker = perm.policy.clock.AfterFunc(perm.policy.cfg.UsageInterval, perm.tick)
	}
}

func (perm *Permit) limit() {
	perm.mu.Lock()
	if perm.released || perm.limited {
		perm.mu.Unlock()
		return
	}
	perm.limited = true
	perm.mu.Unlock()
	err := perm.policy.Run(context.Background(), Telephony, perm.hooks.End)
	if err == nil {
		perm.Release()
	}
	if perm.hooks.LimitReached != nil {
		perm.hooks.LimitReached(err)
	}
}
