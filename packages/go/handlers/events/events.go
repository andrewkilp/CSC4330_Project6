// Package events serves the owner-gated call event stream (#23):
//
//	GET /v1/calls/{id}/events    SSE; Last-Event-ID or ?after_seq= cursor
//	GET /v1/calls/{id}/snapshot  CallSnapshot for reconnect recovery
//
// Events come from the durable repository first and the in-process hub second, so a
// subscriber never misses a committed event and never sees one twice. Each SSE frame
// is `id: <seq>`, `event: <type>`, `data: <event JSON>`; heartbeats are comments.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/auth"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/events"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

// Store is the subset of *repository.Repository (#13) the stream needs.
type Store interface {
	EventsAfter(ctx context.Context, owner, callID string, after int64, limit int) ([]repository.Event, error)
	Snapshot(ctx context.Context, owner, callID string) (repository.Snapshot, error)
}

// page is the repository's maximum EventsAfter page.
const page = 100

type Config struct {
	Store Store
	Hub   *events.Hub
	// RequireOwner authenticates the session, e.g. (*auth.Sessions).RequireOwner.
	RequireOwner func(http.Handler) http.Handler
	// Heartbeat is the comment interval. Zero means 15 s.
	Heartbeat time.Duration
	// ReplayLimit bounds how many missed events one connection replays. A cursor
	// further behind is expired: 409 conflict on after_seq, and the client hydrates a
	// snapshot instead. Zero means 500.
	ReplayLimit int
}

func Register(mux *http.ServeMux, cfg Config) error {
	if cfg.Store == nil || cfg.Hub == nil || cfg.RequireOwner == nil {
		return errors.New("event stream requires a store, hub and session middleware")
	}
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = 15 * time.Second
	}
	if cfg.ReplayLimit <= 0 || cfg.ReplayLimit > 999 {
		cfg.ReplayLimit = 500
	}
	mux.Handle("GET /v1/calls/{id}/events", cfg.RequireOwner(http.HandlerFunc(cfg.stream)))
	mux.Handle("GET /v1/calls/{id}/snapshot", cfg.RequireOwner(http.HandlerFunc(cfg.snapshot)))
	return nil
}

func fail(w http.ResponseWriter, status int, code, message, field string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	body := map[string]string{"code": code, "message": message}
	if field != "" {
		body["field"] = field
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"error": body})
}

func storeError(w http.ResponseWriter, err error) {
	// A malformed id is indistinguishable from a missing or foreign call.
	if errors.Is(err, repository.ErrNotFound) || errors.Is(err, repository.ErrInvalid) {
		fail(w, http.StatusNotFound, "not_found", "Call not found.", "")
		return
	}
	fail(w, http.StatusInternalServerError, "internal", "The request could not be completed.", "")
}

// snapshotBody is CallSnapshot: the repository checkpoint plus the latest summary and
// usage payloads, which live only in the event log.
type snapshotBody struct {
	repository.Snapshot
	Summary json.RawMessage `json:"summary,omitempty"`
	Usage   json.RawMessage `json:"usage,omitempty"`
}

