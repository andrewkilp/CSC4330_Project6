package telephony

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

// ReconcileStore is the subset of *repository.Repository the reconciler needs.
type ReconcileStore interface {
	StaleDialing(ctx context.Context, olderThan time.Duration, limit int) ([]repository.StaleCall, error)
	RecordProviderFailure(ctx context.Context, callID string, f repository.ProviderFailure) ([]repository.Event, error)
}

// Reconciler cleans up orphan dialing (#36): a call that has been "dialing" longer
// than MaxDialing (the provider never answered, never called back, or the start was
// ambiguous) is hung up if a provider call exists, then failed with a visible,
// user-safe provider.error (dial_timeout). It never redials. Cleanup is bounded: each
// sweep handles at most Limit calls, and every hang-up has its own timeout.
type Reconciler struct {
	Store ReconcileStore
	// Calls hangs up calls this process placed and tombstones them, so a start that
	// is still in flight is hung up the moment the provider answers.
	Calls *Calls
	// Providers hangs up calls placed before a restart, by mode ("real", "mock").
	Providers map[string]providers.Telephony
	// Publish fans committed events out (events.Hub.Publish).
	Publish func(...repository.Event)
	// MaxDialing defaults to 90 s; Limit to 50; HangupTimeout to 5 s.
	MaxDialing    time.Duration
	Limit         int
	HangupTimeout time.Duration
}

var dialTimeout = repository.ProviderFailure{Provider: "telephony", Code: "dial_timeout", Retryable: true, FailCall: true}

// Sweep reconciles stale dialing calls once and returns how many it failed.
func (r *Reconciler) Sweep(ctx context.Context) (int, error) {
	if r.Store == nil {
		return 0, providers.ErrInvalid
	}
	maxDialing, limit, timeout := r.MaxDialing, r.Limit, r.HangupTimeout
	if maxDialing <= 0 {
		maxDialing = 90 * time.Second
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	stale, err := r.Store.StaleDialing(ctx, maxDialing, limit)
	if err != nil {
		return 0, err
	}
	failed := 0
	var firstErr error
	for _, s := range stale {
		hangup, cancel := context.WithTimeout(ctx, timeout)
		r.hangUp(hangup, s)
		cancel()
		events, err := r.Store.RecordProviderFailure(ctx, s.CallID, dialTimeout)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		failed++
		if r.Publish != nil && len(events) > 0 {
			r.Publish(events...)
		}
	}
	return failed, firstErr
}

func (r *Reconciler) hangUp(ctx context.Context, s repository.StaleCall) {
	// Calls this process placed (including starts still dialing) are hung up through
	// the live registry, which also tombstones them. Otherwise use the stored SID.
	tracked := false
	if r.Calls != nil {
		tracked = r.Calls.placed(s.CallID)
		if err := r.Calls.End(ctx, s.CallID); err != nil {
			slog.Warn("orphan call hang-up failed", "call_id", s.CallID)
		}
	}
	if tracked || s.ProviderSID == "" {
		return
	}
	if provider := r.Providers[s.Mode]; provider != nil {
		if err := provider.End(ctx, s.ProviderSID); err != nil && !errors.Is(err, providers.ErrNotFound) {
			slog.Warn("orphan call hang-up failed", "call_id", s.CallID)
		}
	}
}

// Run sweeps every interval until ctx ends.
func (r *Reconciler) Run(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 30 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if _, err := r.Sweep(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("dial reconciliation incomplete")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
