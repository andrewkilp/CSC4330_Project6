package calls

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/auth"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

type startFunc func(context.Context, string, string, string) (repository.Call, error)

func (f startFunc) Start(ctx context.Context, owner, id, key string) (repository.Call, error) {
	return f(ctx, owner, id, key)
}

const startTestID = "00000000-0000-4000-8000-000000000001"
const startTestKey = "00000000-0000-4000-8000-000000000002"

func startRequest(body string) *http.Request {
	req := httptest.NewRequest("POST", "/v1/calls/"+startTestID+"/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestStartHandlerUsesSessionOwnerAndExplicitAction(t *testing.T) {
	sessions := auth.NewSessions()
	token, owner := sessions.Create()
	n := 0
	service := startFunc(func(ctx context.Context, gotOwner, id, key string) (repository.Call, error) {
		n++
		if gotOwner != owner || id != startTestID || key != startTestKey {
			t.Fatal("start did not bind to authenticated owner/call/key")
		}
		return repository.Call{ID: id, OwnerID: owner, Mode: "mock", Status: "dialing"}, nil
	})
	mux := http.NewServeMux()
	RegisterStart(mux, NewStartHandler(service, sessions))
	body := `{"idempotency_key":"` + startTestKey + `"}`
	unauthorized := httptest.NewRecorder()
	mux.ServeHTTP(unauthorized, startRequest(body))
	if unauthorized.Code != 401 || n != 0 {
		t.Fatal("missing session invoked start")
	}
	req := startRequest(body)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, req)
	if out.Code != 200 || n != 1 || out.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("start HTTP=%d body=%s", out.Code, out.Body)
	}
	get := httptest.NewRequest("GET", "/v1/calls/"+startTestID+"/start", nil)
	get.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	out = httptest.NewRecorder()
	mux.ServeHTTP(out, get)
	if n != 1 || out.Code != 405 {
		t.Fatal("non-POST implicitly started call")
	}
}

func TestStartHandlerRejectsMalformedAndUnknownFields(t *testing.T) {
	sessions := auth.NewSessions()
	token, _ := sessions.Create()
	mux := http.NewServeMux()
	RegisterStart(mux, NewStartHandler(startFunc(func(context.Context, string, string, string) (repository.Call, error) {
		t.Fatal("invalid body invoked start")
		return repository.Call{}, nil
	}), sessions))
	for _, body := range []string{"null", "[]", "{}", `{"idempotency_key":null}`, `{"idempotency_key":"not-a-uuid"}`, `{"idempotency_key":"` + startTestKey + `","idempotency_key":"` + startTestKey + `"}`, `{"owner_id":"private","idempotency_key":"` + startTestKey + `"}`, `{"idempotency_key":"` + startTestKey + `"} {}`, `{"idempotency_key":"` + strings.Repeat("x", 17000) + `"}`} {
		req := startRequest(body)
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, req)
		if out.Code != 400 {
			t.Fatalf("malformed input HTTP=%d", out.Code)
		}
	}
	req := startRequest(`{"idempotency_key":"` + startTestKey + `"}`)
	req.Header.Set("Content-Type", "text/plain")
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, req)
	if out.Code != 400 {
		t.Fatal("non-JSON request accepted")
	}
}

func TestStartHandlerMapsSafeFailures(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{repository.ErrNotFound, 404, "not_found"}, {repository.ErrConflict, 409, "conflict"}, {repository.ErrInvalid, 400, "invalid_argument"}, {repository.ErrUnavailable, 503, "provider_unavailable"}, {errors.New("private provider key"), 503, "provider_unavailable"}} {
		sessions := auth.NewSessions()
		token, _ := sessions.Create()
		mux := http.NewServeMux()
		RegisterStart(mux, NewStartHandler(startFunc(func(context.Context, string, string, string) (repository.Call, error) {
			return repository.Call{}, tc.err
		}), sessions))
		req := startRequest(`{"idempotency_key":"` + startTestKey + `"}`)
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, req)
		var body struct {
			Error struct {
				Code string `json:"code"`
			}
		}
		if json.Unmarshal(out.Body.Bytes(), &body) != nil || out.Code != tc.status || body.Error.Code != tc.code || strings.Contains(out.Body.String(), "private") {
			t.Fatalf("unsafe failure HTTP=%d body=%s", out.Code, out.Body)
		}
	}
}
