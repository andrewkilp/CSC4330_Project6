package telephony

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

// faultStore is an in-memory ReconcileStore: dialing calls and recorded failures.
type faultStore struct {
	mu       sync.Mutex
	dialing  map[string]repository.StaleCall
	failures []string
	failErr  error
}

func (s *faultStore) StaleDialing(context.Context, time.Duration, int) ([]repository.StaleCall, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []repository.StaleCall
	for _, c := range s.dialing {
		out = append(out, c)
	}
	return out, nil
}

func (s *faultStore) RecordProviderFailure(_ context.Context, callID string, f repository.ProviderFailure) ([]repository.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failErr != nil {
		return nil, s.failErr
	}
	if f.Provider != "telephony" || f.Code != "dial_timeout" || !f.FailCall {
		return nil, repository.ErrInvalid
	}
	s.failures = append(s.failures, callID)
	delete(s.dialing, callID)
	return []repository.Event{{CallID: callID, Seq: 1, Type: "provider.error"}, {CallID: callID, Seq: 2, Type: "call.updated"}}, nil
}

func TestReconcilerFailsOrphanDialingOnceWithoutRedialing(t *testing.T) {
	inFlight, restarted, unanswered := repository.NewID(), repository.NewID(), repository.NewID()
	store := &faultStore{dialing: map[string]repository.StaleCall{
		inFlight:   {CallID: inFlight, Mode: "mock"},                        // start still waiting on the provider
		restarted:  {CallID: restarted, Mode: "real", ProviderSID: "CAold"}, // placed before a restart
		unanswered: {CallID: unanswered, Mode: "mock"},                      // provider never acknowledged
	}}
	calls := NewCalls()
	live := newGatedProvider()
	real := newGatedProvider()
	var published []repository.Event
	r := &Reconciler{Store: store, Calls: calls, Providers: map[string]providers.Telephony{"real": real},
		Publish: func(e ...repository.Event) { published = append(published, e...) }}

	n, err := r.Sweep(context.Background())
	if err != nil || n != 3 || len(store.failures) != 3 || len(published) != 6 {
		t.Fatalf("sweep: n=%d err=%v failures=%v published=%d", n, err, store.failures, len(published))
	}
	if real.ends.Load() != 1 {
		t.Fatalf("restarted orphan hung up %d times", real.ends.Load())
	}
	// The in-flight start answers late: it is hung up at once, never left running.
	if err := calls.Started(inFlight, live, "CAlate"); !errors.Is(err, ErrCallEnded) || live.ends.Load() != 1 {
		t.Fatalf("late start: %v, ends=%d", err, live.ends.Load())
	}
	// A second sweep finds nothing; nothing is redialed or failed twice.
	if n, err := r.Sweep(context.Background()); n != 0 || err != nil || real.ends.Load() != 1 {
		t.Fatalf("second sweep: n=%d err=%v ends=%d", n, err, real.ends.Load())
	}
}

func TestReconcilerHangsUpTrackedCallsThroughTheRegistryOnly(t *testing.T) {
	callID := repository.NewID()
	store := &faultStore{dialing: map[string]repository.StaleCall{callID: {CallID: callID, Mode: "real", ProviderSID: "CA1"}}}
	calls := NewCalls()
	provider := newGatedProvider()
	_ = calls.Started(callID, provider, "CA1")
	r := &Reconciler{Store: store, Calls: calls, Providers: map[string]providers.Telephony{"real": provider}}
	if _, err := r.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := provider.ends.Load(); n != 1 {
		t.Fatalf("hung up %d times, want exactly 1", n)
	}
}

func TestReconcilerReportsStoreFailuresAndKeepsGoing(t *testing.T) {
	a, b := repository.NewID(), repository.NewID()
	store := &faultStore{dialing: map[string]repository.StaleCall{a: {CallID: a, Mode: "mock"}, b: {CallID: b, Mode: "mock"}}, failErr: repository.ErrUnavailable}
	r := &Reconciler{Store: store, Calls: NewCalls()}
	if n, err := r.Sweep(context.Background()); n != 0 || !errors.Is(err, repository.ErrUnavailable) {
		t.Fatalf("sweep = %d, %v", n, err)
	}
	// The calls stay dialing and are retried next sweep; local work is already ended.
	store.failErr = nil
	if n, err := r.Sweep(context.Background()); n != 2 || err != nil {
		t.Fatalf("retry sweep = %d, %v", n, err)
	}
	if _, err := (&Reconciler{}).Sweep(context.Background()); !errors.Is(err, providers.ErrInvalid) {
		t.Fatal("reconciler without a store ran")
	}
}

func TestReconcilerRunStopsWithContext(t *testing.T) {
	store := &faultStore{dialing: map[string]repository.StaleCall{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { (&Reconciler{Store: store}).Run(ctx, time.Millisecond); close(done) }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
}
