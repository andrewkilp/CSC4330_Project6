package calls_test

import (
	"context"
	"encoding/json"
	"regexp"

	"strings"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/calls"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/events"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository/repositorytest"
)

func newService(t *testing.T) (*calls.Service, *repository.Repository) {
	t.Helper()
	store := repositorytest.Open(t).Repo
	svc, err := calls.New(calls.Config{Repo: store, Hub: events.NewHub(0)})
	if err != nil {
		t.Fatal(err)
	}
	return svc, store
}

func scenarioBody(t *testing.T, svc *calls.Service, name string) []byte {
	t.Helper()
	list, err := svc.Scenarios()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.Name == name {
			body, _ := json.Marshal(map[string]any{"mode": "mock", "destination": s.Destination, "business_name": s.BusinessName,
				"goal": s.Goal, "approval_mode": s.ApprovalMode, "consent_acknowledged": true, "idempotency_key": repository.NewID()})
			return body
		}
	}
	t.Fatalf("no scenario %s", name)
	return nil
}

func key(t *testing.T) string { return repository.NewID() }

func action(t *testing.T, parse func([]byte) (calls.Action, *calls.Error), fields map[string]any) calls.Action {
	t.Helper()
	fields["idempotency_key"] = key(t)
	body, _ := json.Marshal(fields)
	a, err := parse(body)
	if err != nil {
		t.Fatalf("parse %v: %+v", fields, err)
	}
	return a
}

