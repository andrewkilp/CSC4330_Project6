package telephony

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/mock"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"github.com/jackc/pgx/v5/pgxpool"
)

func database(t *testing.T) (*repository.Repository, *pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL for disposable Postgres integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("test database must end in _test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("test database unavailable")
	}
	schema := "start_test_" + strings.ReplaceAll(repository.NewID(), "-", "")
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(ctx, `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	for _, name := range []string{"00001_calls.sql", "00002_repository_transactions.sql", "00003_call_start_intents.sql"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "db", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, strings.Split(string(b), "-- +goose Down")[0]); err != nil {
			t.Fatal(err)
		}
	}
	return repository.New(pool), pool, ctx
}
func draft(mode string) repository.Draft {
	n := 4
	return repository.Draft{Mode: mode, Destination: "+12255550100", BusinessName: "Fictional Bistro (mock)", ApprovalMode: "always", ConsentAcknowledged: true, Goal: repository.Goal{Kind: "restaurant", PreferredAt: "2026-11-20T19:00:00-06:00", Timezone: "America/Chicago", PartySize: &n, ContactName: "Fictional Guest", Constraints: []string{}}}
}
func mockProvider(t *testing.T) providers.Telephony {
	t.Helper()
	p, err := mock.NewProviders(mock.RestaurantTimeConflict, mock.DefaultSeed, mock.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return p.Telephony
}
func service(t *testing.T, store StartStore, client providers.Telephony) *StartService {
	t.Helper()
	s, err := NewStartService(store, client, mockProvider(t), StartConfig{EnableReal: true, AllowedNumbers: []string{"+12255550100"}, PublicBaseURL: "https://demo.example", AccountSID: testAccount})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestIntegrationConcurrentStartsOneHTTPDialAcrossKeysAndRestart(t *testing.T) {
	r, pool, ctx := database(t)
	owner, key := repository.NewID(), repository.NewID()
	call, err := r.Create(ctx, owner, repository.NewID(), draft("real"))
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	c := localClient(t, func(w http.ResponseWriter, req *http.Request) {
		requests.Add(1)
		var committed int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM call_starts WHERE call_id=$1 AND state='reserved'`, call.ID).Scan(&committed); err != nil || committed != 1 {
			t.Errorf("provider contacted before durable intent commit: count=%d err=%v", committed, err)
		}
		w.WriteHeader(201)
		_, _ = w.Write(fixture(t))
	})
	s := service(t, r, c)
	const workers = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	goStart := make(chan struct{})
	for range workers {
		wg.Add(1)
		go func() { defer wg.Done(); <-goStart; _, err := s.Start(ctx, owner, call.ID, key); errs <- err }()
	}
	close(goStart)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, repository.ErrConflict) {
			t.Fatal(err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("provider dials=%d", requests.Load())
	}
	newPool, err := pgxpool.NewWithConfig(ctx, pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer newPool.Close()
	restarted := service(t, repository.New(newPool), c)
	if _, err = restarted.Start(ctx, owner, call.ID, key); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.Start(ctx, owner, call.ID, repository.NewID()); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("alternate key redial allowed")
	}
	if requests.Load() != 1 {
		t.Fatal("restart redialed")
	}
}

func TestIntegrationDifferentKeysRacingStillDialOnlyOnce(t *testing.T) {
	r, _, ctx := database(t)
	owner := repository.NewID()
	call, err := r.Create(ctx, owner, repository.NewID(), draft("real"))
	if err != nil {
		t.Fatal(err)
	}
	var requests, successes atomic.Int32
	c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(201)
		_, _ = w.Write(fixture(t))
	})
	s := service(t, r, c)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	start := make(chan struct{})
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.Start(ctx, owner, call.ID, repository.NewID())
			if err == nil {
				successes.Add(1)
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, repository.ErrConflict) {
			t.Fatal(err)
		}
	}
	if requests.Load() != 1 || successes.Load() != 1 {
		t.Fatalf("distinct-key race requests=%d successes=%d", requests.Load(), successes.Load())
	}
}

