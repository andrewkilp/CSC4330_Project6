package policy_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/policy"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/policy/policytest"
)

const callA = "0b6c4f7e-3f1a-4c2b-9d3e-1a2b3c4d5e6f"
const callB = "1c7d5a8f-4a2b-4d3c-8e4f-2b3c4d5e6f70"

var start = time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)

// Rates from docs/provider-matrix.md, checked November 10, 2026.
var matrixRates = policy.Rates{
	CheckedOn: "2026-11-10", TelephonyPerMinute: 0.014, MediaPerMinute: 0.0044, STTPerMinute: 0.0077,
	LLMInputPerMTok: 2, LLMOutputPerMTok: 10, TTSPer1KChars: 0.04,
}

// wait fails the test instead of hanging when a mutation breaks a timer.
func wait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a policy callback")
		var zero T
		return zero
	}
}

func newPolicy(t *testing.T, cfg policy.Config) (*policy.Policy, *policytest.Clock) {
	t.Helper()
	clock := policytest.New(start)
	p, err := policy.New(cfg, clock)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, clock
}

type endRecorder struct {
	mu    sync.Mutex
	calls int
	at    []time.Time
	err   error
	clock *policytest.Clock
}

func (e *endRecorder) End(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	e.calls++
	if e.clock != nil {
		e.at = append(e.at, e.clock.Now())
	}
	return e.err
}

func (e *endRecorder) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func TestDefaultsMatchContract(t *testing.T) {
	p, _ := newPolicy(t, policy.Config{})
	c := p.Config()
	if c.MaxCallDuration != 180*time.Second || c.EndMargin != 5*time.Second || c.MaxConcurrentRealCalls != 1 ||
		c.UsageInterval != 5*time.Second || c.Timeouts != policy.DefaultTimeouts() {
		t.Fatalf("defaults = %+v", c)
	}
	if p.ProviderTimeLimit() != 180 {
		t.Fatalf("provider time limit = %d", p.ProviderTimeLimit())
	}
}

func TestTimeoutHangupEndsBeforeLimitAndReleasesPermit(t *testing.T) {
	p, clock := newPolicy(t, policy.Config{})
	end := &endRecorder{clock: clock}
	var limitErrs []error
	perm, err := p.Acquire(callA, policy.Hooks{End: end.End, LimitReached: func(err error) { limitErrs = append(limitErrs, err) }})
	if err != nil {
		t.Fatal(err)
	}
	if want := start.Add(175 * time.Second); !perm.Deadline().Equal(want) {
		t.Fatalf("deadline = %v, want %v", perm.Deadline(), want)
	}
	clock.Advance(8 * time.Second) // ringing counts toward the limit
	if err := perm.Connected(); err != nil {
		t.Fatal(err)
	}
	clock.Advance(167*time.Second - time.Millisecond)
	if end.count() != 0 || perm.Limited() {
		t.Fatal("hung up before the deadline")
	}
	clock.Advance(time.Millisecond)
	if end.count() != 1 || !perm.Limited() || len(limitErrs) != 1 || limitErrs[0] != nil {
		t.Fatalf("end calls = %d, limited = %v, limit errors = %v", end.count(), perm.Limited(), limitErrs)
	}
	if got := end.at[0].Sub(start); got >= 180*time.Second {
		t.Fatalf("hang-up started at %v, not before the 180 s limit", got)
	}
	if _, held := p.Active(); held {
		t.Fatal("permit still held after a successful limit hang-up")
	}
	if u := perm.Usage(); u.ElapsedSeconds != 167 || !u.EstimateOnly {
		t.Fatalf("final usage = %+v", u)
	}
	clock.Advance(time.Hour)
	if end.count() != 1 || clock.Pending() != 0 {
		t.Fatalf("end calls = %d, pending timers = %d after release", end.count(), clock.Pending())
	}
	if _, err := p.Acquire(callB, policy.Hooks{End: end.End}); err != nil {
		t.Fatalf("next call rejected after release: %v", err)
	}
}