// waitFor polls the durable snapshot until cond holds.
func waitFor(t *testing.T, store *repository.Repository, owner, id, what string, cond func(repository.Snapshot) bool) repository.Snapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		snap, err := store.Snapshot(context.Background(), owner, id)
		if err != nil {
			t.Fatal(err)
		}
		if cond(snap) {
			return snap
		}
		if time.Now().After(deadline) {
			data, _ := json.MarshalIndent(snap, "", " ")
			t.Fatalf("timed out waiting for %s:\n%s", what, data)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func current(snap repository.Snapshot, status string) *repository.Response {
	for i := len(snap.Responses) - 1; i >= 0; i-- {
		r := snap.Responses[i]
		if r.Status == status && r.ContextRevision == snap.Call.ContextRevision {
			return &r
		}
	}
	return nil
}

func spokenCount(snap repository.Snapshot) int {
	n := 0
	for _, turn := range snap.Turns {
		if turn.Speaker == "assistant" {
			n++
		}
	}
	return n
}

func finalBusiness(snap repository.Snapshot) []string {
	var out []string
	for _, turn := range snap.Turns {
		if turn.Speaker == "business" && turn.IsFinal {
			out = append(out, turn.Text)
		}
	}
	return out
}

func TestRestaurantMockCallEndToEnd(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()
	owner := repository.NewID()
	req, perr := calls.ParseCreate(scenarioBody(t, svc, "restaurant-time-conflict"))
	if perr != nil {
		t.Fatal(perr)
	}
	call, cerr := svc.Create(ctx, owner, req)
	if cerr != nil || call.Status != "draft" {
		t.Fatalf("create = %+v %v", call, cerr)
	}
	// Creating never dials.
	time.Sleep(50 * time.Millisecond)
	if c, _ := svc.Get(ctx, owner, call.ID); c.Status != "draft" {
		t.Fatal("create started the call")
	}
	if _, err := svc.Start(ctx, repository.NewID(), call.ID, action(t, calls.ParseStart, map[string]any{})); err == nil || err.Status != 404 {
		t.Fatalf("another owner started the call: %v", err)
	}
	start := action(t, calls.ParseStart, map[string]any{})
	if c, err := svc.Start(ctx, owner, call.ID, start); err != nil || c.Status != "dialing" {
		t.Fatalf("start = %+v %v", c, err)
	}
	if c, err := svc.Start(ctx, owner, call.ID, start); err != nil || c.Status != "dialing" {
		t.Fatalf("start retry = %+v %v", c, err)
	}

	approve := func(r *repository.Response, rev int64) {
		t.Helper()
		a := action(t, calls.ParseApproval, map[string]any{"context_revision": rev})
		if got, err := svc.Approve(ctx, owner, call.ID, r.ID, a); err != nil || got.Status != "approved" {
			t.Fatalf("approve = %+v %v", got, err)
		}
	}

	// 1. Greeting -> suggestion -> approve -> spoken.
	snap := waitFor(t, store, owner, call.ID, "first suggestion", func(s repository.Snapshot) bool { return current(s, "suggested") != nil })
	first := current(snap, "suggested")
	if !strings.Contains(first.Text, "AI assistant") || snap.Call.Status != "connected" {
		t.Fatalf("first suggestion = %+v", first)
	}
	for _, turn := range snap.Turns {
		if turn.Speaker == "assistant" {
			t.Fatal("suggestion entered the transcript before approval and playback")
		}
	}
	approve(first, 0)
	snap = waitFor(t, store, owner, call.ID, "first reply spoken and next question", func(s repository.Snapshot) bool {
		return spokenCount(s) == 1 && current(s, "suggested") != nil
	})
	if got := finalBusiness(snap); len(got) != 2 || !strings.Contains(got[1], "8:45") {
		t.Fatalf("business turns = %v", got)
	}
	hold := current(snap, "suggested")
	if !hold.NeedsApproval || hold.Reason == "" {
		t.Fatalf("uncertain reply should need approval with a reason: %+v", hold)
	}

	// 2. Private instruction -> revision 1 -> stale approval rejected -> revised reply.
	stale := action(t, calls.ParseInstruction, map[string]any{"text": "8:45 is fine. Do not accept 6:15.", "expected_revision": 1})
	if _, err := svc.Instruct(ctx, owner, call.ID, stale); err == nil || err.Field != "expected_revision" {
		t.Fatalf("stale instruction = %v", err)
	}
	ins := action(t, calls.ParseInstruction, map[string]any{"text": "8:45 is fine. Do not accept 6:15.", "expected_revision": 0})
	in, ierr := svc.Instruct(ctx, owner, call.ID, ins)
	if ierr != nil || in.ContextRevision != 1 || in.Status != "applied" || in.CreatedAt.Location() != time.UTC {
		t.Fatalf("instruct = %+v %v", in, ierr)
	}
	if again, err := svc.Instruct(ctx, owner, call.ID, ins); err != nil || again.ID != in.ID {
		t.Fatalf("instruction retry = %+v %v", again, err)
	}
	if _, err := svc.Approve(ctx, owner, call.ID, hold.ID, action(t, calls.ParseApproval, map[string]any{"context_revision": 0})); err == nil || err.Status != 409 {
		t.Fatalf("stale approval = %v", err)
	}
	snap = waitFor(t, store, owner, call.ID, "revised suggestion", func(s repository.Snapshot) bool {
		r := current(s, "suggested")
		return r != nil && r.ContextRevision == 1
	})
	revised := current(snap, "suggested")
	if !strings.Contains(revised.Text, "8:45 works") {
		t.Fatalf("revised = %+v", revised)
	}
	edited := "8:45 works. Please book an indoor table for four at 8:45 under Jordan Lee."
	a := action(t, calls.ParseApproval, map[string]any{"context_revision": 1, "edited_text": edited})
	if got, err := svc.Approve(ctx, owner, call.ID, revised.ID, a); err != nil || got.Text != edited {
		t.Fatalf("edited approval = %+v %v", got, err)
	}

	// 3. Pause during the next suggestion, resume, approve, and the booking arrives.
	snap = waitFor(t, store, owner, call.ID, "confirmation question", func(s repository.Snapshot) bool {
		return spokenCount(s) == 2 && current(s, "suggested") != nil
	})
	if got := finalBusiness(snap); !strings.Contains(got[len(got)-1], "BLB-2048") {
		t.Fatalf("business = %v", got)
	}
	pause := action(t, calls.ParseControl, map[string]any{"action": "pause"})
	if c, err := svc.Control(ctx, owner, call.ID, pause); err != nil || c.Status != "paused" {
		t.Fatalf("pause = %+v %v", c, err)
	}
	snap = waitFor(t, store, owner, call.ID, "suggestion cancelled by pause", func(s repository.Snapshot) bool { return current(s, "suggested") == nil })
	if _, err := svc.Control(ctx, owner, call.ID, action(t, calls.ParseControl, map[string]any{"action": "pause"})); err == nil || err.Field != "action" {
		t.Fatalf("double pause = %v", err)
	}
	if c, err := svc.Control(ctx, owner, call.ID, action(t, calls.ParseControl, map[string]any{"action": "resume"})); err != nil || c.Status != "connected" {
		t.Fatalf("resume = %+v %v", c, err)
	}
	snap = waitFor(t, store, owner, call.ID, "regenerated deferral", func(s repository.Snapshot) bool { return current(s, "suggested") != nil })
	approve(current(snap, "suggested"), 1)
	snap = waitFor(t, store, owner, call.ID, "goodbye", func(s repository.Snapshot) bool {
		return spokenCount(s) == 3 && len(finalBusiness(s)) == 4
	})

	// 4. End: terminal state and an evidence-backed summary.
	end := action(t, calls.ParseControl, map[string]any{"action": "end"})
	if c, err := svc.Control(ctx, owner, call.ID, end); err != nil || c.Status != "ended" {
		t.Fatalf("end = %+v %v", c, err)
	}
	if c, err := svc.Control(ctx, owner, call.ID, end); err != nil || c.Status != "ended" {
		t.Fatalf("end retry = %+v %v", c, err)
	}
	var raw json.RawMessage
	deadline := time.Now().Add(10 * time.Second)
	for {
		var serr *calls.Error
		if raw, serr = svc.Summary(ctx, owner, call.ID); serr == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no summary: %+v", serr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	snap, _ = store.Snapshot(ctx, owner, call.ID)
	var summary struct {
		Outcome          string   `json:"outcome"`
		EvidenceTurnIDs  []string `json:"evidence_turn_ids"`
		ConfirmedDetails struct {
			ConfirmationReference string `json:"confirmation_reference"`
		} `json:"confirmed_details"`
	}
	_ = json.Unmarshal(raw, &summary)
	if summary.Outcome != "confirmed" || summary.ConfirmedDetails.ConfirmationReference != "BLB-2048" || len(summary.EvidenceTurnIDs) != 1 {
		t.Fatalf("summary = %s", raw)
	}
	found := false
	for _, turn := range snap.Turns {
		if turn.ID == summary.EvidenceTurnIDs[0] {
			found = turn.Speaker == "business" && turn.IsFinal && strings.Contains(turn.Text, "BLB-2048")
		}
	}
	if !found {
		t.Fatal("summary evidence is not this call's finalized business turn")
	}

	// The durable log is contiguous and every event's envelope matches its payload's
	// revision, as the client reducer requires.
	var evs []repository.Event
	for after := int64(0); ; {
		page, err := store.EventsAfter(ctx, owner, call.ID, after, 100)
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, page...)
		if len(page) < 100 {
			break
		}
		after = page[len(page)-1].Seq
	}
	last := int64(len(evs))
	if snap.LastSeq != last {
		t.Fatalf("events %d, last seq %d", last, snap.LastSeq)
	}
	stamp := regexp.MustCompile(`"(\w+_at)":"([^"]*)"`)
	for i, e := range evs {
		if e.Seq != int64(i+1) {
			t.Fatalf("seq gap at %d", i)
		}
		// Server metadata timestamps in payloads must be UTC with Z (docs/contracts.md).
		// The envelope's occurred_at is normalized by the event stream (#23).
		wire := e.Payload
		for _, m := range stamp.FindAllStringSubmatch(string(wire), -1) {
			if m[1] != "preferred_at" && !strings.HasSuffix(m[2], "Z") {
				t.Fatalf("%s %s = %q is not UTC", e.Type, m[1], m[2])
			}
		}
		var p struct {
			ContextRevision *int64 `json:"context_revision"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		if p.ContextRevision != nil && *p.ContextRevision != e.ContextRevision {
			t.Fatalf("%s payload revision %d in envelope %d", e.Type, *p.ContextRevision, e.ContextRevision)
		}
	}
	// Nothing is written after the end.
	time.Sleep(100 * time.Millisecond)
	if later, _ := store.EventsAfter(ctx, owner, call.ID, last, 10); len(later) != 0 {
		t.Fatalf("events after end: %d", len(later))
	}
}

func TestCreateValidation(t *testing.T) {
	svc, _ := newService(t)
	base := map[string]any{}
	_ = json.Unmarshal(scenarioBody(t, svc, "appointment-missing-detail"), &base)
	cases := map[string]func(map[string]any){
		"destination":          func(m map[string]any) { m["destination"] = "+12255551234" },
		"consent_acknowledged": func(m map[string]any) { m["consent_acknowledged"] = "true" },
		"goal.party_size":      func(m map[string]any) { m["goal"].(map[string]any)["party_size"] = 2 },
		"goal.timezone":        func(m map[string]any) { m["goal"].(map[string]any)["timezone"] = "Mars/Base" },
		"goal.preferred_at":    func(m map[string]any) { m["goal"].(map[string]any)["preferred_at"] = "2026-11-17 10:00" },
		"owner_id":             func(m map[string]any) { m["owner_id"] = repository.NewID() },
		"business_name":        func(m map[string]any) { m["business_name"] = strings.Repeat("😀", 61) },
	}
	for field, mutate := range cases {
		m := map[string]any{}
		data, _ := json.Marshal(base)
		_ = json.Unmarshal(data, &m)
		mutate(m)
		body, _ := json.Marshal(m)
		if _, err := calls.ParseCreate(body); err == nil || err.Field != field {
			t.Errorf("%s: %+v", field, err)
		}
	}
	m := map[string]any{}
	data, _ := json.Marshal(base)
	_ = json.Unmarshal(data, &m)
	m["mode"] = "real"
	m["destination"] = "+12255551234"
	body, _ := json.Marshal(m)
	req, perr := calls.ParseCreate(body)
	if perr != nil {
		t.Fatal(perr)
	}
	if _, err := svc.Create(context.Background(), repository.NewID(), req); err == nil || err.Code != "provider_unavailable" {
		t.Fatalf("real create without providers = %+v", err)
	}
}
