package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

// durableStore adds the event and update surfaces of *repository.Repository.
type durableStore struct{ mediaStore }

func (durableStore) Update(context.Context, string, string, string, string, any, func(*repository.Tx) (any, error)) (json.RawMessage, error) {
	return nil, repository.ErrUnavailable
}
func (durableStore) EventsAfter(context.Context, string, string, int64, int) ([]repository.Event, error) {
	return nil, nil
}
func (durableStore) Snapshot(context.Context, string, string) (repository.Snapshot, error) {
	return repository.Snapshot{}, repository.ErrNotFound
}

func status(t *testing.T, store any, method, path string) int {
	t.Helper()
	var h = newRouter("development")
	if store != nil {
		var err error
		h, err = configuredRouter("development", store.(durableStore), "")
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, strings.NewReader(`{"action":"end","idempotency_key":"11111111-1111-4111-8111-111111111111"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

func TestControlAndEventRoutesNeedDurableStorage(t *testing.T) {
	t.Setenv("ENABLE_REAL_CALLS", "false")
	t.Setenv("TWILIO_WEBHOOKS_ENABLED", "")
	control := "/v1/calls/" + mediaCallID + "/control"
	events := "/v1/calls/" + mediaCallID + "/events"
	if got := status(t, nil, "POST", control); got != 404 {
		t.Fatalf("control without storage = %d, want 404", got)
	}
	// #22 keeps event routes mounted: authenticate first, then return 503
	// for unavailable storage instead of hiding the endpoint.
	if got := status(t, nil, "GET", events); got != 401 {
		t.Fatalf("events without a session = %d, want 401", got)
	}
	// Mounted and owner-gated with storage.
	if got := status(t, durableStore{}, "POST", control); got != 401 {
		t.Fatalf("control without a session = %d, want 401", got)
	}
	if got := status(t, durableStore{}, "GET", events); got != 401 {
		t.Fatalf("events without a session = %d, want 401", got)
	}
}

func TestCommittedTerminalStatusStopsLiveWork(t *testing.T) {
	rt := newLiveRuntime()
	stream := &closeCounter{}
	if err := rt.calls.AttachMedia(mediaCallID, stream); err != nil {
		t.Fatal(err)
	}
	sub := rt.hub.Subscribe(mediaCallID)
	defer sub.Close()
	connected, _ := json.Marshal(map[string]string{"status": "connected"})
	rt.publish(repository.Event{CallID: mediaCallID, Seq: 1, Type: "call.updated", Payload: connected})
	if rt.calls.Ended(mediaCallID) {
		t.Fatal("a non-terminal update ended live work")
	}
	ended, _ := json.Marshal(map[string]string{"status": "ended"})
	rt.publish(repository.Event{CallID: mediaCallID, Seq: 2, Type: "call.updated", Payload: ended})
	for i := range 2 {
		select {
		case e := <-sub.C:
			if e.Seq != int64(i+1) {
				t.Fatalf("subscriber got seq %d", e.Seq)
			}
		case <-time.After(time.Second):
			t.Fatal("event not fanned out")
		}
	}
	waitUntil(t, "live work ended", func() bool { return rt.calls.Ended(mediaCallID) })
	if stream.closed.Load() != 1 {
		t.Fatalf("stream closed %d times", stream.closed.Load())
	}
}
