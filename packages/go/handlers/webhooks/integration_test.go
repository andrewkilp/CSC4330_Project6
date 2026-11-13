package webhooks

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"github.com/jackc/pgx/v5/pgxpool"
)

func callbackDB(t *testing.T) (*repository.Repository, *pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL to a disposable *_test Postgres database")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("refusing non-test callback database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("test database unavailable")
	}
	schema := "webhooks_test_" + strings.ReplaceAll(repository.NewID(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal("cannot create disposable callback schema")
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("test database unavailable")
	}
	t.Cleanup(func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	for _, file := range []string{"00001_calls.sql", "00002_repository_transactions.sql", "00003_call_start_intents.sql"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", file))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, strings.Split(string(b), "-- +goose Down")[0]); err != nil {
			t.Fatal("cannot apply callback test migration")
		}
	}
	return repository.New(pool), pool, ctx
}

func callbackDraft(mode string) repository.Draft {
	party := 4
	return repository.Draft{Mode: mode, Destination: "+12255550110", BusinessName: "Fictional bistro (mock data)", ApprovalMode: "always", ConsentAcknowledged: true,
		Goal: repository.Goal{Kind: "restaurant", PreferredAt: "2030-11-15T19:00:00Z", Timezone: "UTC", PartySize: &party, Constraints: []string{}, ContactName: "Fictional guest"}}
}

func reservedFixture(t *testing.T, r *repository.Repository, ctx context.Context, mode string) (repository.Call, string, string, signedFixture) {
	t.Helper()
	owner, key := repository.NewID(), repository.NewID()
	c, err := r.Create(ctx, owner, repository.NewID(), callbackDraft(mode))
	if err != nil {
		t.Fatal(err)
	}
	f := fixture(t, "status")
	u, _ := url.Parse(f.URL)
	q := u.Query()
	attempt := repository.NewID()
	q.Set("call_id", c.ID)
	q.Set("attempt_id", attempt)
	u.RawQuery = q.Encode()
	f.URL = u.String()
	hash := sha256.Sum256([]byte(q.Get("token")))
	policy := repository.StartPolicy{EnableReal: true, AllowedNumbers: []string{c.Destination}, AccountSID: config(t).AccountSID}
	if _, err = r.ReserveCallStart(ctx, owner, c.ID, key, policy, repository.StartIntent{AttemptID: attempt, TokenHash: hash[:]}); err != nil {
		t.Fatal(err)
	}
	return c, owner, key, f
}

func TestIntegrationSignedCallbacksCommitBeforePublishAndNeverRegress(t *testing.T) {
	r, _, ctx := callbackDB(t)
	call, owner, key, f := reservedFixture(t, r, ctx, "real")
	cfg := config(t)
	cfg.MediaEnabled = true
	published := 0
	cfg.Publish = func(_ context.Context, events []repository.Event) {
		stored, err := r.GetCall(ctx, owner, call.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 {
			t.Fatal("wrong published event count")
		}
		var payload repository.Call
		if json.Unmarshal(events[0].Payload, &payload) != nil || payload.Status != stored.Status {
			t.Fatal("event published before matching state committed")
		}
		durable, err := r.EventsAfter(ctx, owner, call.ID, events[0].Seq-1, 10)
		if err != nil || len(durable) == 0 || durable[0].ID != events[0].ID {
			t.Fatal("published event not committed")
		}
		published++
	}
	h := handler(t, cfg, r)
	voice := httptest.NewRecorder()
	h.ServeHTTP(voice, request(t, f, VoicePath))
	if voice.Code != 200 || !strings.Contains(voice.Body.String(), "<Connect>") {
		t.Fatalf("signed pre-REST voice=%d", voice.Code)
	}
	stored, err := r.GetCall(ctx, owner, call.ID)
	if err != nil || stored.Status != "dialing" || published != 0 {
		t.Fatal("voice invented connected status")
	}
	u, _ := url.Parse(f.URL)
	attempt := u.Query().Get("attempt_id")
	for _, status := range []string{"initiated", "in-progress", "in-progress", "ringing", "completed", "in-progress", "completed"} {
		req := request(t, f, StatusPath)
		resign(t, req, cfg, strings.Replace(f.Body, "in-progress", status, 1))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 204 {
			t.Fatalf("signed %s=%d", status, w.Code)
		}
	}
	stored, err = r.GetCall(ctx, owner, call.ID)
	if err != nil || stored.Status != "ended" || published != 2 {
		t.Fatalf("status=%s published=%d err=%v", stored.Status, published, err)
	}
	events, err := r.EventsAfter(ctx, owner, call.ID, 0, 100)
	if err != nil || len(events) != 4 {
		t.Fatalf("durable event count=%d err=%v", len(events), err)
	}
	lateVoice := httptest.NewRecorder()
	h.ServeHTTP(lateVoice, request(t, f, VoicePath))
	if lateVoice.Code != 200 || !strings.Contains(lateVoice.Body.String(), "<Hangup>") {
		t.Fatal("terminal voice returned media stream")
	}
	// The asynchronous provider association won the race with the REST response.
	if _, err = r.ConfirmCallStart(ctx, owner, call.ID, attempt, "CA"+strings.Repeat("b", 32)); err != nil {
		t.Fatal(err)
	}
	replay, err := r.ReserveCallStart(ctx, owner, call.ID, key, repository.StartPolicy{}, repository.StartIntent{AttemptID: repository.NewID(), TokenHash: make([]byte, 32)})
	if err != nil || replay.Fresh {
		t.Fatal("callback-associated start retry could dial again")
	}
	for _, change := range []string{"token", "attempt_id", "call_id", "CallSid"} {
		req := request(t, f, StatusPath)
		body := f.Body
		if change == "CallSid" {
			body = strings.Replace(body, "CA"+strings.Repeat("b", 32), "CA"+strings.Repeat("c", 32), 1)
		} else {
			q := req.URL.Query()
			value := repository.NewID()
			if change == "token" {
				value = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
			}
			q.Set(change, value)
			req.URL.RawQuery = q.Encode()
		}
		resign(t, req, cfg, body)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 404 {
			t.Fatalf("wrong %s=%d", change, w.Code)
		}
	}
	if published != 2 {
		t.Fatal("wrong correlation published events")
	}
}

func TestIntegrationSignedTwilioCannotBindMockStart(t *testing.T) {
	r, _, ctx := callbackDB(t)
	_, _, _, f := reservedFixture(t, r, ctx, "mock")
	h := handler(t, config(t), r)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request(t, f, VoicePath))
	if w.Code != 404 {
		t.Fatalf("mock association accepted: %d", w.Code)
	}
}

func TestIntegrationEventFailureRollsBackStateAndDoesNotPublish(t *testing.T) {
	r, pool, ctx := callbackDB(t)
	call, owner, _, f := reservedFixture(t, r, ctx, "real")
	if _, err := pool.Exec(ctx, "DROP TABLE call_events"); err != nil {
		t.Fatal(err)
	}
	cfg := config(t)
	cfg.Publish = func(context.Context, []repository.Event) { t.Fatal("published rolled-back event") }
	h := handler(t, cfg, r)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request(t, f, StatusPath))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed transaction=%d", w.Code)
	}
	stored, err := r.GetCall(ctx, owner, call.ID)
	if err != nil || stored.Status != "dialing" {
		t.Fatal("status did not roll back after event failure")
	}
}
