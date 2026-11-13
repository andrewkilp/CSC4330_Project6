package control

import (
	"context"
	"errors"
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository/repositorytest"
)

// Runs against Postgres when SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL is set.
func TestIntegrationControlsCommitAtomicallyAndIdempotently(t *testing.T) {
	db := repositorytest.Open(t)
	ctx := context.Background()
	owner := repository.NewID()
	n := 4
	call, err := db.Repo.Create(ctx, owner, repository.NewID(), repository.Draft{Mode: "mock", Destination: "+12255550100",
		BusinessName: "Fictional Bistro (mock)", ApprovalMode: "always", ConsentAcknowledged: true,
		Goal: repository.Goal{Kind: "restaurant", PreferredAt: "2026-11-20T19:00:00-06:00", Timezone: "America/Chicago", PartySize: &n, Constraints: []string{}, ContactName: "Fictional Guest"}})
	if err != nil {
		t.Fatal(err)
	}
	reply := repository.NewID()
	if _, err := db.Repo.Update(ctx, owner, call.ID, "setup", repository.NewID(), "connect", func(tx *repository.Tx) (any, error) {
		for _, s := range []string{"dialing", "connected"} {
			if err := tx.SetCall(0, s, 0); err != nil {
				return nil, err
			}
		}
		return nil, tx.PutResponse(repository.Response{ID: reply, CallID: call.ID, Text: "Table for four at seven?", Status: "suggested", NeedsApproval: true})
	}); err != nil {
		t.Fatal(err)
	}
	store := Repository{Store: db.Repo}
	pauseKey := repository.NewID()
	paused, err := store.Control(ctx, owner, call.ID, pauseKey, "pause")
	if err != nil || paused.Call.Status != "paused" || len(paused.Events) != 2 {
		t.Fatalf("pause = %+v %v", paused, err)
	}
	replay, err := store.Control(ctx, owner, call.ID, pauseKey, "pause")
	if err != nil || replay.Call.Status != "paused" || len(replay.Events) != 0 {
		t.Fatalf("replay = %+v %v", replay, err)
	}
	if _, err := store.Control(ctx, owner, call.ID, pauseKey, "end"); !errors.Is(err, repository.ErrConflict) {
		t.Fatalf("reused key = %v", err)
	}
	if _, err := store.Control(ctx, repository.NewID(), call.ID, repository.NewID(), "end"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("other owner = %v", err)
	}
	if r, err := store.Control(ctx, owner, call.ID, repository.NewID(), "resume"); err != nil || r.Call.Status != "connected" {
		t.Fatalf("resume = %+v %v", r, err)
	}
	ended, err := store.Control(ctx, owner, call.ID, repository.NewID(), "end")
	if err != nil || ended.Call.Status != "ended" || len(ended.Events) != 1 {
		t.Fatalf("end = %+v %v", ended, err)
	}
	again, err := store.Control(ctx, owner, call.ID, repository.NewID(), "end")
	if err != nil || again.Call.Status != "ended" || len(again.Events) != 0 {
		t.Fatalf("repeated end = %+v %v", again, err)
	}
	if _, err := store.Control(ctx, owner, call.ID, repository.NewID(), "resume"); !errors.Is(err, ErrAction) {
		t.Fatalf("resume after end = %v", err)
	}
	snap, err := db.Repo.Snapshot(ctx, owner, call.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range snap.Responses {
		if r.ID == reply && r.Status != "cancelled" {
			t.Fatalf("pause did not cancel the suggestion: %s", r.Status)
		}
	}
	events, err := db.Repo.EventsAfter(ctx, owner, call.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	// pause: response + call; resume: call; end: call. Contiguous, end last.
	if len(events) != 4 || events[3].Type != "call.updated" {
		t.Fatalf("events = %d", len(events))
	}
	for i, e := range events {
		if e.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d", i, e.Seq)
		}
	}
}
