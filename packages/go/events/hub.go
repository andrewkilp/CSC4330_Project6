// Package events fans durable call events out to live subscribers (#23). It is the
// in-process half of the SSE feed: the repository (#13) is the durable source of
// truth and replay, and Publish is called only after the events' transaction commits.
// One API instance; no multi-instance broker.
package events

import (
	"sync"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

// DefaultBuffer bounds each subscriber's queue. A subscriber that falls this far
// behind is disconnected (Lagged) and recovers by replaying from the repository.
const DefaultBuffer = 256

// Hub is safe for concurrent use.
type Hub struct {
	buffer int

	mu   sync.Mutex
	subs map[string]map[*Subscription]struct{}
}

// NewHub returns a hub with the given per-subscriber buffer (DefaultBuffer if <= 0).
func NewHub(buffer int) *Hub {
	if buffer <= 0 {
		buffer = DefaultBuffer
	}
	return &Hub{buffer: buffer, subs: map[string]map[*Subscription]struct{}{}}
}

// Subscription receives live events for one call until Close or until it lags.
type Subscription struct {
	C <-chan repository.Event

	hub    *Hub
	callID string
	ch     chan repository.Event
	lagged bool // guarded by hub.mu
	closed bool // guarded by hub.mu
}

// Subscribe registers before the caller replays from the repository, so no event
// committed after the replay read can be missed; duplicates are filtered by seq.
func (h *Hub) Subscribe(callID string) *Subscription {
	s := &Subscription{hub: h, callID: callID, ch: make(chan repository.Event, h.buffer)}
	s.C = s.ch
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs[callID] == nil {
		h.subs[callID] = map[*Subscription]struct{}{}
	}
	h.subs[callID][s] = struct{}{}
	return s
}

// Close unregisters the subscription and closes C. Idempotent.
func (s *Subscription) Close() {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	s.hub.removeLocked(s)
}

// Lagged reports whether the hub dropped this subscriber for falling behind.
func (s *Subscription) Lagged() bool {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	return s.lagged
}

func (h *Hub) removeLocked(s *Subscription) {
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
	if set := h.subs[s.callID]; set != nil {
		delete(set, s)
		if len(set) == 0 {
			delete(h.subs, s.callID)
		}
	}
}

// Publish delivers committed events to current subscribers without blocking. A full
// subscriber is marked lagged and closed rather than slowing other calls.
func (h *Hub) Publish(events ...repository.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range events {
		for s := range h.subs[e.CallID] {
			select {
			case s.ch <- e:
			default:
				s.lagged = true
				h.removeLocked(s)
			}
		}
	}
}

// Subscribers reports how many live subscriptions a call has.
func (h *Hub) Subscribers(callID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[callID])
}
