package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

const apiStartID = "00000000-0000-4000-8000-000000000001"
const apiStartKey = "00000000-0000-4000-8000-000000000002"

func sessionCookie(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/session", strings.NewReader("{}")))
	if w.Code != 201 {
		t.Fatalf("session=%d %s", w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing session cookie")
	}
	return cookies[0]
}
func apiStartRequest(cookie *http.Cookie) *http.Request {
	r := httptest.NewRequest("POST", "/v1/calls/"+apiStartID+"/start", strings.NewReader(`{"idempotency_key":"`+apiStartKey+`"}`))
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return r
}

func TestStartRouteIsMountedAuthenticatedAndUnavailableWithoutDB(t *testing.T) {
	t.Setenv("ENABLE_REAL_CALLS", "false")
	for _, typedNil := range []bool{false, true} {
		var store *repository.Repository
		var h http.Handler
		var err error
		if typedNil {
			h, err = configuredRouter("development", store, "")
		} else {
			h, err = configuredRouter("development", nil, "")
		}
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, apiStartRequest(nil))
		if w.Code != 401 {
			t.Fatalf("unauthenticated start=%d", w.Code)
		}
		cookie := sessionCookie(t, h)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, apiStartRequest(cookie))
		if w.Code != 503 || !strings.Contains(w.Body.String(), "provider_unavailable") {
			t.Fatalf("no DB start=%d %s", w.Code, w.Body)
		}
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
		if w.Code != 200 {
			t.Fatal("start wiring lost health")
		}
	}
}

func TestStartEnvironmentDefaultsDisabledAndStrictlyValidatesOptIn(t *testing.T) {
	t.Setenv("ENABLE_REAL_CALLS", "")
	for _, name := range []string{"TWILIO_ACCOUNT_SID", "TWILIO_AUTH_TOKEN", "TWILIO_FROM_NUMBER", "CALL_ALLOWED_NUMBERS", "PUBLIC_BASE_URL"} {
		t.Setenv(name, "private-invalid-config")
	}
	if _, err := configuredRouter("development", nil, ""); err != nil {
		t.Fatal("default mock mode constructed a real provider")
	}
	for _, raw := range []string{"TRUE", "1", " true", "false ", "private-invalid-config"} {
		t.Setenv("ENABLE_REAL_CALLS", raw)
		if _, err := configuredRouter("development", nil, ""); err == nil || strings.Contains(err.Error(), "private") {
			t.Fatalf("invalid opt-in accepted/leaked: %q %v", raw, err)
		}
	}
	t.Setenv("ENABLE_REAL_CALLS", "true")
	if _, err := configuredRouter("development", nil, ""); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("enabled invalid provider config did not fail closed")
	}
	t.Setenv("TWILIO_ACCOUNT_SID", "AC11111111111111111111111111111111")
	t.Setenv("TWILIO_AUTH_TOKEN", "test-only")
	t.Setenv("TWILIO_FROM_NUMBER", "+12255550101")
	t.Setenv("CALL_ALLOWED_NUMBERS", "+12255550100")
	t.Setenv("PUBLIC_BASE_URL", "https://demo.example?")
	if _, err := configuredRouter("development", nil, ""); err == nil {
		t.Fatal("invalid callback origin accepted")
	}
	t.Setenv("PUBLIC_BASE_URL", "https://demo.example")
	if _, err := configuredRouter("development", nil, ""); err != nil {
		t.Fatal("valid opt-in configuration rejected; constructors must not dial")
	}
}

type startOnlyAPIStore struct {
	owner    string
	reserves int
	call     repository.Call
}

func (s *startOnlyAPIStore) Create(context.Context, string, string, repository.Draft) (repository.Call, error) {
	return repository.Call{}, repository.ErrUnavailable
}
func (s *startOnlyAPIStore) GetCall(context.Context, string, string) (repository.Call, error) {
	return s.call, nil
}
func (s *startOnlyAPIStore) ReserveCallStart(ctx context.Context, owner, id, key string, p repository.StartPolicy, intent repository.StartIntent) (repository.StartAttempt, error) {
	if p.EnableReal {
		return repository.StartAttempt{}, errors.New("test unexpectedly enabled real calls")
	}
	s.owner = owner
	s.reserves++
	s.call = repository.Call{ID: id, OwnerID: owner, Mode: "mock", Destination: "+12255550100", Status: "dialing"}
	return repository.StartAttempt{Call: s.call, AttemptID: intent.AttemptID, Fresh: true}, nil
}
func (s *startOnlyAPIStore) ConfirmCallStart(ctx context.Context, owner, id, attempt, sid string) (repository.Call, error) {
	if !strings.HasPrefix(sid, "MOCKCA") {
		return repository.Call{}, errors.New("real provider was selected")
	}
	return s.call, nil
}
func (s *startOnlyAPIStore) MarkCallStartAmbiguous(context.Context, string, string, string) error {
	return nil
}

func TestStartUsesSharedSessionStoreAndOriginGuard(t *testing.T) {
	t.Setenv("ENABLE_REAL_CALLS", "false")
	store := &startOnlyAPIStore{}
	h, err := configuredRouter("development", store, "http://localhost:8081")
	if err != nil {
		t.Fatal(err)
	}
	cookie := sessionCookie(t, h)
	bad := apiStartRequest(cookie)
	bad.Header.Set("Origin", "https://evil.invalid")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, bad)
	if w.Code != 400 || store.reserves != 0 {
		t.Fatal("foreign origin reached start service")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, apiStartRequest(cookie))
	if w.Code != 200 || store.reserves != 1 || store.owner == "" {
		t.Fatalf("shared auth start=%d %s", w.Code, w.Body)
	}
	var call repository.Call
	if json.Unmarshal(w.Body.Bytes(), &call) != nil || call.OwnerID != store.owner || call.Mode != "mock" {
		t.Fatal("start owner/mode contract lost")
	}
	logout := httptest.NewRequest("DELETE", "/v1/session", strings.NewReader("{}"))
	logout.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, logout)
	if w.Code != 204 {
		t.Fatal("logout route lost")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, apiStartRequest(cookie))
	if w.Code != 401 || store.reserves != 1 {
		t.Fatal("revoked session started another call")
	}
}
