package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/auth"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

const callID = "3d2e9e65-7f30-4ca1-9e80-4b5c6d7e8f92"

// fakeTx is an in-memory repository.Tx.
type fakeTx struct {
	call      repository.Call
	responses []repository.Response
	events    []repository.Event
}

func (t *fakeTx) Call() repository.Call { return t.call }
func (t *fakeTx) Responses() ([]repository.Response, error) {
	return append([]repository.Response(nil), t.responses...), nil
}
func (t *fakeTx) PutResponse(r repository.Response) error {
	for i := range t.responses {
		if t.responses[i].ID == r.ID {
			t.responses[i] = r
			return nil
		}
	}
	return repository.ErrNotFound
}
func (t *fakeTx) SetCall(expected int64, status string, revision int64) error {
	if expected != t.call.ContextRevision {
		return repository.ErrConflict
	}
	t.call.Status, t.call.ContextRevision = status, revision
	return nil
}
func (t *fakeTx) AppendEvent(kind string, payload any) (repository.Event, error) {
	b, _ := json.Marshal(payload)
	e := repository.Event{CallID: t.call.ID, Seq: int64(len(t.events) + 1), Type: kind, ContextRevision: t.call.ContextRevision, Payload: b}
	t.events = append(t.events, e)
	return e, nil
}

func txWith(status string) *fakeTx {
	return &fakeTx{call: repository.Call{ID: callID, Status: status, ContextRevision: 2}, responses: []repository.Response{
		{ID: "r-old", CallID: callID, ContextRevision: 1, Status: "suggested", Text: "stale"},
		{ID: "r-spoken", CallID: callID, ContextRevision: 2, Status: "spoken", Text: "done"},
		{ID: "r-live", CallID: callID, ContextRevision: 2, Status: "speaking", Text: "speaking now"},
	}}
}

