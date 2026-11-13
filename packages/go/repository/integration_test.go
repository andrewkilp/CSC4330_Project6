package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func database(t *testing.T) (*Repository, context.Context) {
	return databaseWithMigrations(t, []string{"00001_calls.sql", "00002_repository_transactions.sql"})
}
func databaseWithMigrations(t *testing.T, migrations []string) (*Repository, context.Context) {
	t.Helper()
	dsn := os.Getenv("SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL to a disposable *_test database for Postgres integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatal("refusing integration tests: database name must end in _test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("test database unavailable")
	}
	schema := "repository_test_" + strings.ReplaceAll(NewID(), "-", "")
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal("cannot create disposable test schema")
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
	})
	for _, name := range migrations {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "db", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		up := strings.Split(string(b), "-- +goose Down")[0]
		if _, err = pool.Exec(ctx, up); err != nil {
			t.Fatalf("migration %s: %v", name, err)
		}
	}
	return New(pool), ctx
}

func create(t *testing.T, r *Repository, ctx context.Context, owner string) Call {
	t.Helper()
	c, err := r.Create(ctx, owner, NewID(), draft())
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func update(t *testing.T, r *Repository, ctx context.Context, owner, id string, fn func(*Tx) (any, error)) json.RawMessage {
	t.Helper()
	b, err := r.Update(ctx, owner, id, "test", NewID(), map[string]any{}, fn)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestIntegrationOwnerFencesEveryBoundary(t *testing.T) {
	r, ctx := database(t)
	owner, other := NewID(), NewID()
	c := create(t, r, ctx, owner)
	if _, err := r.GetCall(ctx, other, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("crossowner GetCall=%v", err)
	}
	if _, err := r.Snapshot(ctx, other, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("crossowner Snapshot=%v", err)
	}
	if _, err := r.EventsAfter(ctx, other, c.ID, 0, 100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("crossowner events=%v", err)
	}
	called := false
	if _, err := r.Update(ctx, other, c.ID, "write", NewID(), map[string]any{}, func(*Tx) (any, error) { called = true; return map[string]any{}, nil }); !errors.Is(err, ErrNotFound) || called {
		t.Fatalf("crossowner mutation=%v called=%v", err, called)
	}
	if err := r.DeleteCall(ctx, other, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("crossowner delete=%v", err)
	}
	if err := r.DeleteCall(ctx, owner, c.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("live call deleted=%v", err)
	}
	// A child ID belonging to a different call cannot be rewritten through an owned Tx.
	foreign := create(t, r, ctx, other)
	response := Response{ID: NewID(), CallID: foreign.ID, Status: "suggested", Text: "Fictional reply", NeedsApproval: true}
	update(t, r, ctx, other, foreign.ID, func(tx *Tx) (any, error) { return map[string]any{}, tx.PutResponse(response) })
	_, err := r.Update(ctx, owner, c.ID, "overwrite", NewID(), map[string]any{}, func(tx *Tx) (any, error) { response.CallID = c.ID; return map[string]any{}, tx.PutResponse(response) })
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign child overwritten: %v", err)
	}
	_, err = r.Update(ctx, owner, c.ID, "readforeign", NewID(), map[string]any{}, func(tx *Tx) (any, error) { _, err := tx.Response(response.ID); return map[string]any{}, err })
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign child read: %v", err)
	}
}

