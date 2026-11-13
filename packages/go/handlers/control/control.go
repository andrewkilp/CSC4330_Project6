// Package control serves POST /v1/calls/{id}/control (#31): pause, resume and end.
//
// Each action commits one durable state change first, through repository.Update with
// the caller's idempotency key, cancelling any active response in the same
// transaction. Only after the commit does it publish the new events and apply the
// decision to live provider work (Runtime). Runtime effects are idempotent and run on
// every successful request, including an idempotent replay, so a retry after a crash
// between commit and effect still stops audio and hangs up.
//
//	pause   connected -> paused   cancel generation and synthesis, clear queued audio
//	resume  paused -> connected   generate from the newest context (in the background)
//	end     draft|dialing -> cancelled, connected|paused -> ended
//	        cancel everything, close streams, hang up; terminal and published once
//
// Pause on a paused call, resume on a connected call and end on a terminal call
// return the call unchanged with no new event. Pause or resume on any other status is
// 409 conflict on `action`.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/auth"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

// Store commits one control action and returns the resulting call JSON (the
// idempotent result), the call, and any events it appended (none on a replay or
// no-op). It returns ErrAction when the action is not allowed in the call's state.
// Repository adapts *repository.Repository.
type Store interface {
	Control(ctx context.Context, owner, callID, key, action string) (Result, error)
}

type Result struct {
	Raw    json.RawMessage
	Call   repository.Call
	Events []repository.Event
}

// ErrAction reports pause or resume in a state that does not allow it.
var ErrAction = errors.New("control action not allowed in this state")

// Tx is the subset of *repository.Tx that Apply uses.
type Tx interface {
	Call() repository.Call
	Responses() ([]repository.Response, error)
	PutResponse(repository.Response) error
	SetCall(expectedRevision int64, status string, revision int64) error
	AppendEvent(kind string, payload any) (repository.Event, error)
}

// Repository runs Apply in one locked, idempotent repository transaction.
type Repository struct {
	Store interface {
		Update(ctx context.Context, owner, callID, action, key string, request any, fn func(*repository.Tx) (any, error)) (json.RawMessage, error)
	}
}

func (r Repository) Control(ctx context.Context, owner, callID, key, action string) (Result, error) {
	var appended []repository.Event
	conflict := false
	raw, err := r.Store.Update(ctx, owner, callID, "control", key, request{Action: action, IdempotencyKey: key}, func(t *repository.Tx) (any, error) {
		call, events, err := Apply(t, action)
		if errors.Is(err, ErrAction) {
			conflict = true
			return nil, repository.ErrConflict
		}
		appended = events
		return call, err
	})
	if conflict {
		return Result{}, ErrAction
	}
	if err != nil {
		return Result{}, err
	}
	out := Result{Raw: raw, Events: appended}
	if repository.DecodeResult(raw, &out.Call) != nil {
		return Result{}, repository.ErrUnavailable
	}
	return out, nil
}

// Runtime applies a committed control decision to live provider work. Every method
// must be idempotent. telephony.Calls implements it.
type Runtime interface {
	Pause(ctx context.Context, callID string) error
	Resume(callID string)
	End(ctx context.Context, callID string) error
}

type Config struct {
	Store   Store
	Runtime Runtime
	// Publish fans committed events out to live subscribers (events.Hub.Publish).
	Publish func(...repository.Event)
	// RequireOwner authenticates the session, e.g. (*auth.Sessions).RequireOwner.
	RequireOwner func(http.Handler) http.Handler
	// EffectTimeout bounds runtime effects, which are not tied to the request so a
	// disconnecting browser cannot leave audio playing. Zero means 10 s.
	EffectTimeout time.Duration
}

func Register(mux *http.ServeMux, cfg Config) error {
	if cfg.Store == nil || cfg.Runtime == nil || cfg.Publish == nil || cfg.RequireOwner == nil {
		return errors.New("call controls require a store, runtime, publisher and session middleware")
	}
	if cfg.EffectTimeout <= 0 {
		cfg.EffectTimeout = 10 * time.Second
	}
	mux.Handle("POST /v1/calls/{id}/control", cfg.RequireOwner(http.HandlerFunc(cfg.control)))
	return nil
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type request struct {
	Action         string `json:"action"`
	IdempotencyKey string `json:"idempotency_key"`
}

func writeError(w http.ResponseWriter, status int, code, message, field string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	body := map[string]string{"code": code, "message": message}
	if field != "" {
		body["field"] = field
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"error": body})
}

