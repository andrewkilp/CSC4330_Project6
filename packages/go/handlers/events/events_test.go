package events_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/auth"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/events"
	handler "github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/events"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository/repositorytest"
)

type server struct {
	*httptest.Server
	store    *repository.Repository
	hub      *events.Hub
	sessions *auth.Sessions
}

func newServer(t *testing.T, store *repository.Repository, replayLimit int) *server {
	return newServerWith(t, store, replayLimit, auth.NewSessions())
}

func newServerWith(t *testing.T, store *repository.Repository, replayLimit int, sessions *auth.Sessions) *server {
	t.Helper()
	s := &server{store: store, hub: events.NewHub(0), sessions: sessions}
	mux := http.NewServeMux()
	if err := handler.Register(mux, handler.Config{Store: store, Hub: s.hub, RequireOwner: s.sessions.RequireOwner,
		Heartbeat: 30 * time.Millisecond, ReplayLimit: replayLimit}); err != nil {
		t.Fatal(err)
	}
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// login creates a session and a call owned by it.
func (s *server) login(t *testing.T) (cookie, callID string) {
	t.Helper()
	token, owner := s.sessions.Create()
	party := 2
	call, err := s.store.Create(context.Background(), owner, repository.NewID(), repository.Draft{Mode: "mock",
		Destination: "+15555550142", BusinessName: "Fictional Bistro", ApprovalMode: "always", ConsentAcknowledged: true,
		Goal: repository.Goal{Kind: "restaurant", PreferredAt: "2026-11-20T19:30:00-06:00", Timezone: "America/Chicago",
			PartySize: &party, Constraints: []string{}, ContactName: "Sam Rivera"}})
	if err != nil {
		t.Fatal(err)
	}
	return auth.CookieName + "=" + token, call.ID
}

func (s *server) append(t *testing.T, cookie, callID string, n int, publish bool) []repository.Event {
	t.Helper()
	owner := s.ownerOf(t, cookie)
	var all []repository.Event
	for i := range n {
		var events []repository.Event
		_, err := s.store.Update(context.Background(), owner, callID, "test.usage", repository.NewID(), map[string]int{"i": i},
			func(tx *repository.Tx) (any, error) {
				e, err := tx.AppendEvent("usage.updated", map[string]any{"elapsed_seconds": i, "estimated_cost_usd": 0.01, "estimate_only": true})
				events = append(events, e)
				return map[string]bool{"ok": true}, err
			})
		if err != nil {
			t.Fatal(err)
		}
		if publish {
			s.hub.Publish(events...)
		}
		all = append(all, events...)
	}
	return all
}

func (s *server) ownerOf(t *testing.T, cookie string) string {
	owner, ok := s.sessions.Lookup(strings.TrimPrefix(cookie, auth.CookieName+"="))
	if !ok {
		t.Fatal("unknown session")
	}
	return owner
}

type frame struct {
	id, event, data, comment string
}

type client struct {
	resp   *http.Response
	cancel context.CancelFunc
	frames chan frame
}

func (s *server) open(t *testing.T, cookie, callID, query string, header map[string]string) (*client, *http.Response) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.URL+"/v1/calls/"+callID+"/events"+query, nil)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		return nil, resp
	}
	c := &client{resp: resp, cancel: cancel, frames: make(chan frame, 64)}
	go func() {
		defer close(c.frames)
		r := bufio.NewReader(resp.Body)
		var f frame
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case line == "":
				c.frames <- f
				f = frame{}
			case strings.HasPrefix(line, ":"):
				f.comment = strings.TrimSpace(line[1:])
			case strings.HasPrefix(line, "id: "):
				f.id = line[4:]
			case strings.HasPrefix(line, "event: "):
				f.event = line[7:]
			case strings.HasPrefix(line, "data: "):
				f.data = line[6:]
			}
		}
	}()
	t.Cleanup(func() { cancel(); resp.Body.Close() })
	return c, resp
}

// next returns the next event frame, skipping comments.
func (c *client) next(t *testing.T) repository.Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case f, ok := <-c.frames:
			if !ok {
				t.Fatal("stream closed")
			}
			if f.comment != "" {
				continue
			}
			var e repository.Event
			if err := json.Unmarshal([]byte(f.data), &e); err != nil {
				t.Fatalf("bad data %q", f.data)
			}
			if !strings.HasSuffix(e.OccurredAt.Format(time.RFC3339Nano), "Z") || !strings.Contains(f.data, `Z","context_revision"`) {
				t.Fatalf("occurred_at is not UTC: %s", f.data)
			}
			if f.id != strconv.FormatInt(e.Seq, 10) || f.event != e.Type {
				t.Fatalf("frame %+v does not match event", f)
			}
			return e
		case <-timeout:
			t.Fatal("timed out waiting for an event")
		}
	}
}

func (c *client) waitComment(t *testing.T, text string) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case f, ok := <-c.frames:
			if !ok {
				t.Fatal("stream closed")
			}
			if f.comment == text {
				return
			}
		case <-timeout:
			t.Fatalf("no %q comment", text)
		}
	}
}

func errorField(t *testing.T, resp *http.Response) (int, string, string) {
	t.Helper()
	defer resp.Body.Close()
	var body struct {
		Error struct{ Code, Field string } `json:"error"`
	}
	data, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(data, &body)
	return resp.StatusCode, body.Error.Code, body.Error.Field
}