func TestApplyTransitions(t *testing.T) {
	cases := []struct {
		action, from, to string
		err              error
		events           []string
	}{
		{"pause", "connected", "paused", nil, []string{"response.updated", "call.updated"}},
		{"pause", "paused", "paused", nil, nil},
		{"pause", "dialing", "dialing", ErrAction, nil},
		{"pause", "ended", "ended", ErrAction, nil},
		{"resume", "paused", "connected", nil, []string{"call.updated"}},
		{"resume", "connected", "connected", nil, nil},
		{"resume", "draft", "draft", ErrAction, nil},
		{"resume", "cancelled", "cancelled", ErrAction, nil},
		{"end", "connected", "ended", nil, []string{"response.updated", "call.updated"}},
		{"end", "paused", "ended", nil, []string{"response.updated", "call.updated"}},
		{"end", "dialing", "cancelled", nil, []string{"response.updated", "call.updated"}},
		{"end", "draft", "cancelled", nil, []string{"response.updated", "call.updated"}},
		{"end", "ended", "ended", nil, nil},
		{"end", "failed", "failed", nil, nil},
		{"hangup", "connected", "connected", repository.ErrInvalid, nil},
	}
	for _, tc := range cases {
		t.Run(tc.action+"-from-"+tc.from, func(t *testing.T) {
			tx := txWith(tc.from)
			call, events, err := Apply(tx, tc.action)
			if !errors.Is(err, tc.err) || call.Status != tc.to || tx.call.Status != tc.to {
				t.Fatalf("Apply = %s, %v; want %s, %v", call.Status, err, tc.to, tc.err)
			}
			var kinds []string
			for _, e := range events {
				kinds = append(kinds, e.Type)
			}
			if strings.Join(kinds, ",") != strings.Join(tc.events, ",") {
				t.Fatalf("events = %v, want %v", kinds, tc.events)
			}
			// Pause and end retire every unfinished reply (old revision too); spoken
			// replies are history and stay spoken. Resume never touches replies.
			for _, r := range tx.responses {
				want := "suggested"
				switch {
				case r.ID == "r-spoken":
					want = "spoken"
				case r.ID == "r-live":
					want = "speaking"
				}
				if tc.err == nil && len(tc.events) > 1 && r.ID != "r-spoken" {
					want = "cancelled"
				}
				if r.Status != want {
					t.Errorf("response %s = %s, want %s", r.ID, r.Status, want)
				}
			}
			// Only the current revision's reply gets an event, and it comes first.
			if len(events) == 2 {
				var r repository.Response
				_ = json.Unmarshal(events[0].Payload, &r)
				if r.ID != "r-live" || r.Status != "cancelled" || events[0].ContextRevision != r.ContextRevision {
					t.Fatalf("first event payload = %+v", r)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// HTTP handler.

// fakeStore mirrors repository.Update: a per-key idempotency record, replay returns
// the original result without events, and a reused key with another action conflicts.
type fakeStore struct {
	mu   sync.Mutex
	tx   *fakeTx
	keys map[string]string
	raw  map[string]json.RawMessage
}

func newStore(status string) *fakeStore {
	tx := txWith(status)
	return &fakeStore{tx: tx, keys: map[string]string{}, raw: map[string]json.RawMessage{}}
}

func (s *fakeStore) Control(_ context.Context, owner, id, key, action string) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != callID {
		return Result{}, repository.ErrNotFound
	}
	if prior, ok := s.keys[key]; ok {
		if prior != action {
			return Result{}, repository.ErrConflict
		}
		var call repository.Call
		_ = json.Unmarshal(s.raw[key], &call)
		return Result{Raw: s.raw[key], Call: call}, nil
	}
	before := len(s.tx.events)
	call, _, err := Apply(s.tx, action)
	if err != nil {
		return Result{}, err
	}
	raw, _ := json.Marshal(call)
	s.keys[key], s.raw[key] = action, raw
	return Result{Raw: raw, Call: call, Events: append([]repository.Event(nil), s.tx.events[before:]...)}, nil
}

type fakeRuntime struct {
	mu                   sync.Mutex
	pauses, resumes, end int
	endErr               error
}

func (r *fakeRuntime) Pause(context.Context, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pauses++
	return nil
}
func (r *fakeRuntime) Resume(string) { r.mu.Lock(); r.resumes++; r.mu.Unlock() }
func (r *fakeRuntime) End(ctx context.Context, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.end++
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return r.endErr
}

type harness struct {
	h         http.Handler
	store     *fakeStore
	runtime   *fakeRuntime
	published *[]repository.Event
	cookie    *http.Cookie
}

func newHarness(t *testing.T, status string) harness {
	t.Helper()
	sessions := auth.NewSessions()
	token, _ := sessions.Create()
	var published []repository.Event
	var mu sync.Mutex
	store, runtime := newStore(status), &fakeRuntime{}
	mux := http.NewServeMux()
	err := Register(mux, Config{Store: store, Runtime: runtime, RequireOwner: sessions.RequireOwner,
		Publish: func(e ...repository.Event) { mu.Lock(); published = append(published, e...); mu.Unlock() }})
	if err != nil {
		t.Fatal(err)
	}
	return harness{mux, store, runtime, &published, &http.Cookie{Name: auth.CookieName, Value: token}}
}

func (h harness) post(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/calls/"+callID+"/control", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(h.cookie)
	w := httptest.NewRecorder()
	h.h.ServeHTTP(w, r)
	return w
}

func body(action, key string) string {
	return `{"action":"` + action + `","idempotency_key":"` + key + `"}`
}

const (
	key1 = "11111111-1111-4111-8111-111111111111"
	key2 = "22222222-2222-4222-8222-222222222222"
	key3 = "33333333-3333-4333-8333-333333333333"
)

func errorField(t *testing.T, w *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	var out struct {
		Error struct{ Code, Field, Message string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("error body %q: %v", w.Body.String(), err)
	}
	return out.Error.Code, out.Error.Field
}

func TestControlRejectsBadRequests(t *testing.T) {
	h := newHarness(t, "connected")
	for name, tc := range map[string]struct{ body, field string }{
		"unknown field":   {`{"action":"pause","idempotency_key":"` + key1 + `","owner_id":"x"}`, "owner_id"},
		"duplicate field": {`{"action":"pause","action":"end","idempotency_key":"` + key1 + `"}`, "action"},
		"bad action":      {body("hangup", key1), "action"},
		"missing key":     {`{"action":"pause"}`, "idempotency_key"},
		"bad key":         {body("pause", "not-a-uuid"), "idempotency_key"},
		"not an object":   {`["pause"]`, ""},
		"trailing data":   {body("pause", key1) + `{}`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			w := h.post(t, tc.body)
			if code, field := errorField(t, w); w.Code != 400 || code != "invalid_argument" || field != tc.field {
				t.Fatalf("%d %s field=%q, want 400 invalid_argument field=%q", w.Code, code, field, tc.field)
			}
		})
	}
	if len(*h.published) != 0 || h.runtime.pauses+h.runtime.end != 0 {
		t.Fatal("a rejected request had side effects")
	}

	noSession := httptest.NewRequest("POST", "/v1/calls/"+callID+"/control", strings.NewReader(body("end", key1)))
	noSession.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.h.ServeHTTP(w, noSession)
	if w.Code != 401 {
		t.Fatalf("no session = %d", w.Code)
	}
	r := httptest.NewRequest("POST", "/v1/calls/not-a-uuid/control", strings.NewReader(body("end", key1)))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(h.cookie)
	w = httptest.NewRecorder()
	h.h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("bad id = %d", w.Code)
	}
	other := httptest.NewRequest("POST", "/v1/calls/99999999-9999-4999-8999-999999999999/control", strings.NewReader(body("end", key1)))
	other.Header.Set("Content-Type", "application/json")
	other.AddCookie(h.cookie)
	w = httptest.NewRecorder()
	h.h.ServeHTTP(w, other)
	if w.Code != 404 {
		t.Fatalf("unknown call = %d", w.Code)
	}
}

func TestPauseResumeEndPublishOnceAndApplyEffects(t *testing.T) {
	h := newHarness(t, "connected")
	w := h.post(t, body("pause", key1))
	var call repository.Call
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &call) != nil || call.Status != "paused" {
		t.Fatalf("pause = %d %s", w.Code, w.Body)
	}
	if len(*h.published) != 2 || h.runtime.pauses != 1 {
		t.Fatalf("pause published %d events, %d runtime pauses", len(*h.published), h.runtime.pauses)
	}
	// A retried request replays the result: no new event, effect repeated (idempotent).
	if w := h.post(t, body("pause", key1)); w.Code != 200 || len(*h.published) != 2 || h.runtime.pauses != 2 {
		t.Fatalf("replayed pause: %d, %d events, %d pauses", w.Code, len(*h.published), h.runtime.pauses)
	}
	if w := h.post(t, body("resume", key1)); w.Code != 409 {
		t.Fatalf("reused key for another action = %d", w.Code)
	} else if _, field := errorField(t, w); field != "idempotency_key" {
		t.Fatalf("reused key field = %s", field)
	}
	if w := h.post(t, body("resume", key2)); w.Code != 200 || h.runtime.resumes != 1 || len(*h.published) != 3 {
		t.Fatalf("resume: %d, %d resumes, %d events", w.Code, h.runtime.resumes, len(*h.published))
	}
	if w := h.post(t, body("end", key3)); w.Code != 200 || h.runtime.end != 1 {
		t.Fatalf("end: %d, %d ends", w.Code, h.runtime.end)
	}
	terminalEvents := len(*h.published)
	// Repeated end with fresh keys: same terminal call, no new event, runtime End repeated.
	for _, key := range []string{"44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555"} {
		w := h.post(t, body("end", key))
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &call) != nil || call.Status != "ended" {
			t.Fatalf("repeated end = %d %s", w.Code, w.Body)
		}
	}
	if len(*h.published) != terminalEvents || h.runtime.end != 3 {
		t.Fatalf("repeated end published %d new events", len(*h.published)-terminalEvents)
	}
	// Nothing can reopen an ended call.
	for _, action := range []string{"pause", "resume"} {
		w := h.post(t, body(action, "66666666-6666-4666-8666-66666666666"+map[string]string{"pause": "6", "resume": "7"}[action]))
		if code, field := errorField(t, w); w.Code != 409 || code != "conflict" || field != "action" {
			t.Fatalf("%s after end = %d %s %s", action, w.Code, code, field)
		}
	}
}

func TestEndDuringDialCancelsAndHangsUp(t *testing.T) {
	h := newHarness(t, "dialing")
	w := h.post(t, body("end", key1))
	var call repository.Call
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &call) != nil || call.Status != "cancelled" || h.runtime.end != 1 {
		t.Fatalf("end during dial = %d %s, %d ends", w.Code, w.Body, h.runtime.end)
	}
}

func TestHangupFailureIsVisibleAndRetryable(t *testing.T) {
	h := newHarness(t, "connected")
	h.runtime.endErr = errors.New("vendor said: account suspended")
	w := h.post(t, body("end", key1))
	if code, _ := errorField(t, w); w.Code != 503 || code != "provider_unavailable" || strings.Contains(w.Body.String(), "suspended") {
		t.Fatalf("hang-up failure = %d %s", w.Code, w.Body)
	}
	// The terminal state was still committed and published once.
	if h.store.tx.call.Status != "ended" || len(*h.published) != 2 {
		t.Fatalf("status %s, %d events", h.store.tx.call.Status, len(*h.published))
	}
	h.runtime.endErr = nil
	if w := h.post(t, body("end", key1)); w.Code != 200 || h.runtime.end != 2 || len(*h.published) != 2 {
		t.Fatalf("retried end = %d, %d ends, %d events", w.Code, h.runtime.end, len(*h.published))
	}
}

func TestRegisterRequiresDependencies(t *testing.T) {
	sessions := auth.NewSessions()
	ok := Config{Store: newStore("connected"), Runtime: &fakeRuntime{}, Publish: func(...repository.Event) {}, RequireOwner: sessions.RequireOwner}
	for name, mutate := range map[string]func(*Config){
		"store": func(c *Config) { c.Store = nil }, "runtime": func(c *Config) { c.Runtime = nil },
		"publish": func(c *Config) { c.Publish = nil }, "session": func(c *Config) { c.RequireOwner = nil },
	} {
		c := ok
		mutate(&c)
		if Register(http.NewServeMux(), c) == nil {
			t.Errorf("Register without %s succeeded", name)
		}
	}
}
