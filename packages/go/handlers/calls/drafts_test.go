package calls_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/auth"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/calls"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/session"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"github.com/jackc/pgx/v5/pgxpool"
)

const draftJSON = `{"mode":"mock","destination":"+12255550110","business_name":"Bistro (mock)","goal":{"kind":"restaurant","preferred_at":"2026-11-20T19:00:00-06:00","timezone":"America/Chicago","party_size":4,"constraints":[],"contact_name":"Jordan Lee"},"approval_mode":"always","consent_acknowledged":true,"idempotency_key":"00000000-0000-4000-8000-000000000001"}`

type memoryDrafts struct {
	mu     sync.Mutex
	rows   map[string]repository.Call
	keys   map[string]string
	bodies map[string]string
}

func (m *memoryDrafts) Create(ctx context.Context, owner, key string, d repository.Draft) (repository.Call, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key = owner + ":" + key
	b, _ := json.Marshal(d)
	if id, ok := m.keys[key]; ok {
		if m.bodies[key] != string(b) {
			return repository.Call{}, repository.ErrConflict
		}
		return m.rows[id], nil
	}
	c := repository.Call{ID: repository.NewID(), OwnerID: owner, Mode: d.Mode, Destination: d.Destination, BusinessName: d.BusinessName, Goal: d.Goal, Status: "draft", ApprovalMode: d.ApprovalMode, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	m.rows[c.ID] = c
	m.keys[key] = c.ID
	m.bodies[key] = string(b)
	return c, nil
}
func (m *memoryDrafts) GetCall(ctx context.Context, owner, id string) (repository.Call, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.rows[id]
	if !ok || c.OwnerID != owner {
		return repository.Call{}, repository.ErrNotFound
	}
	return c, nil
}
func newMemoryDrafts() *memoryDrafts {
	return &memoryDrafts{rows: map[string]repository.Call{}, keys: map[string]string{}, bodies: map[string]string{}}
}
func draftServer(t *testing.T, store calls.DraftStore) (*httptest.Server, *http.Client) {
	t.Helper()
	s := auth.NewSessions()
	mux := http.NewServeMux()
	session.Register(mux, s, false)
	calls.RegisterDrafts(mux, calls.NewDraftHandler(store, s))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	jar, _ := cookiejar.New(nil)
	return server, &http.Client{Jar: jar}
}
func requestDraft(t *testing.T, client *http.Client, method, url, body string, want int) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != want {
		var b any
		_ = json.NewDecoder(resp.Body).Decode(&b)
		t.Fatalf("%s returned %d, want %d: %+v", method, resp.StatusCode, want, b)
	}
	return resp
}
func draftSession(t *testing.T, s *httptest.Server, c *http.Client) {
	requestDraft(t, c, "POST", s.URL+"/v1/session", "{}", 201)
}
func draftDecode(t *testing.T, r *http.Response) repository.Call {
	t.Helper()
	var c repository.Call
	if json.NewDecoder(r.Body).Decode(&c) != nil {
		t.Fatal("invalid call response")
	}
	return c
}