func TestIntegrationCallbackCanBeatRESTAndConflictingSIDFailsClosed(t *testing.T) {
	r, _, ctx := database(t)
	owner := repository.NewID()
	call, err := r.Create(ctx, owner, repository.NewID(), draft("real"))
	if err != nil {
		t.Fatal(err)
	}
	c := localClient(t, func(w http.ResponseWriter, req *http.Request) {
		if err := req.ParseForm(); err != nil {
			t.Error(err)
		}
		u, err := url.Parse(req.Form.Get("StatusCallback"))
		if err != nil {
			t.Error(err)
		}
		q := u.Query()
		out, err := r.ApplyTwilioCallback(ctx, repository.TwilioCallback{CallID: q.Get("call_id"), AttemptID: q.Get("attempt_id"), Token: q.Get("token"), AccountSID: testAccount, CallSID: testCallSID, Status: "in-progress"})
		if err != nil || out.Call.Status != "connected" {
			t.Errorf("early callback=%+v %v", out, err)
		}
		w.WriteHeader(201)
		_, _ = w.Write(fixture(t))
	})
	s := service(t, r, c)
	out, err := s.Start(ctx, owner, call.ID, repository.NewID())
	if err != nil || out.Status != "connected" {
		t.Fatalf("callback race=%+v %v", out, err)
	}
}

func TestIntegrationAmbiguousHTTPAndPostDialDatabaseFailureNeverRetry(t *testing.T) {
	for _, dbFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP500", true: "acknowledgement lost"}[dbFailure], func(t *testing.T) {
			r, _, ctx := database(t)
			owner, key := repository.NewID(), repository.NewID()
			call, err := r.Create(ctx, owner, repository.NewID(), draft("real"))
			if err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			c := localClient(t, func(w http.ResponseWriter, req *http.Request) {
				requests.Add(1)
				if !dbFailure {
					w.WriteHeader(500)
					_, _ = w.Write([]byte("private provider error"))
					return
				}
				w.WriteHeader(201)
				_, _ = w.Write(fixture(t))
			})
			var store StartStore = r
			if dbFailure {
				store = &confirmFailure{StartStore: r}
			}
			s := service(t, store, c)
			if _, err = s.Start(ctx, owner, call.ID, key); !errors.Is(err, repository.ErrUnavailable) {
				t.Fatalf("ambiguous start=%v", err)
			}
			for _, retryKey := range []string{key, repository.NewID()} {
				if _, err = service(t, r, c).Start(ctx, owner, call.ID, retryKey); !errors.Is(err, repository.ErrConflict) {
					t.Fatalf("ambiguous retry=%v", err)
				}
			}
			if requests.Load() != 1 {
				t.Fatalf("ambiguous dial repeated %d times", requests.Load())
			}
		})
	}
}

type confirmFailure struct{ StartStore }

func (c *confirmFailure) ConfirmCallStart(context.Context, string, string, string, string) (repository.Call, error) {
	return repository.Call{}, repository.ErrUnavailable
}

func TestIntegrationMockModeNeverTouchesRealAdapterAndGateBlocks(t *testing.T) {
	r, _, ctx := database(t)
	owner := repository.NewID()
	var realRequests atomic.Int32
	c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		realRequests.Add(1)
		t.Error("unexpected real provider call")
	})
	s := service(t, r, c)
	mockCall, err := r.Create(ctx, owner, repository.NewID(), draft("mock"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(ctx, owner, mockCall.ID, repository.NewID()); err != nil {
		t.Fatal(err)
	}
	realCall, err := r.Create(ctx, owner, repository.NewID(), draft("real"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(ctx, repository.NewID(), realCall.ID, repository.NewID()); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal(err)
	}
	disabled, err := NewStartService(r, nil, mockProvider(t), StartConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = disabled.Start(ctx, owner, realCall.ID, repository.NewID()); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatal(err)
	}
	blocked, err := NewStartService(r, c, mockProvider(t), StartConfig{EnableReal: true, AllowedNumbers: []string{"+12255550102"}, PublicBaseURL: "https://demo.example", AccountSID: testAccount})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = blocked.Start(ctx, owner, realCall.ID, repository.NewID()); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatal(err)
	}
	if realRequests.Load() != 0 {
		t.Fatal("gate invoked real adapter")
	}
}
