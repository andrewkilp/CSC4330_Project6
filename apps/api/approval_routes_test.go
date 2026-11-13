package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/conversation"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type approvalFixture struct {
	repo     *repository.Repository
	ctx      context.Context
	router   http.Handler
	call     repository.Call
	response repository.Response
	cookie   *http.Cookie
}

func TestApprovalRoutesPreserveOriginAndMissingStorageBehavior(t *testing.T) {
	t.Setenv("ENABLE_REAL_CALLS", "false")
	for _, origin := range []string{pipelineAppOrigin, "http://192.0.2.10:8081", ""} {
		h, err := configuredRouter("development", nil, origin)
		if err != nil {
			t.Fatal("valid development origin rejected", err)
		}
		session := httptest.NewRecorder()
		h.ServeHTTP(session, httptest.NewRequest("POST", "/v1/session", strings.NewReader("{}")))
		cookie := session.Result().Cookies()[0]
		path := "/v1/calls/00000000-0000-4000-8000-000000000001/responses/00000000-0000-4000-8000-000000000002/approve"
		body := `{"context_revision":0,"idempotency_key":"00000000-0000-4000-8000-000000000003"}`
		for _, authenticated := range []bool{false, true} {
			r := httptest.NewRequest("POST", path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			if origin != "" {
				r.Header.Set("Origin", origin)
			}
			if authenticated {
				r.AddCookie(cookie)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := 401
			if authenticated {
				want = 503
			}
			if w.Code != want {
				t.Fatal(w.Code, w.Body)
			}
		}
	}
}

func approvalSetup(t *testing.T) approvalFixture {
	t.Helper()
	repo, ctx := pipelineDatabase(t)
	t.Setenv("ENABLE_REAL_CALLS", "false")
	h, err := configuredRouter("development", repo, pipelineAppOrigin)
	if err != nil {
		t.Fatal(err)
	}
	session := pipelineRequest(h, "POST", "/v1/session", "{}", nil)
	var identity struct {
		Owner string `json:"account_id"`
	}
	_ = json.Unmarshal(session.Body.Bytes(), &identity)
	n := 4
	call, err := repo.Create(ctx, identity.Owner, repository.NewID(), repository.Draft{Mode: "mock", Destination: "+12255550110", BusinessName: "Approval fixture", Goal: repository.Goal{Kind: "restaurant", PreferredAt: "2026-11-20T19:00:00-06:00", Timezone: "America/Chicago", PartySize: &n, Constraints: []string{}, ContactName: "Fictional Guest"}, ApprovalMode: "always", ConsentAcknowledged: true})
	if err != nil {
		t.Fatal(err)
	}
	response := repository.Response{ID: repository.NewID(), CallID: call.ID, Text: "Original suggestion.", Status: "suggested", NeedsApproval: true}
	_, err = repo.Update(ctx, call.OwnerID, call.ID, "fixture", repository.NewID(), map[string]string{"state": "suggested"}, func(tx *repository.Tx) (any, error) {
		if e := tx.SetCall(0, "connected", 0); e != nil {
			return nil, e
		}
		if e := tx.PutResponse(response); e != nil {
			return nil, e
		}
		if _, e := tx.AppendEvent("call.updated", tx.Call()); e != nil {
			return nil, e
		}
		return tx.AppendEvent("response.updated", response)
	})
	if err != nil {
		t.Fatal(err)
	}
	return approvalFixture{repo, ctx, h, call, response, session.Result().Cookies()[0]}
}
func (f approvalFixture) path() string {
	return "/v1/calls/" + f.call.ID + "/responses/" + f.response.ID + "/approve"
}
func TestIntegrationDurableApprovalHTTPAndSynthesisClaim(t *testing.T) {
	f := approvalSetup(t)
	key := repository.NewID()
	text := "Edited approval only."
	body := `{"context_revision":0,"edited_text":"` + text + `","idempotency_key":"` + key + `"}`
	var wg sync.WaitGroup
	var accepted atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := pipelineRequest(f.router, "POST", f.path(), body, f.cookie)
			if w.Code == 200 {
				var r repository.Response
				if json.Unmarshal(w.Body.Bytes(), &r) == nil && r.Text == text && r.Status == "approved" {
					accepted.Add(1)
				}
			} else {
				t.Errorf("approval status=%d %s", w.Code, w.Body)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 20 {
		t.Fatal(accepted.Load())
	}
	events, err := f.repo.EventsAfter(f.ctx, f.call.OwnerID, f.call.ID, 0, 100)
	if err != nil || len(events) != 4 {
		t.Fatal("duplicate approval event", len(events), err)
	}
	// A new service has no local journal; restart replay comes from PostgreSQL.
	service := conversation.DurableApprovals{Store: f.repo}
	a := conversation.Approval{ResponseID: f.response.ID, ContextRevision: 0, EditedText: &text, IdempotencyKey: key}
	old, err := service.Approve(f.ctx, f.call.OwnerID, f.call.ID, a)
	if err != nil || old.Text != text {
		t.Fatal("restart replay", err)
	}
	changed := "Different text"
	a.EditedText = &changed
	if _, err = service.Approve(f.ctx, f.call.OwnerID, f.call.ID, a); err == nil {
		t.Fatal("changed idempotent request accepted")
	}
	a.EditedText = &text
	a.IdempotencyKey = repository.NewID()
	if _, err = service.Approve(f.ctx, f.call.OwnerID, f.call.ID, a); err == nil {
		t.Fatal("fresh-key double approval accepted")
	}
	var claims atomic.Int32
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := service.ClaimForSynthesis(f.ctx, f.call.OwnerID, f.call.ID, f.response.ID)
			if e == nil {
				if r.Text != text || r.Status != "speaking" {
					t.Error("claim did not use stored edited text")
				}
				claims.Add(1)
			}
		}()
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatal("synthesis claimed more than once", claims.Load())
	}
	// HTTP replay returns the original acknowledgement, never a new speech claim.
	if w := pipelineRequest(f.router, "POST", f.path(), body, f.cookie); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if _, err = service.ClaimForSynthesis(f.ctx, f.call.OwnerID, f.call.ID, f.response.ID); err == nil {
		t.Fatal("claimed twice after retry")
	}
	snap, err := f.repo.Snapshot(f.ctx, f.call.OwnerID, f.call.ID)
	if err != nil || len(snap.Turns) != 0 {
		t.Fatal("approval invented spoken turn")
	}
}
func TestIntegrationDurableApprovalOwnerRevisionAndInterruption(t *testing.T) {
	for _, scenario := range []string{"owner", "instruction", "paused", "ended", "partial", "superseded"} {
		t.Run(scenario, func(t *testing.T) {
			f := approvalSetup(t)
			cookie := f.cookie
			want := 409
			if scenario == "owner" {
				cookie = pipelineRequest(f.router, "POST", "/v1/session", "{}", nil).Result().Cookies()[0]
				want = 404
			} else {
				_, err := f.repo.Update(f.ctx, f.call.OwnerID, f.call.ID, "fixture_change", repository.NewID(), map[string]string{"scenario": scenario}, func(tx *repository.Tx) (any, error) {
					switch scenario {
					case "instruction":
						if e := tx.SetCall(0, "connected", 1); e != nil {
							return nil, e
						}
						instruction := repository.Instruction{ID: repository.NewID(), CallID: f.call.ID, Text: "Use latest goal.", ContextRevision: 1, Status: "applied"}
						if e := tx.PutInstruction(instruction); e != nil {
							return nil, e
						}
						return tx.AppendEvent("instruction.updated", instruction)
					case "paused", "ended":
						if e := tx.SetCall(0, scenario, 0); e != nil {
							return nil, e
						}
						return tx.AppendEvent("call.updated", tx.Call())
					case "partial":
						turn := repository.Turn{ID: repository.NewID(), CallID: f.call.ID, Speaker: "business", Text: "Wait…", StartedAtMS: 100}
						if e := tx.PutTurn(turn); e != nil {
							return nil, e
						}
						return tx.AppendEvent("turn.updated", turn)
					default:
						response := f.response
						response.ID = repository.NewID()
						if e := tx.PutResponse(response); e != nil {
							return nil, e
						}
						return tx.AppendEvent("response.updated", response)
					}
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			body := `{"context_revision":0,"idempotency_key":"` + repository.NewID() + `"}`
			w := pipelineRequest(f.router, "POST", f.path(), body, cookie)
			if w.Code != want {
				t.Fatalf("got %d want %d: %s", w.Code, want, w.Body)
			}
		})
	}
}

type rollbackApprovals struct{ repo *repository.Repository }

func (s rollbackApprovals) Update(ctx context.Context, owner, id, action, key string, request any, fn func(*repository.Tx) (any, error)) (json.RawMessage, error) {
	return s.repo.Update(ctx, owner, id, action, key, request, func(tx *repository.Tx) (any, error) {
		if _, err := fn(tx); err != nil {
			return nil, err
		}
		return nil, repository.ErrUnavailable
	})
}
func TestIntegrationApprovalRollbackPublishesNothingAndClaimRechecksRevision(t *testing.T) {
	f := approvalSetup(t)
	var publishes int
	service := conversation.DurableApprovals{Store: rollbackApprovals{f.repo}, Publish: func(...repository.Event) { publishes++ }}
	a := conversation.Approval{ResponseID: f.response.ID, IdempotencyKey: repository.NewID()}
	if _, err := service.Approve(f.ctx, f.call.OwnerID, f.call.ID, a); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatal(err)
	}
	if publishes != 0 {
		t.Fatal("published rollback")
	}
	snap, err := f.repo.Snapshot(f.ctx, f.call.OwnerID, f.call.ID)
	if err != nil || snap.Responses[0].Status != "suggested" || snap.LastSeq != 3 {
		t.Fatal("approval rollback failed", err)
	}
	service.Store = f.repo
	if _, err = service.Approve(f.ctx, f.call.OwnerID, f.call.ID, a); err != nil {
		t.Fatal(err)
	}
	_, err = f.repo.Update(f.ctx, f.call.OwnerID, f.call.ID, "revise", repository.NewID(), map[string]int{"revision": 1}, func(tx *repository.Tx) (any, error) {
		if e := tx.SetCall(0, "connected", 1); e != nil {
			return nil, e
		}
		return tx.Call(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ClaimForSynthesis(f.ctx, f.call.OwnerID, f.call.ID, f.response.ID); err == nil {
		t.Fatal("stale synthesis claim accepted")
	}
}