func TestIntegrationConcurrentSequencesAndIdempotentRetry(t *testing.T) {
	r, ctx := database(t)
	owner := NewID()
	c := create(t, r, ctx, owner)
	const workers = 24
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	start := make(chan struct{})
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := r.Update(ctx, owner, c.ID, "append", NewID(), map[string]any{"i": i}, func(tx *Tx) (any, error) {
				return tx.AppendEvent("usage.updated", map[string]any{"elapsed_seconds": i, "estimated_cost_usd": 0, "estimate_only": true})
			})
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	events, err := r.EventsAfter(ctx, owner, c.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != workers+1 {
		t.Fatalf("events=%d", len(events))
	}
	for i, e := range events {
		if e.Seq != int64(i+1) {
			t.Fatal("duplicate or missing sequence")
		}
	}
	key := NewID()
	var callbacks atomic.Int32
	errs = make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.Update(ctx, owner, c.ID, "same-key", key, json.RawMessage(`{"text":"same"}`), func(tx *Tx) (any, error) {
				callbacks.Add(1)
				return tx.AppendEvent("usage.updated", map[string]any{"elapsed_seconds": 42, "estimated_cost_usd": 0, "estimate_only": true})
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if callbacks.Load() != 1 {
		t.Fatalf("idempotent callbacks=%d", callbacks.Load())
	}
	events, _ = r.EventsAfter(ctx, owner, c.ID, 0, 100)
	if len(events) != workers+2 {
		t.Fatalf("retry duplicated events=%d", len(events))
	}
	_, err = r.Update(ctx, owner, c.ID, "same-key", key, map[string]any{"text": "different"}, func(*Tx) (any, error) { t.Error("conflicting retry invoked callback"); return nil, nil })
	if !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

func TestIntegrationCreateRetryAndOriginalResult(t *testing.T) {
	r, ctx := database(t)
	owner, key := NewID(), NewID()
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	errs := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() { defer wg.Done(); c, err := r.Create(ctx, owner, key, draft()); ids <- c.ID; errs <- err }()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for got := range ids {
		if id != "" && got != id {
			t.Fatal("retried creation allocated another call")
		}
		id = got
	}
	var count int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM calls`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("calls=%d error=%v", count, err)
	}
	update(t, r, ctx, owner, id, func(tx *Tx) (any, error) { return map[string]any{}, tx.SetCall(0, "connected", 0) })
	again, err := r.Create(ctx, owner, key, draft())
	if err != nil || again.ID != id || again.Status != "draft" {
		t.Fatalf("create replay drifted: %+v %v", again, err)
	}
	d := draft()
	d.BusinessName = "Different (mock)"
	if _, err := r.Create(ctx, owner, key, d); !errors.Is(err, ErrConflict) {
		t.Fatalf("create key conflict=%v", err)
	}
}

func TestIntegrationCRUDRollbackAndFinalImmutability(t *testing.T) {
	r, ctx := database(t)
	owner := NewID()
	c := create(t, r, ctx, owner)
	response := Response{ID: NewID(), CallID: c.ID, Status: "suggested", Text: "Fictional suggestion", NeedsApproval: true}
	turn := Turn{ID: NewID(), CallID: c.ID, Speaker: "business", Text: "Fictional partial", StartedAtMS: 0}
	instruction := Instruction{ID: NewID(), CallID: c.ID, Text: "Choose a later time", Status: "applied", ContextRevision: 1}
	var escaped *Tx
	update(t, r, ctx, owner, c.ID, func(tx *Tx) (any, error) {
		escaped = tx
		if err := tx.SetCall(0, "connected", 1); err != nil {
			return nil, err
		}
		if err := tx.PutResponse(response); err != nil {
			return nil, err
		}
		if err := tx.PutTurn(turn); err != nil {
			return nil, err
		}
		if err := tx.PutInstruction(instruction); err != nil {
			return nil, err
		}
		if _, err := tx.AppendEvent("instruction.updated", instruction); err != nil {
			return nil, err
		}
		return tx.Call(), nil
	})
	if err := escaped.PutTurn(turn); !errors.Is(err, ErrConflict) {
		t.Fatal("escaped transaction reused")
	}
	snapshot, err := r.Snapshot(ctx, owner, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Turns) != 1 || len(snapshot.Responses) != 1 || len(snapshot.Instructions) != 1 || snapshot.LastSeq != 2 {
		t.Fatalf("incomplete snapshot: %+v", snapshot)
	}
	update(t, r, ctx, owner, c.ID, func(tx *Tx) (any, error) {
		response.Status = "cancelled"
		if err := tx.PutResponse(response); err != nil {
			return nil, err
		}
		if got, err := tx.Response(response.ID); err != nil || got.Status != "cancelled" {
			t.Fatalf("response update not stored: %+v %v", got, err)
		}
		for _, check := range []func() error{
			func() error {
				v, err := tx.Turns()
				if err == nil && len(v) != 1 {
					t.Fatal("turn collection missing row")
				}
				return err
			},
			func() error {
				v, err := tx.Responses()
				if err == nil && len(v) != 1 {
					t.Fatal("response collection missing row")
				}
				return err
			},
			func() error {
				v, err := tx.Instructions()
				if err == nil && len(v) != 1 {
					t.Fatal("instruction collection missing row")
				}
				return err
			},
		} {
			if err := check(); err != nil {
				return nil, err
			}
		}
		return response, nil
	})
	end := int64(1000)
	turn.IsFinal = true
	turn.EndedAtMS = &end
	turn.Text = "Fictional final"
	update(t, r, ctx, owner, c.ID, func(tx *Tx) (any, error) {
		if err := tx.PutTurn(turn); err != nil {
			return nil, err
		}
		return tx.Turn(turn.ID)
	})
	_, err = r.Update(ctx, owner, c.ID, "finalrewrite", NewID(), map[string]any{}, func(tx *Tx) (any, error) { turn.Text = "Changed final"; return map[string]any{}, tx.PutTurn(turn) })
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("final turn changed=%v", err)
	}
	before, _ := r.Snapshot(ctx, owner, c.ID)
	key := NewID()
	_, err = r.Update(ctx, owner, c.ID, "rollback", key, map[string]any{}, func(tx *Tx) (any, error) {
		if _, err := tx.AppendEvent("usage.updated", map[string]any{"estimate_only": true}); err != nil {
			return nil, err
		}
		if err := tx.SetCall(1, "ended", 2); err != nil {
			return nil, err
		}
		return nil, ErrConflict
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	after, _ := r.Snapshot(ctx, owner, c.ID)
	if after.LastSeq != before.LastSeq || after.Call.ContextRevision != before.Call.ContextRevision || after.Call.Status != before.Call.Status {
		t.Fatal("rollback left partial state")
	}
	// Reusing a rolled-back key must execute the new successful write exactly once.
	_, err = r.Update(ctx, owner, c.ID, "rollback", key, map[string]any{}, func(tx *Tx) (any, error) {
		return tx.AppendEvent("usage.updated", map[string]any{"estimate_only": true})
	})
	if err != nil {
		t.Fatal(err)
	}
	update(t, r, ctx, owner, c.ID, func(tx *Tx) (any, error) {
		if _, err := tx.Response(response.ID); err != nil {
			return nil, err
		}
		if _, err := tx.Instruction(instruction.ID); err != nil {
			return nil, err
		}
		if err := tx.DeleteTurn(turn.ID); err != nil {
			return nil, err
		}
		if err := tx.DeleteResponse(response.ID); err != nil {
			return nil, err
		}
		if err := tx.DeleteInstruction(instruction.ID); err != nil {
			return nil, err
		}
		g := draft().Goal
		g.Constraints = []string{"after seven"}
		if err := tx.PutGoal(2, g); err != nil {
			return nil, err
		}
		if err := tx.SetCall(1, "ended", 2); err != nil {
			return nil, err
		}
		if err := tx.DeleteGoal(0); err != nil {
			return nil, err
		}
		return tx.Call(), nil
	})
	if err := r.DeleteCall(ctx, owner, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.GetCall(ctx, owner, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	for _, table := range []string{"call_goals", "call_turns", "call_responses", "call_instructions", "call_events", "call_idempotency_keys"} {
		var n int
		if err := r.pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&n); err != nil || n != 0 {
			t.Fatalf("cascade %s=%d error=%v", table, n, err)
		}
	}
}

func TestIntegrationDeferredForeignKeyRollsBackWholeMutation(t *testing.T) {
	r, ctx := database(t)
	owner := NewID()
	c := create(t, r, ctx, owner)
	key := NewID()
	end := int64(100)
	_, err := r.Update(ctx, owner, c.ID, "bad-fk", key, map[string]any{}, func(tx *Tx) (any, error) {
		if err := tx.PutTurn(Turn{ID: NewID(), CallID: c.ID, Speaker: "assistant", Text: "Fictional speech", IsFinal: true, StartedAtMS: 0, EndedAtMS: &end, ResponseID: NewID()}); err != nil {
			return nil, err
		}
		return tx.AppendEvent("usage.updated", map[string]any{"estimate_only": true})
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("deferred FK=%v", err)
	}
	s, err := r.Snapshot(ctx, owner, c.ID)
	if err != nil || len(s.Turns) != 0 || s.LastSeq != 1 {
		t.Fatalf("commit failure left state: %+v %v", s, err)
	}
	_, err = r.Update(ctx, owner, c.ID, "bad-fk", key, map[string]any{}, func(tx *Tx) (any, error) {
		return tx.AppendEvent("usage.updated", map[string]any{"estimate_only": true})
	})
	if err != nil {
		t.Fatalf("commit failure retained idempotency key: %v", err)
	}
}

func TestIntegrationGoalCacheTracksEitherMutationOrdering(t *testing.T) {
	r, ctx := database(t)
	owner := NewID()
	c := create(t, r, ctx, owner)
	update(t, r, ctx, owner, c.ID, func(tx *Tx) (any, error) {
		if err := tx.SetCall(0, "connected", 1); err != nil {
			return nil, err
		}
		g := draft().Goal
		g.Constraints = []string{"after eight"}
		if err := tx.PutGoal(1, g); err != nil {
			return nil, err
		}
		if got := tx.Call().Goal.Constraints; len(got) != 1 || got[0] != "after eight" {
			t.Fatal("SetCall then PutGoal left stale cached goal")
		}
		return tx.AppendEvent("call.updated", tx.Call())
	})
	update(t, r, ctx, owner, c.ID, func(tx *Tx) (any, error) {
		g := draft().Goal
		g.Constraints = []string{"after nine"}
		if err := tx.PutGoal(2, g); err != nil {
			return nil, err
		}
		if err := tx.SetCall(1, "connected", 2); err != nil {
			return nil, err
		}
		if got := tx.Call().Goal.Constraints; len(got) != 1 || got[0] != "after nine" {
			t.Fatal("PutGoal then SetCall left stale cached goal")
		}
		return tx.AppendEvent("call.updated", tx.Call())
	})
	events, err := r.EventsAfter(ctx, owner, c.ID, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range events {
		var call Call
		if err := json.Unmarshal(e.Payload, &call); err != nil {
			t.Fatal(err)
		}
		want := []string{"after eight", "after nine"}[i]
		if len(call.Goal.Constraints) != 1 || call.Goal.Constraints[0] != want {
			t.Fatal("call.updated disagrees with transactional goal")
		}
	}
}

func TestIntegrationCounterBackfillAndMigrationDown(t *testing.T) {
	r, ctx := databaseWithMigrations(t, []string{"00001_calls.sql"})
	owner, id := NewID(), NewID()
	if _, err := r.pool.Exec(ctx, `INSERT INTO session_owners(id) VALUES($1)`, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO calls(id,owner_id,mode,destination,business_name,consent_acknowledged) VALUES($1,$2,'mock','+12255550100','Fictional Bistro (mock)',true)`, id, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO call_events(id,call_id,seq,type,context_revision,payload) VALUES($1,$2,7,'usage.updated',0,'{"estimate_only":true}')`, NewID(), id); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "db", "migrations", "00002_repository_transactions.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.pool.Exec(ctx, strings.Split(string(b), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	var seq int64
	if err = r.pool.QueryRow(ctx, `SELECT last_event_seq FROM calls WHERE id=$1`, id).Scan(&seq); err != nil || seq != 7 {
		t.Fatalf("backfill=%d %v", seq, err)
	}
	for _, name := range []string{"00002_repository_transactions.sql", "00001_calls.sql"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "db", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = r.pool.Exec(ctx, strings.Split(string(b), "-- +goose Down")[1]); err != nil {
			t.Fatalf("down %s: %v", name, err)
		}
	}
}

func TestIntegrationAppointmentGoalAndImmutableSnapshots(t *testing.T) {
	r, ctx := database(t)
	owner := NewID()
	d := draft()
	d.Goal.Kind = "appointment"
	d.Goal.PartySize = nil
	d.Goal.Service = "Fictional haircut"
	d.Goal.Timezone = "UTC"
	d.Goal.PreferredAt = "2026-11-20T19:00:00Z"
	d.Goal.Constraints = nil
	c, err := r.Create(ctx, owner, NewID(), d)
	if err != nil {
		t.Fatal(err)
	}
	if c.Goal.Service != d.Goal.Service || c.Goal.PartySize != nil || c.Goal.Constraints == nil || c.Goal.PreferredAt != d.Goal.PreferredAt {
		t.Fatalf("appointment goal changed: %+v", c.Goal)
	}
	update(t, r, ctx, owner, c.ID, func(tx *Tx) (any, error) { return tx.Call(), tx.PutGoal(0, d.Goal) })
	_, err = r.Update(ctx, owner, c.ID, "change-goal", NewID(), map[string]any{}, func(tx *Tx) (any, error) {
		g := d.Goal
		g.Service = "Changed immutable service"
		return map[string]any{}, tx.PutGoal(0, g)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("goal snapshot rewritten=%v", err)
	}
	_, err = r.Update(ctx, owner, c.ID, "stale-revision", NewID(), map[string]any{}, func(tx *Tx) (any, error) { return map[string]any{}, tx.SetCall(1, "connected", 1) })
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision accepted=%v", err)
	}
	d.ConsentAcknowledged = false
	if _, err = r.Create(ctx, owner, NewID(), d); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing consent accepted")
	}
}
