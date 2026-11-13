package responses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/claude"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/conversation"
)

const (
	callID = "00000000-0000-4000-8000-000000000010"
	key    = "00000000-0000-4000-8000-000000000020"
	origin = "http://localhost:8081"
)

type responder struct{}

func (responder) Generate(ctx context.Context, in claude.Input, emit func(claude.Update) error) error {
	return emit(claude.Update{Text: "Could you check Friday?", ContextRevision: in.ContextRevision, Final: true})
}

type ownerKey struct{}

func setup(t *testing.T) (http.Handler, *conversation.Loop, string) {
	t.Helper()
	l, err := conversation.NewLoop(callID, json.RawMessage(`{"kind":"restaurant"}`), responder{})
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Transition(conversation.Dialing)
	_ = l.Transition(conversation.Connected)
	if err := l.ObserveBusinessTurn(context.Background(), conversation.BusinessTurn{ID: "turn", CallID: callID, Text: "Is Friday okay?", IsFinal: true}); err != nil {
		t.Fatal(err)
	}
	h, err := New(Config{Origin: origin, OwnerID: func(ctx context.Context) (string, bool) { s, ok := ctx.Value(ownerKey{}).(string); return s, ok }, Lookup: func(ctx context.Context, id string) (OwnedCall, error) {
		if id != callID {
			return OwnedCall{}, ErrNotFound
		}
		return OwnedCall{ID: callID, OwnerID: "owner-a", Loop: l}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return h, l, l.Snapshot().Response.ID
}

func request(h http.Handler, id, owner, body string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/calls/"+callID+"/responses/"+id+"/approve", strings.NewReader(body))
	if owner != "" {
		r = r.WithContext(context.WithValue(r.Context(), ownerKey{}, owner))
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", origin)
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func body(key string) string { return fmt.Sprintf(`{"context_revision":0,"idempotency_key":%q}`, key) }

func TestOwnerApprovalEditIdempotencyAndDoubleApprove(t *testing.T) {
	h, l, id := setup(t)
	editBody := fmt.Sprintf(`{"context_revision":0,"idempotency_key":%q,"edited_text":"Saturday instead, please."}`, key)
	a := request(h, id, "owner-a", editBody, nil)
	if a.Code != 200 {
		t.Fatalf("%d %s", a.Code, a.Body)
	}
	var reply conversation.Reply
	if json.Unmarshal(a.Body.Bytes(), &reply) != nil || reply.Text != "Saturday instead, please." || reply.ID != id || reply.Status != conversation.Approved {
		t.Fatalf("wrong reply %s", a.Body)
	}
	events := len(l.Events())
	b := request(h, id, "owner-a", editBody, nil)
	if b.Code != 200 || a.Body.String() != b.Body.String() || len(l.Events()) != events {
		t.Fatal("retry changed approval")
	}
	c := request(h, id, "owner-a", body("00000000-0000-4000-8000-000000000021"), nil)
	if c.Code != 409 || !strings.Contains(c.Body.String(), `"field":"status"`) {
		t.Fatalf("double approve %d %s", c.Code, c.Body)
	}
	d := request(h, id, "owner-a", body(key), nil)
	if d.Code != 409 || !strings.Contains(d.Body.String(), `"field":"idempotency_key"`) {
		t.Fatalf("changed key %d %s", d.Code, d.Body)
	}
}

func TestWrongOwnerAndMissingSessionCannotApproveOrReplay(t *testing.T) {
	h, l, id := setup(t)
	for _, owner := range []string{"owner-b", ""} {
		w := request(h, id, owner, body(key), nil)
		want := 404
		if owner == "" {
			want = 401
		}
		if w.Code != want || l.Snapshot().Response.Status != conversation.Suggested {
			t.Fatalf("owner=%q %d %s", owner, w.Code, w.Body)
		}
	}
	if w := request(h, id, "owner-a", body(key), nil); w.Code != 200 {
		t.Fatal(w.Body)
	}
	if w := request(h, id, "owner-b", body(key), nil); w.Code != 404 || strings.Contains(w.Body.String(), "Friday") {
		t.Fatalf("replay leak %d %s", w.Code, w.Body)
	}
}

func TestStaleInstructionReturnsReviewedContract(t *testing.T) {
	h, l, id := setup(t)
	if err := l.Instruct(context.Background(), "Saturday instead", 0); err != nil {
		t.Fatal(err)
	}
	w := request(h, id, "owner-a", body(key), nil)
	want := `{"error":{"code":"conflict","message":"This suggestion is out of date. Review the newest suggestion before approving.","field":"context_revision"}}` + "\n"
	if w.Code != 409 || w.Body.String() != want {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestNegativeRequestsDoNotMutateApproval(t *testing.T) {
	for _, tc := range []struct {
		name, body, field string
		headers           map[string]string
	}{
		{"unknown", `{"context_revision":0,"idempotency_key":"` + key + `","owner_id":"owner-a"}`, "owner_id", nil},
		{"duplicate", `{"context_revision":0,"context_revision":1,"idempotency_key":"` + key + `"}`, "context_revision", nil},
		{"null revision", `{"context_revision":null,"idempotency_key":"` + key + `"}`, "context_revision", nil},
		{"fractional revision", `{"context_revision":0.1,"idempotency_key":"` + key + `"}`, "context_revision", nil},
		{"unsafe revision", `{"context_revision":9007199254740992,"idempotency_key":"` + key + `"}`, "context_revision", nil},
		{"missing revision", `{"idempotency_key":"` + key + `"}`, "context_revision", nil},
		{"null edit", `{"context_revision":0,"edited_text":null,"idempotency_key":"` + key + `"}`, "edited_text", nil},
		{"blank edit", `{"context_revision":0,"edited_text":" ","idempotency_key":"` + key + `"}`, "edited_text", nil},
		{"bad key", `{"context_revision":0,"idempotency_key":"secret-value"}`, "idempotency_key", nil},
		{"trailing", body(key) + ` {}`, "request", nil},
		{"null body", `null`, "request", nil},
		{"oversize", strings.Repeat(" ", 16*1024) + body(key), "request", nil},
		{"foreign origin", body(key), "origin", map[string]string{"Origin": "https://example.invalid"}},
		{"cross-site", body(key), "origin", map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{"form", body(key), "request", map[string]string{"Content-Type": "text/plain"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, l, id := setup(t)
			w := request(h, id, "owner-a", tc.body, tc.headers)
			if w.Code != 400 || !strings.Contains(w.Body.String(), `"field":"`+tc.field+`"`) || strings.Contains(w.Body.String(), "secret-value") {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if l.Snapshot().Response.Status != conversation.Suggested {
				t.Fatal("invalid request approved")
			}
		})
	}
}

func TestConcurrentRetriesEmitExactlyOneApproval(t *testing.T) {
	h, l, id := setup(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := request(h, id, "owner-a", body(key), nil); w.Code != 200 {
				t.Errorf("%d %s", w.Code, w.Body)
			}
		}()
	}
	wg.Wait()
	count := 0
	for _, e := range l.Events() {
		if e.Payload.Status == conversation.Approved {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("%d approvals", count)
	}
}

func TestLookupFailureDoesNotExposeInternalError(t *testing.T) {
	h, err := New(Config{Origin: origin, OwnerID: func(context.Context) (string, bool) { return "owner-a", true }, Lookup: func(context.Context, string) (OwnedCall, error) {
		return OwnedCall{}, errors.New("private database error")
	}})
	if err != nil {
		t.Fatal(err)
	}
	w := request(h, "response", "owner-a", body(key), nil)
	if w.Code != 500 || strings.Contains(w.Body.String(), "database") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