func (cfg Config) control(w http.ResponseWriter, r *http.Request) {
	owner, ok := auth.OwnerID(r.Context())
	if !ok {
		writeError(w, 401, "unauthenticated", "A valid session is required.", "")
		return
	}
	callID := r.PathValue("id")
	if !uuidPattern.MatchString(callID) {
		writeError(w, 400, "invalid_argument", "Call id must be a UUID.", "id")
		return
	}
	req, field, ok := decode(w, r)
	if !ok {
		writeError(w, 400, "invalid_argument", "Expected {action, idempotency_key} with action pause, resume or end.", field)
		return
	}

	result, err := cfg.Store.Control(r.Context(), owner, callID, req.IdempotencyKey, req.Action)
	switch {
	case err == nil:
	case errors.Is(err, ErrAction):
		writeError(w, 409, "conflict", "This control is not available while the call is in its current state.", "action")
		return
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, 404, "not_found", "Call not found.", "")
		return
	case errors.Is(err, repository.ErrConflict):
		writeError(w, 409, "conflict", "This idempotency key was already used for a different request.", "idempotency_key")
		return
	case errors.Is(err, repository.ErrInvalid):
		writeError(w, 400, "invalid_argument", "This control request is invalid.", "")
		return
	default:
		writeError(w, 503, "provider_unavailable", "The call could not be updated. Try again.", "")
		return
	}
	if len(result.Events) > 0 {
		cfg.Publish(result.Events...)
	}
	if err := cfg.effect(callID, req.Action, result.Call.Status); err != nil {
		// The durable state is already committed and published. Report the provider
		// failure without vendor detail; repeating the same request retries it.
		slog.Warn("call control effect failed", "action", req.Action)
		writeError(w, 503, "provider_unavailable", "The call was updated here, but the phone provider did not confirm it. Retry to try again.", "")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(result.Raw)
}

// effect applies the committed status to live work. It is keyed on the resulting
// status, not on whether this request changed it, so replays repeat it safely.
func (cfg Config) effect(callID, action, status string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.EffectTimeout)
	defer cancel()
	switch {
	case terminal(status):
		return cfg.Runtime.End(ctx, callID)
	case action == "pause" && status == "paused":
		return cfg.Runtime.Pause(ctx, callID)
	case action == "resume" && status == "connected":
		cfg.Runtime.Resume(callID)
	}
	return nil
}

func terminal(status string) bool {
	return status == "ended" || status == "failed" || status == "cancelled"
}

func active(status string) bool {
	return status == "generating" || status == "suggested" || status == "approved" || status == "speaking"
}

// Apply performs one control action inside a locked call transaction and returns the
// resulting call and the events it appended (none for a no-op).
func Apply(t Tx, action string) (repository.Call, []repository.Event, error) {
	call := t.Call()
	var next string
	switch action {
	case "pause":
		switch call.Status {
		case "paused":
			return call, nil, nil
		case "connected":
			next = "paused"
		default:
			return call, nil, ErrAction
		}
	case "resume":
		switch call.Status {
		case "connected":
			return call, nil, nil
		case "paused":
			next = "connected"
		default:
			return call, nil, ErrAction
		}
	case "end":
		switch call.Status {
		case "ended", "failed", "cancelled":
			return call, nil, nil
		case "connected", "paused":
			next = "ended"
		default: // draft, dialing: the call never connected
			next = "cancelled"
		}
	default:
		return call, nil, repository.ErrInvalid
	}

	var events []repository.Event
	if action != "resume" {
		// Pause and end cancel any reply that has not finished, before the call event,
		// so a client that stops applying responses after end still sees them retired.
		responses, err := t.Responses()
		if err != nil {
			return call, nil, err
		}
		for _, r := range responses {
			if !active(r.Status) {
				continue
			}
			r.Status = "cancelled"
			if err := t.PutResponse(r); err != nil {
				return call, nil, err
			}
			// Older-revision replies are already retired by every client; only the
			// current revision's reply needs an event (payload revision must match).
			if r.ContextRevision == call.ContextRevision {
				e, err := t.AppendEvent("response.updated", r)
				if err != nil {
					return call, nil, err
				}
				events = append(events, e)
			}
		}
	}
	if err := t.SetCall(call.ContextRevision, next, call.ContextRevision); err != nil {
		return call, nil, err
	}
	call = t.Call()
	e, err := t.AppendEvent("call.updated", call)
	if err != nil {
		return call, nil, err
	}
	return call, append(events, e), nil
}

// decode reads a bounded JSON object with exactly the two contract fields.
func decode(w http.ResponseWriter, r *http.Request) (request, string, bool) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return request{}, "", false
	}
	fields, field, err := object(json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)))
	if err != nil {
		return request{}, field, false
	}
	for key := range fields {
		if key != "action" && key != "idempotency_key" {
			return request{}, key, false
		}
	}
	var req request
	if json.Unmarshal(fields["action"], &req.Action) != nil || (req.Action != "pause" && req.Action != "resume" && req.Action != "end") {
		return request{}, "action", false
	}
	if json.Unmarshal(fields["idempotency_key"], &req.IdempotencyKey) != nil || !uuidPattern.MatchString(req.IdempotencyKey) {
		return request{}, "idempotency_key", false
	}
	return req, "", true
}

// object decodes exactly one JSON object, rejecting duplicate keys and trailing data.
func object(d *json.Decoder) (map[string]json.RawMessage, string, error) {
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, "", io.ErrUnexpectedEOF
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil, "", io.ErrUnexpectedEOF
		}
		if _, dup := out[key]; dup {
			return nil, key, io.ErrUnexpectedEOF
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, key, err
		}
		out[key] = value
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') || d.Decode(new(json.RawMessage)) != io.EOF {
		return nil, "", io.ErrUnexpectedEOF
	}
	return out, "", nil
}