func TestOwnerGating(t *testing.T) {
	s := newServer(t, repositorytest.Open(t).Repo, 0)
	_, callID := s.login(t)
	intruder, _ := s.login(t)
	if _, resp := s.open(t, "", callID, "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no session = %d", resp.StatusCode)
	}
	if _, resp := s.open(t, intruder, callID, "", nil); resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong owner = %d", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodGet, s.URL+"/v1/calls/"+callID+"/snapshot", nil)
	req.Header.Set("Cookie", intruder)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if code, c, _ := errorField(t, resp); code != http.StatusNotFound || c != "not_found" {
		t.Fatalf("wrong-owner snapshot = %d %s", code, c)
	}
	if s.hub.Subscribers(callID) != 0 {
		t.Fatal("denied request left a subscription")
	}
}

func TestReplayCursorThenLiveAcrossTwoSubscribers(t *testing.T) {
	s := newServer(t, repositorytest.Open(t).Repo, 0)
	cookie, callID := s.login(t)
	s.append(t, cookie, callID, 3, true) // seq 2..4

	a, _ := s.open(t, cookie, callID, "?after_seq=2", nil)
	b, _ := s.open(t, cookie, callID, "?after_seq=0", map[string]string{"Last-Event-ID": "3"}) // header wins
	for _, want := range []int64{3, 4} {
		if e := a.next(t); e.Seq != want {
			t.Fatalf("a replay = %d, want %d", e.Seq, want)
		}
	}
	if e := b.next(t); e.Seq != 4 {
		t.Fatalf("b replay = %d", e.Seq)
	}
	a.waitComment(t, "connected")
	b.waitComment(t, "connected")
	s.append(t, cookie, callID, 2, true) // live 5, 6
	for _, c := range []*client{a, b} {
		for _, want := range []int64{5, 6} {
			if e := c.next(t); e.Seq != want || e.Type != "usage.updated" || e.CallID != callID {
				t.Fatalf("live = %+v, want %d", e, want)
			}
		}
	}
	// A duplicate publish is filtered by seq.
	dupes := s.append(t, cookie, callID, 1, true)
	s.hub.Publish(dupes...)
	if e := a.next(t); e.Seq != 7 {
		t.Fatalf("after duplicate = %d", e.Seq)
	}
	a.waitComment(t, "heartbeat")
}

func TestUnpublishedCommitIsFilledFromStorage(t *testing.T) {
	s := newServer(t, repositorytest.Open(t).Repo, 0)
	cookie, callID := s.login(t)
	c, _ := s.open(t, cookie, callID, "", nil)
	if e := c.next(t); e.Seq != 1 {
		t.Fatalf("initial = %d", e.Seq)
	}
	c.waitComment(t, "connected")
	s.append(t, cookie, callID, 2, false) // seq 2, 3 committed, never published
	s.append(t, cookie, callID, 1, true)  // seq 4 published
	for _, want := range []int64{2, 3, 4} {
		if e := c.next(t); e.Seq != want {
			t.Fatalf("gap fill = %d, want %d", e.Seq, want)
		}
	}
}

func TestExpiredAndInvalidCursors(t *testing.T) {
	s := newServer(t, repositorytest.Open(t).Repo, 3)
	cookie, callID := s.login(t)
	s.append(t, cookie, callID, 4, true) // last seq 5
	for _, q := range []string{"?after_seq=1", "?after_seq=6"} {
		_, resp := s.open(t, cookie, callID, q, nil)
		if code, c, field := errorField(t, resp); code != http.StatusConflict || c != "conflict" || field != "after_seq" {
			t.Fatalf("%s = %d %s %s", q, code, c, field)
		}
	}
	for _, q := range []string{"?after_seq=-1", "?after_seq=abc", "?after_seq=99999999999999999"} {
		_, resp := s.open(t, cookie, callID, q, nil)
		if code, _, field := errorField(t, resp); code != http.StatusBadRequest || field != "after_seq" {
			t.Fatalf("%s = %d %s", q, code, field)
		}
	}
	if c, _ := s.open(t, cookie, callID, "?after_seq=2", nil); c == nil || c.next(t).Seq != 3 {
		t.Fatal("cursor within the replay bound rejected")
	}
	if s.hub.Subscribers(callID) != 1 {
		t.Fatalf("rejected cursors left %d subscriptions", s.hub.Subscribers(callID)-1)
	}
}

func TestDisconnectCleansUpSubscription(t *testing.T) {
	s := newServer(t, repositorytest.Open(t).Repo, 0)
	cookie, callID := s.login(t)
	c, _ := s.open(t, cookie, callID, "", nil)
	c.next(t)
	if s.hub.Subscribers(callID) != 1 {
		t.Fatal("not subscribed")
	}
	c.cancel()
	deadline := time.Now().Add(5 * time.Second)
	for s.hub.Subscribers(callID) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("subscription leaked after disconnect")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDurableEventsReplayAfterRestart(t *testing.T) {
	db := repositorytest.Open(t)
	first := newServer(t, db.Repo, 0)
	cookie, callID := first.login(t)
	first.append(t, cookie, callID, 2, true)
	first.Close()

	// A new process: a new pool on the same database and an empty hub. Sessions are in
	// memory (#8), so the same session table stands in for an owner still signed in.
	reopened := db.Reopen(t)
	second := newServerWith(t, reopened, 0, first.sessions)
	c, _ := second.open(t, cookie, callID, "?after_seq=1", nil)
	if a, b := c.next(t), c.next(t); a.Seq != 2 || b.Seq != 3 {
		t.Fatalf("replay after restart = %d, %d", a.Seq, b.Seq)
	}
	req, _ := http.NewRequest(http.MethodGet, second.URL+"/v1/calls/"+callID+"/snapshot", nil)
	req.Header.Set("Cookie", cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var snap struct {
		repository.Snapshot
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil || snap.LastSeq != 3 || snap.Call.ID != callID || snap.Usage == nil {
		t.Fatalf("snapshot = %+v, %v", snap, err)
	}
}