func (cfg Config) snapshot(w http.ResponseWriter, r *http.Request) {
	owner, ok := auth.OwnerID(r.Context())
	if !ok {
		fail(w, http.StatusUnauthorized, "unauthenticated", "A valid session is required.", "")
		return
	}
	id := r.PathValue("id")
	snap, err := cfg.Store.Snapshot(r.Context(), owner, id)
	if err != nil {
		storeError(w, err)
		return
	}
	body := snapshotBody{Snapshot: snap}
	// Contract metadata timestamps are UTC with Z, whatever the database session zone.
	body.Call.CreatedAt, body.Call.UpdatedAt = body.Call.CreatedAt.UTC(), body.Call.UpdatedAt.UTC()
	for i := range body.Instructions {
		body.Instructions[i].CreatedAt = body.Instructions[i].CreatedAt.UTC()
	}
	// Scan only up to the checkpoint so the payloads belong to the same state.
	for after := int64(0); after < snap.LastSeq; {
		events, err := cfg.Store.EventsAfter(r.Context(), owner, id, after, page)
		if err != nil {
			storeError(w, err)
			return
		}
		if len(events) == 0 {
			break
		}
		for _, e := range events {
			if e.Seq > snap.LastSeq {
				break
			}
			switch e.Type {
			case "summary.ready":
				body.Summary = e.Payload
			case "usage.updated":
				body.Usage = e.Payload
			}
		}
		after = events[len(events)-1].Seq
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(body)
}

// replay reads committed events after the cursor, at most limit+1 of them, so the
// caller can tell an expired cursor from a full page.
func (cfg Config) replay(ctx context.Context, owner, callID string, after int64, limit int) ([]repository.Event, error) {
	var out []repository.Event
	for len(out) <= limit {
		events, err := cfg.Store.EventsAfter(ctx, owner, callID, after, page)
		if err != nil {
			return nil, err
		}
		out = append(out, events...)
		if len(events) < page {
			break
		}
		after = events[len(events)-1].Seq
	}
	return out, nil
}

// known reports whether seq is an existing event of the call (0 always is).
func (cfg Config) known(ctx context.Context, owner, callID string, seq int64) (bool, error) {
	if seq == 0 {
		_, err := cfg.Store.EventsAfter(ctx, owner, callID, 0, 1) // owner fence
		return err == nil, err
	}
	events, err := cfg.Store.EventsAfter(ctx, owner, callID, seq-1, 1)
	return err == nil && len(events) == 1 && events[0].Seq == seq, err
}

// cursor reads Last-Event-ID (a reconnect) in preference to ?after_seq= (the first
// connection). Both are decimal sequences; absent means 0.
func cursor(r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if raw == "" {
		raw = r.URL.Query().Get("after_seq")
	}
	if raw == "" {
		return 0, true
	}
	if len(raw) > 16 {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	return n, err == nil && n >= 0
}

func (cfg Config) stream(w http.ResponseWriter, r *http.Request) {
	owner, ok := auth.OwnerID(r.Context())
	if !ok {
		fail(w, http.StatusUnauthorized, "unauthenticated", "A valid session is required.", "")
		return
	}
	after, ok := cursor(r)
	if !ok {
		fail(w, http.StatusBadRequest, "invalid_argument", "Invalid event cursor.", "after_seq")
		return
	}
	callID := r.PathValue("id")
	// Subscribe before reading the replay so nothing committed in between is lost.
	sub := cfg.Hub.Subscribe(callID)
	defer sub.Close()
	exists, err := cfg.known(r.Context(), owner, callID, after)
	if err != nil {
		storeError(w, err)
		return
	}
	var replay []repository.Event
	if exists {
		if replay, err = cfg.replay(r.Context(), owner, callID, after, cfg.ReplayLimit); err != nil {
			storeError(w, err)
			return
		}
	}
	if !exists || len(replay) > cfg.ReplayLimit {
		fail(w, http.StatusConflict, "conflict", "The event cursor has expired. Reload the call.", "after_seq")
		return
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	sent := after
	write := func(e repository.Event) error {
		e.OccurredAt = e.OccurredAt.UTC() // contract timestamps are UTC with Z
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Type, data); err != nil {
			return err
		}
		sent = e.Seq
		return nil
	}
	for _, e := range replay {
		if write(e) != nil {
			return
		}
	}
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil || rc.Flush() != nil {
		return
	}
	heartbeat := time.NewTicker(cfg.Heartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		case e, open := <-sub.C:
			if !open {
				// Lagged or shut down: the client reconnects with Last-Event-ID.
				return
			}
			if e.Seq <= sent {
				continue
			}
			if e.Seq > sent+1 {
				// Publication raced ahead of an earlier commit; fill from durable storage.
				missing, err := cfg.Store.EventsAfter(r.Context(), owner, callID, sent, int(min(e.Seq-sent, page)))
				if err != nil {
					return
				}
				for _, m := range missing {
					if m.Seq <= e.Seq && write(m) != nil {
						return
					}
				}
				if sent < e.Seq-1 {
					return // still missing; let the client reconnect and replay
				}
			}
			if e.Seq == sent+1 && write(e) != nil {
				return
			}
			if rc.Flush() != nil {
				return
			}
		}
	}
}