func TestShortConfiguredLimitUsesProportionalMargin(t *testing.T) {
	p, clock := newPolicy(t, policy.Config{MaxCallDuration: 10 * time.Second})
	end := &endRecorder{}
	if _, err := p.Acquire(callA, policy.Hooks{End: end.End}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(9*time.Second - time.Millisecond)
	if end.count() != 0 {
		t.Fatal("ended early")
	}
	clock.Advance(time.Millisecond)
	if end.count() != 1 {
		t.Fatal("did not end one second before a 10 s limit")
	}
}

func TestFailedLimitHangupKeepsPermitUntilReleased(t *testing.T) {
	p, clock := newPolicy(t, policy.Config{})
	end := &endRecorder{err: errors.New("provider unavailable")}
	var got error
	perm, err := p.Acquire(callA, policy.Hooks{End: end.End, LimitReached: func(err error) { got = err }})
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(175 * time.Second)
	if got == nil || end.count() != 1 {
		t.Fatalf("limit error = %v, end calls = %d", got, end.count())
	}
	if id, held := p.Active(); !held || id != callA {
		t.Fatal("permit released while the call may still be live")
	}
	if _, err := p.Acquire(callB, policy.Hooks{End: end.End}); !errors.Is(err, policy.ErrConcurrentCall) {
		t.Fatalf("second start = %v, want ErrConcurrentCall", err)
	}
	perm.Release()
	if _, err := p.Acquire(callB, policy.Hooks{End: end.End}); err != nil {
		t.Fatalf("start after release: %v", err)
	}
}

func TestLimitHangupTimeoutIsReported(t *testing.T) {
	p, clock := newPolicy(t, policy.Config{Timeouts: policy.Timeouts{Telephony: 2 * time.Second}})
	var got error
	hung := make(chan struct{})
	_, err := p.Acquire(callA, policy.Hooks{
		End: func(ctx context.Context) error {
			close(hung)
			<-ctx.Done()
			return ctx.Err()
		},
		LimitReached: func(err error) { got = err },
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { clock.Advance(175 * time.Second); close(done) }()
	wait(t, hung)
	// The deadline callback is blocked inside End; time cannot move from this goroutine
	// until it returns, so cancel it by advancing from a third goroutine.
	go clock.Advance(2 * time.Second)
	wait(t, done)
	if !errors.Is(got, policy.ErrProviderTimeout) {
		t.Fatalf("limit error = %v, want ErrProviderTimeout", got)
	}
	if _, held := p.Active(); !held {
		t.Fatal("permit released after an unconfirmed hang-up")
	}
}

func TestConcurrentStartRejected(t *testing.T) {
	p, _ := newPolicy(t, policy.Config{})
	end := &endRecorder{}
	first, err := p.Acquire(callA, policy.Hooks{End: end.End})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Acquire(callB, policy.Hooks{End: end.End}); !errors.Is(err, policy.ErrConcurrentCall) {
		t.Fatalf("second start = %v, want ErrConcurrentCall", err)
	}
	if _, err := p.StartReal(context.Background(), callB, policy.Hooks{End: end.End}, func(context.Context) error {
		t.Fatal("dialed while another real call was active")
		return nil
	}); !errors.Is(err, policy.ErrConcurrentCall) {
		t.Fatalf("StartReal = %v, want ErrConcurrentCall", err)
	}
	first.Release()
	first.Release() // idempotent

	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Acquire(callB, policy.Hooks{End: end.End}); err == nil {
				wins.Add(1)
			} else if !errors.Is(err, policy.ErrConcurrentCall) {
				t.Errorf("unexpected error %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d concurrent starts acquired the single permit", wins.Load())
	}
}

func TestStartFailureReleasesPermit(t *testing.T) {
	p, _ := newPolicy(t, policy.Config{})
	end := &endRecorder{}
	dialErr := errors.New("dial rejected")
	if _, err := p.StartReal(context.Background(), callA, policy.Hooks{End: end.End}, func(context.Context) error { return dialErr }); !errors.Is(err, dialErr) {
		t.Fatalf("StartReal = %v", err)
	}
	if _, held := p.Active(); held {
		t.Fatal("permit held after failed start")
	}
	if end.count() != 1 {
		t.Fatalf("best-effort hang-up calls = %d", end.count())
	}
	perm, err := p.StartReal(context.Background(), callB, policy.Hooks{End: end.End}, func(context.Context) error { return nil })
	if err != nil || perm.CallID() != callB {
		t.Fatalf("start after failure: %v", err)
	}
}

func TestStartTimeoutReleasesPermit(t *testing.T) {
	p, clock := newPolicy(t, policy.Config{})
	end := &endRecorder{}
	entered := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := p.StartReal(context.Background(), callA, policy.Hooks{End: end.End}, func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			return errors.New("vendor said: socket closed") // provider detail must not escape
		})
		result <- err
	}()
	wait(t, entered)
	clock.Advance(10*time.Second - time.Millisecond)
	select {
	case err := <-result:
		t.Fatalf("start returned before its timeout: %v", err)
	default:
	}
	clock.Advance(time.Millisecond)
	err := wait(t, result)
	if !errors.Is(err, policy.ErrProviderTimeout) || strings.Contains(err.Error(), "vendor") {
		t.Fatalf("StartReal = %v, want ErrProviderTimeout without provider detail", err)
	}
	if _, held := p.Active(); held {
		t.Fatal("permit held after timed-out start")
	}
	if end.count() != 1 {
		t.Fatalf("best-effort hang-up calls = %d", end.count())
	}
}

func TestRunProviderTimeouts(t *testing.T) {
	p, clock := newPolicy(t, policy.Config{Timeouts: policy.Timeouts{LLM: 3 * time.Second}})
	if err := p.Run(context.Background(), policy.LLM, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	other := errors.New("rate limited")
	if err := p.Run(context.Background(), policy.LLM, func(context.Context) error { return other }); !errors.Is(err, other) {
		t.Fatalf("pre-timeout error rewritten: %v", err)
	}
	if err := p.Run(context.Background(), policy.Provider("fax"), func(context.Context) error { return nil }); !errors.Is(err, policy.ErrInvalid) {
		t.Fatalf("unknown provider = %v", err)
	}
	result := make(chan error, 1)
	entered := make(chan struct{})
	go func() {
		result <- p.Run(context.Background(), policy.LLM, func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	wait(t, entered)
	clock.Advance(3 * time.Second)
	if err := wait(t, result); !errors.Is(err, policy.ErrProviderTimeout) || !strings.Contains(err.Error(), "llm") {
		t.Fatalf("Run = %v", err)
	}
	// Caller cancellation is not reported as a provider timeout.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Run(ctx, policy.TTS, func(ctx context.Context) error { return ctx.Err() }); !errors.Is(err, context.Canceled) || errors.Is(err, policy.ErrProviderTimeout) {
		t.Fatalf("cancelled Run = %v", err)
	}
	if clock.Pending() != 0 {
		t.Fatalf("%d timeout timers leaked", clock.Pending())
	}
}

func TestUsageUpdatesAreMonotonicEstimatesAndStopAtRelease(t *testing.T) {
	p, clock := newPolicy(t, policy.Config{Rates: matrixRates})
	end := &endRecorder{}
	var updates []policy.Usage
	perm, err := p.Acquire(callA, policy.Hooks{End: end.End, Usage: func(u policy.Usage) { updates = append(updates, u) }})
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(4 * time.Second) // ringing is neither elapsed time nor billed minutes
	if len(updates) != 0 {
		t.Fatal("usage published before the call connected")
	}
	if err := perm.Connected(); err != nil {
		t.Fatal(err)
	}
	if err := perm.Connected(); err != nil {
		t.Fatal(err)
	}
	if err := perm.RecordLLM(2500, 150); err != nil {
		t.Fatal(err)
	}
	if err := perm.RecordTTS(80); err != nil {
		t.Fatal(err)
	}
	clock.Advance(12 * time.Second)
	if len(updates) != 3 {
		t.Fatalf("updates = %+v", updates)
	}
	for i, u := range updates {
		if !u.EstimateOnly || u.EstimatedCostUSD < 0 || (i > 0 && (u.ElapsedSeconds < updates[i-1].ElapsedSeconds ||
			u.EstimatedCostUSD < updates[i-1].EstimatedCostUSD)) {
			t.Fatalf("update %d = %+v", i, u)
		}
	}
	if updates[0].ElapsedSeconds != 0 || updates[2].ElapsedSeconds != 10 {
		t.Fatalf("elapsed = %d, %d", updates[0].ElapsedSeconds, updates[2].ElapsedSeconds)
	}
	// 10 s connected: one started minute of voice+media, 10/60 min of STT, plus metered usage.
	want := policy.Estimate(matrixRates, policy.Meter{LLMInputTokens: 2500, LLMOutputTokens: 150, TTSCharacters: 80}, 10*time.Second)
	if updates[2].EstimatedCostUSD != want || want != 0.0294 {
		t.Fatalf("estimate = %v, want %v (0.0294)", updates[2].EstimatedCostUSD, want)
	}
	final := perm.Release()
	if final.ElapsedSeconds != 12 {
		t.Fatalf("final = %+v", final)
	}
	n := len(updates)
	clock.Advance(time.Minute)
	if len(updates) != n || clock.Pending() != 0 {
		t.Fatalf("usage continued after release: %d -> %d updates, %d timers", n, len(updates), clock.Pending())
	}
	if perm.Usage() != final {
		t.Fatal("usage changed after release")
	}
	if err := perm.RecordLLM(1, 1); !errors.Is(err, policy.ErrReleased) {
		t.Fatalf("RecordLLM after release = %v", err)
	}
	if err := perm.RecordTTS(1); !errors.Is(err, policy.ErrReleased) {
		t.Fatalf("RecordTTS after release = %v", err)
	}
	if err := perm.Connected(); !errors.Is(err, policy.ErrReleased) {
		t.Fatalf("Connected after release = %v", err)
	}
	if err := perm.RecordTTS(-1); !errors.Is(err, policy.ErrInvalid) {
		t.Fatalf("negative count = %v", err)
	}
}

func TestUsageHookMayReleaseWithoutDeadlock(t *testing.T) {
	p, clock := newPolicy(t, policy.Config{})
	end := &endRecorder{}
	var perm *policy.Permit
	calls := 0
	perm, err := p.Acquire(callA, policy.Hooks{End: end.End, Usage: func(policy.Usage) {
		calls++
		if calls == 2 {
			perm.Release()
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := perm.Connected(); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	if calls != 2 || clock.Pending() != 0 {
		t.Fatalf("calls = %d, pending = %d", calls, clock.Pending())
	}
}

func TestAcquireValidatesInput(t *testing.T) {
	p, _ := newPolicy(t, policy.Config{})
	if _, err := p.Acquire("", policy.Hooks{End: (&endRecorder{}).End}); !errors.Is(err, policy.ErrInvalid) {
		t.Fatalf("empty id = %v", err)
	}
	if _, err := p.Acquire(callA, policy.Hooks{}); !errors.Is(err, policy.ErrInvalid) {
		t.Fatalf("missing End = %v", err)
	}
	if _, err := p.StartReal(context.Background(), callA, policy.Hooks{End: (&endRecorder{}).End}, nil); !errors.Is(err, policy.ErrInvalid) {
		t.Fatalf("missing start = %v", err)
	}
	if _, held := p.Active(); held {
		t.Fatal("invalid request acquired the permit")
	}
}