func TestDraftCreateReadOwnerAndRetry(t *testing.T) {
	s, c := draftServer(t, newMemoryDrafts())
	requestDraft(t, c, "POST", s.URL+"/v1/calls", draftJSON, 401)
	draftSession(t, s, c)
	a := draftDecode(t, requestDraft(t, c, "POST", s.URL+"/v1/calls", draftJSON, 201))
	b := draftDecode(t, requestDraft(t, c, "POST", s.URL+"/v1/calls", draftJSON, 201))
	if a.ID != b.ID || a.Status != "draft" || a.OwnerID == "" {
		t.Fatalf("invalid draft/retry %+v %+v", a, b)
	}
	got := draftDecode(t, requestDraft(t, c, "GET", s.URL+"/v1/calls/"+a.ID, "", 200))
	if got.ID != a.ID {
		t.Fatal("wrong call")
	}
	requestDraft(t, c, "POST", s.URL+"/v1/calls", strings.Replace(draftJSON, `Bistro (mock)`, `Other (mock)`, 1), 409)
	jar, _ := cookiejar.New(nil)
	other := &http.Client{Jar: jar}
	draftSession(t, s, other)
	requestDraft(t, other, "GET", s.URL+"/v1/calls/"+a.ID, "", 404)
	requestDraft(t, c, "GET", s.URL+"/v1/calls/not-a-uuid", "", 404)
	requestDraft(t, c, "POST", s.URL+"/v1/calls/"+a.ID+"/start", `{}`, 404) // Draft handler contains no start implementation.
	appointment := strings.NewReplacer(`"restaurant"`, `"appointment"`, `"party_size":4`, `"service":"Haircut"`, `000000000001`, `000000000002`).Replace(draftJSON)
	app := draftDecode(t, requestDraft(t, c, "POST", s.URL+"/v1/calls", appointment, 201))
	if app.Goal.Service != "Haircut" || app.Goal.PartySize != nil || app.Status != "draft" {
		t.Fatal("invalid appointment draft")
	}
}
func TestDraftValidationRejectsMalformedInputs(t *testing.T) {
	s, c := draftServer(t, newMemoryDrafts())
	draftSession(t, s, c)
	for _, tc := range []struct{ name, body, field string }{
		{"unknown", strings.Replace(draftJSON, `"mode":`, `"owner_id":"claimed","mode":`, 1), "owner_id"},
		{"duplicate", strings.Replace(draftJSON, `"mode":"mock"`, `"mode":"real","mode":"mock"`, 1), "mode"},
		{"nested unknown", strings.Replace(draftJSON, `"kind":`, `"unexpected":1,"kind":`, 1), "goal.unexpected"},
		{"real destination", strings.Replace(draftJSON, "+12255550110", "+12255551234", 1), "destination"},
		{"date", strings.Replace(draftJSON, "2026-11-20", "2026-02-30", 1), "goal.preferred_at"},
		{"offset", strings.Replace(draftJSON, "19:00:00-06:00", "19:00:00-05:00", 1), "goal.preferred_at"},
		{"zone", strings.Replace(draftJSON, "America/Chicago", "Mars/Olympus", 1), "goal.timezone"},
		{"null constraints", strings.Replace(draftJSON, `"constraints":[]`, `"constraints":null`, 1), "goal.constraints"},
		{"bad consent", strings.Replace(draftJSON, `"consent_acknowledged":true`, `"consent_acknowledged":"true"`, 1), "consent_acknowledged"},
		{"opposite field", strings.Replace(draftJSON, `"party_size":4`, `"party_size":4,"service":"Haircut"`, 1), "goal.service"},
		{"long unicode", strings.Replace(draftJSON, "Jordan Lee", strings.Repeat("😀", 61), 1), "goal.contact_name"},
		{"oversize", strings.Repeat(" ", 16384) + draftJSON, "request"}, {"trailing", draftJSON + `{}`, "request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := requestDraft(t, c, "POST", s.URL+"/v1/calls", tc.body, 400)
			var body struct{ Error struct{ Field string } }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Error.Field != tc.field {
				t.Fatalf("field=%q want%q", body.Error.Field, tc.field)
			}
		})
	}
}

func TestDraftIntegrationPostgres(t *testing.T) {
	dsn := os.Getenv("SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set dedicated *_test database URL for HTTP/Postgres evidence")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("refusing non-test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("test database unavailable")
	}
	defer admin.Close()
	schema := "draft_http_test_" + strings.ReplaceAll(repository.NewID(), "-", "")
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, `DROP SCHEMA `+schema+` CASCADE`)
	}()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, name := range []string{"00001_calls.sql", "00002_repository_transactions.sql"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "db", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, strings.Split(string(b), "-- +goose Down")[0]); err != nil {
			t.Fatal(err)
		}
	}
	s, c := draftServer(t, repository.New(pool))
	draftSession(t, s, c)
	a := draftDecode(t, requestDraft(t, c, "POST", s.URL+"/v1/calls", draftJSON, 201))
	b := draftDecode(t, requestDraft(t, c, "POST", s.URL+"/v1/calls", draftJSON, 201))
	if a.ID != b.ID || a.Status != "draft" {
		t.Fatal("database retry changed draft")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM calls`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rows=%d err=%v", count, err)
	}
	jar, _ := cookiejar.New(nil)
	other := &http.Client{Jar: jar}
	draftSession(t, s, other)
	requestDraft(t, other, "GET", s.URL+"/v1/calls/"+a.ID, "", 404)
	app := strings.NewReplacer(`"restaurant"`, `"appointment"`, `"party_size":4`, `"service":"Haircut"`, `000000000001`, `000000000002`).Replace(draftJSON)
	v := draftDecode(t, requestDraft(t, c, "POST", s.URL+"/v1/calls", app, 201))
	if v.Goal.Service != "Haircut" || v.Status != "draft" {
		t.Fatal("invalid stored appointment")
	}
}
