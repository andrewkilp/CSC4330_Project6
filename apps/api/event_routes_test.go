package main

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEventRoutesAuthenticateBeforeUnavailableStorage(t *testing.T) {
	t.Setenv("ENABLE_REAL_CALLS", "false")
	h, err := configuredRouter("development", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"events", "snapshot"} {
		route := "/v1/calls/00000000-0000-4000-8000-000000000001/" + path
		if w := pipelineRequest(h, "GET", route, "", nil); w.Code != 401 && w.Code != 400 {
			t.Fatal(w.Code)
		}
		// Requests without Origin still need the authenticated owner cookie.
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/session", strings.NewReader("{}")))
		cookie := w.Result().Cookies()[0]
		r := httptest.NewRequest("GET", route, nil)
		r.AddCookie(cookie)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 503 {
			t.Fatalf("%s status=%d", path, w.Code)
		}
	}
}

func TestStreamCursorPreflight(t *testing.T) {
	t.Setenv("ENABLE_REAL_CALLS", "false")
	h, err := configuredRouter("development", nil, pipelineAppOrigin)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("OPTIONS", "/v1/calls/00000000-0000-4000-8000-000000000001/events", nil)
	r.Header.Set("Origin", pipelineAppOrigin)
	r.Header.Set("Access-Control-Request-Method", "GET")
	r.Header.Set("Access-Control-Request-Headers", "Last-Event-ID")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "Last-Event-ID") {
		t.Fatal("cursor preflight failed")
	}
}
func TestIntegrationAPIMountsSnapshotAndStreamsUnpublishedCommits(t *testing.T) {
	repo, ctx := pipelineDatabase(t)
	t.Setenv("ENABLE_REAL_CALLS", "false")
	h, err := configuredRouter("development", repo, pipelineAppOrigin)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	session := pipelineRequest(h, "POST", "/v1/session", "{}", nil)
	cookie := session.Result().Cookies()[0]
	var identity struct {
		Owner string `json:"account_id"`
	}
	if json.Unmarshal(session.Body.Bytes(), &identity) != nil {
		t.Fatal("session")
	}
	size := 4
	c, err := repo.Create(ctx, identity.Owner, repository.NewID(), repository.Draft{Mode: "mock", Destination: "+12255550110", BusinessName: "Fixture Bistro", Goal: repository.Goal{Kind: "restaurant", PreferredAt: "2026-11-20T19:00:00-06:00", Timezone: "America/Chicago", PartySize: &size, Constraints: []string{}, ContactName: "Fictional Guest"}, ApprovalMode: "always", ConsentAcknowledged: true})
	if err != nil {
		t.Fatal(err)
	}
	snap := pipelineRequest(h, "GET", "/v1/calls/"+c.ID+"/snapshot", "", cookie)
	if snap.Code != 200 {
		t.Fatal(snap.Code, snap.Body)
	}
	var checkpoint repository.Snapshot
	if json.Unmarshal(snap.Body.Bytes(), &checkpoint) != nil || checkpoint.LastSeq != 1 {
		t.Fatal("snapshot checkpoint")
	}
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(readCtx, "GET", srv.URL+"/v1/calls/"+c.ID+"/events?after_seq=1", nil)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "connected") {
		t.Fatal(line, err)
	}
	_, err = repo.Update(ctx, identity.Owner, c.ID, "fixture", repository.NewID(), map[string]string{"action": "connect"}, func(tx *repository.Tx) (any, error) {
		if e := tx.SetCall(0, "dialing", 0); e != nil {
			return nil, e
		}
		return tx.AppendEvent("call.updated", tx.Call())
	})
	if err != nil {
		t.Fatal(err)
	}
	for {
		line, err = reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "id: ") {
			if strings.TrimSpace(line) != "id: 2" {
				t.Fatal(line)
			}
			break
		}
	}
	cancel()
	foreign := pipelineRequest(h, "POST", "/v1/session", "{}", nil).Result().Cookies()[0]
	if got := pipelineRequest(h, "GET", "/v1/calls/"+c.ID+"/snapshot", "", foreign); got.Code != 404 {
		t.Fatal(got.Code)
	}
}
