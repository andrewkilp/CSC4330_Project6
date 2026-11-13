package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	pipelineAccount      = "AC11111111111111111111111111111111"
	pipelineSID          = "CA22222222222222222222222222222222"
	pipelineAuthToken    = "pipeline-fixture-auth-token-not-real"
	pipelinePublicOrigin = "https://callbacks.example.invalid"
	pipelineAppOrigin    = "http://localhost:8081"
)

type pipelineTransportFunc func(*http.Request) (*http.Response, error)

func (f pipelineTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func pipelineDatabase(t *testing.T) (*repository.Repository, context.Context) {
	t.Helper()
	dsn := os.Getenv("SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL to a disposable *_test database for composed API evidence")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("refusing non-test database configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("test database unavailable")
	}
	schema := "pipeline_test_" + strings.ReplaceAll(repository.NewID(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal("cannot create disposable test schema")
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("cannot open test pool")
	}
	t.Cleanup(func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	paths, err := filepath.Glob(filepath.Join("..", "..", "db", "migrations", "*.sql"))
	if err != nil || len(paths) == 0 {
		t.Fatal("test migrations unavailable")
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		up := strings.Split(string(data), "-- +goose Down")[0]
		if _, err = pool.Exec(ctx, up); err != nil {
			t.Fatalf("migration %s failed: %v", filepath.Base(path), err)
		}
	}
	return repository.New(pool), ctx
}

func pipelineRequest(h http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", pipelineAppOrigin)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func pipelineCall(t *testing.T, w *httptest.ResponseRecorder, status int) repository.Call {
	t.Helper()
	if w.Code != status {
		t.Fatalf("API status=%d want=%d body=%s", w.Code, status, w.Body)
	}
	var call repository.Call
	if json.Unmarshal(w.Body.Bytes(), &call) != nil || call.ID == "" || call.OwnerID == "" {
		t.Fatalf("invalid API call result: %s", w.Body)
	}
	return call
}

// Sign independently of the webhook package using the outbound callback URL
// captured at the fake REST boundary. No production validator helper is reused.
func pipelineCallback(h http.Handler, callbackURL, status string, valid bool) *httptest.ResponseRecorder {
	form := url.Values{"AccountSid": {pipelineAccount}, "CallSid": {pipelineSID}, "To": {"+12255550110"}, "From": {"+12255550111"}, "CallStatus": {status}, "Direction": {"outbound-api"}}
	keys := make([]string, 0, len(form))
	for key := range form {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	mac := hmac.New(sha1.New, []byte(pipelineAuthToken))
	_, _ = mac.Write([]byte(callbackURL))
	for _, key := range keys {
		for _, value := range form[key] {
			_, _ = mac.Write([]byte(key + value))
		}
	}
	signature := mac.Sum(nil)
	if !valid {
		signature[0] ^= 0xff
	}
	r := httptest.NewRequest(http.MethodPost, callbackURL, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Twilio-Signature", base64.StdEncoding.EncodeToString(signature))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestIntegrationComposedDraftStartAndSignedCallbackPipeline(t *testing.T) {
	repo, ctx := pipelineDatabase(t)
	// No external HTTP is possible through the provider client in this test.
	// Install the rejecting fake before enabling the server's real-call policy.
	originalTransport := http.DefaultTransport
	var router http.Handler
	var providerPosts atomic.Int32
	var hangupOnce sync.Once
	hangupDone := make(chan struct{})
	var voiceURL, statusURL string
	http.DefaultTransport = pipelineTransportFunc(func(r *http.Request) (*http.Response, error) {
		// #31/#36 now hang up registered live work after a committed terminal
		// callback. Accept only that exact synthetic provider request as well.
		if r.Method == http.MethodPost && r.URL.Scheme == "https" && r.URL.Host == "api.twilio.com" && r.URL.Path == "/2010-04-01/Accounts/"+pipelineAccount+"/Calls/"+pipelineSID+".json" {
			account, token, ok := r.BasicAuth()
			if !ok || account != pipelineAccount || token != pipelineAuthToken {
				t.Error("invalid fixture hangup credentials")
			}
			if err := r.ParseForm(); err != nil || r.PostForm.Get("Status") != "completed" {
				t.Error("invalid fixture hangup request")
			}
			defer hangupOnce.Do(func() { close(hangupDone) })
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"sid":"` + pipelineSID + `"}`))}, nil
		}
		if r.Method != http.MethodPost || r.URL.Scheme != "https" || r.URL.Host != "api.twilio.com" || r.URL.Path != "/2010-04-01/Accounts/"+pipelineAccount+"/Calls.json" {
			t.Error("unexpected HTTP request blocked by pipeline test transport")
			return nil, errors.New("unexpected test request")
		}
		providerPosts.Add(1)
		account, token, ok := r.BasicAuth()
		if !ok || account != pipelineAccount || token != pipelineAuthToken {
			t.Error("provider did not use fixture account credentials")
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		form, err := url.ParseQuery(string(data))
		if err != nil {
			return nil, err
		}
		voiceURL, statusURL = form.Get("Url"), form.Get("StatusCallback")
		if form.Get("To") != "+12255550110" || !strings.HasPrefix(voiceURL, pipelinePublicOrigin+"/webhooks/twilio/voice?") || !strings.HasPrefix(statusURL, pipelinePublicOrigin+"/webhooks/twilio/status?") {
			t.Error("outbound request lost its reviewed destination or configured callbacks")
		}
		// Twilio can deliver callbacks before its REST response is observed.
		// Both traverse the actual composed API and committed start ledger here.
		voice := pipelineCallback(router, voiceURL, "in-progress", true)
		if voice.Code != 200 || !strings.Contains(voice.Body.String(), "<Hangup>") {
			t.Errorf("pre-REST signed voice status=%d; pending media bridge must hang up", voice.Code)
		}
		answered := pipelineCallback(router, statusURL, "in-progress", true)
		if answered.Code != 204 {
			t.Errorf("pre-REST signed status=%d body=%s", answered.Code, answered.Body)
		}
		body := `{"sid":"` + pipelineSID + `","account_sid":"` + pipelineAccount + `"}`
		return &http.Response{StatusCode: 201, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	t.Setenv("ENABLE_REAL_CALLS", "true")
	t.Setenv("TWILIO_WEBHOOKS_ENABLED", "")
	t.Setenv("CALL_ALLOWED_NUMBERS", "+12255550110")
	t.Setenv("PUBLIC_BASE_URL", pipelinePublicOrigin)
	t.Setenv("TWILIO_ACCOUNT_SID", pipelineAccount)
	t.Setenv("TWILIO_AUTH_TOKEN", pipelineAuthToken)
	t.Setenv("TWILIO_FROM_NUMBER", "+12255550111")
	var err error
	router, err = configuredRouter("development", repo, pipelineAppOrigin)
	if err != nil {
		t.Fatal(err)
	}
	session := pipelineRequest(router, http.MethodPost, "/v1/session", "{}", nil)
	if session.Code != 201 {
		t.Fatalf("session status=%d body=%s", session.Code, session.Body)
	}
	cookies := session.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("shared session cookie missing")
	}
	cookie := cookies[0]
	createBody := `{"mode":"real","destination":"+12255550110","business_name":"Pipeline Bistro (mock)","goal":{"kind":"restaurant","preferred_at":"2026-11-20T19:00:00-06:00","timezone":"America/Chicago","party_size":4,"constraints":[],"contact_name":"Fictional Guest"},"approval_mode":"always","consent_acknowledged":true,"idempotency_key":"00000000-0000-4000-8000-000000000001"}`
	created := pipelineCall(t, pipelineRequest(router, http.MethodPost, "/v1/calls", createBody, cookie), 201)
	if created.Status != "draft" || providerPosts.Load() != 0 {
		t.Fatal("creating a draft contacted the provider")
	}
	read := pipelineCall(t, pipelineRequest(router, http.MethodGet, "/v1/calls/"+created.ID, "", cookie), 200)
	if read.ID != created.ID || providerPosts.Load() != 0 {
		t.Fatal("reading a draft contacted the provider")
	}
	startBody := `{"idempotency_key":"00000000-0000-4000-8000-000000000002"}`
	started := pipelineCall(t, pipelineRequest(router, http.MethodPost, "/v1/calls/"+created.ID+"/start", startBody, cookie), 200)
	if started.ID != created.ID || providerPosts.Load() != 1 {
		t.Fatal("explicit start did not make exactly one fake REST request")
	}
	pipelineCall(t, pipelineRequest(router, http.MethodPost, "/v1/calls/"+created.ID+"/start", startBody, cookie), 200)
	if providerPosts.Load() != 1 {
		t.Fatal("start retry dialed twice")
	}
	connected := pipelineCall(t, pipelineRequest(router, http.MethodGet, "/v1/calls/"+created.ID, "", cookie), 200)
	if connected.Status != "connected" {
		t.Fatalf("callback-before-REST association lost connected state: %s", connected.Status)
	}
	before, err := repo.EventsAfter(ctx, created.OwnerID, created.ID, 0, 100)
	if err != nil || len(before) != 3 {
		t.Fatalf("create/start/connected durable events=%d err=%v", len(before), err)
	}
	bad := pipelineCallback(router, statusURL, "completed", false)
	if bad.Code != 403 {
		t.Fatalf("corrupt callback MAC status=%d", bad.Code)
	}
	unchanged := pipelineCall(t, pipelineRequest(router, http.MethodGet, "/v1/calls/"+created.ID, "", cookie), 200)
	if unchanged.Status != "connected" {
		t.Fatal("corrupt callback MAC changed call state")
	}
	afterBad, err := repo.EventsAfter(ctx, created.OwnerID, created.ID, 0, 100)
	if err != nil || len(afterBad) != len(before) {
		t.Fatal("corrupt callback MAC appended a durable event")
	}
	for _, status := range []string{"in-progress", "ringing", "initiated", "completed", "in-progress", "completed"} {
		if w := pipelineCallback(router, statusURL, status, true); w.Code != 204 {
			t.Fatalf("signed status %s failed: %d %s", status, w.Code, w.Body)
		}
	}
	ended := pipelineCall(t, pipelineRequest(router, http.MethodGet, "/v1/calls/"+created.ID, "", cookie), 200)
	if ended.Status != "ended" {
		t.Fatalf("terminal state regressed to %s", ended.Status)
	}
	events, err := repo.EventsAfter(ctx, created.OwnerID, created.ID, 0, 100)
	if err != nil || len(events) != 4 {
		t.Fatalf("duplicate/out-of-order callbacks changed durable events: %d %v", len(events), err)
	}
	for i, event := range events {
		if event.Seq != int64(i+1) {
			t.Fatal("durable pipeline event sequence has a gap")
		}
	}
	if w := pipelineCallback(router, voiceURL, "in-progress", true); w.Code != 200 || !strings.Contains(w.Body.String(), "<Hangup>") {
		t.Fatal("terminal voice callback opened media")
	}
	if providerPosts.Load() != 1 {
		t.Fatal("callbacks or reads contacted the provider")
	}
	select {
	case <-hangupDone:
	case <-time.After(5 * time.Second):
		t.Fatal("terminal callback did not hang up live work")
	}
}
