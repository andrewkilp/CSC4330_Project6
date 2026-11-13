package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
)

func request() providers.ResponseRequest {
	return providers.ResponseRequest{
		CallID: "3d2e9e65-7f30-4ca1-9e80-4b5c6d7e8f92",
		Goal:   json.RawMessage(`{"kind":"restaurant"}`),
		Transcript: []providers.TranscriptTurn{
			{Speaker: providers.SpeakerBusiness, Text: "What time?"},
			{Speaker: providers.SpeakerAssistant, Text: "Around seven."},
		},
		Instructions:    []string{"Ask for 7 pm"},
		ContextRevision: 3,
	}
}

func TestResponderMapsRequestAndDrafts(t *testing.T) {
	c := client(t, func(r *http.Request) (*http.Response, error) {
		var b struct{ Messages []struct{ Content string } }
		var in Input
		if json.NewDecoder(r.Body).Decode(&b) != nil || json.Unmarshal([]byte(b.Messages[0].Content), &in) != nil ||
			len(in.Transcript) != 2 || in.Transcript[1].Speaker != "assistant" || in.Instructions[0] != "Ask for 7 pm" || in.ContextRevision != 3 {
			t.Fatalf("request not mapped: %+v", in)
		}
		return response(200, fixture(t, "complete")), nil
	})
	var drafts []providers.ResponseDraft
	if err := (Responder{c}).Generate(context.Background(), request(), func(d providers.ResponseDraft) error {
		drafts = append(drafts, d)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 3 || !drafts[2].Final || drafts[2].Text != "Is 7 pm available?" || drafts[2].ContextRevision != 3 {
		t.Fatalf("drafts: %+v", drafts)
	}
}

func TestResponderCancelAndCallbackErrors(t *testing.T) {
	c := client(t, func(*http.Request) (*http.Response, error) { return response(200, fixture(t, "complete")), nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := 0
	if err := (Responder{c}).Generate(ctx, request(), func(providers.ResponseDraft) error { n++; cancel(); return nil }); !errors.Is(err, context.Canceled) || n != 1 {
		t.Fatalf("cancel: %v after %d drafts", err, n)
	}
	stop := errors.New("consumer stopped")
	if err := (Responder{c}).Generate(context.Background(), request(), func(providers.ResponseDraft) error { return stop }); err != stop {
		t.Fatalf("callback error = %v, want it unchanged", err)
	}
}

func TestResponderMapsProviderFailures(t *testing.T) {
	raw, err := os.ReadFile("testdata/rate-limit.http")
	if err != nil {
		t.Fatal(err)
	}
	c := client(t, func(*http.Request) (*http.Response, error) {
		return http.ReadResponse(bufio.NewReader(bytes.NewReader(raw)), nil)
	})
	err = (Responder{c}).Generate(context.Background(), request(), func(providers.ResponseDraft) error {
		t.Fatal("rate limit emitted text")
		return nil
	})
	if !errors.Is(err, providers.ErrUnavailable) || !errors.Is(err, ErrRateLimited) {
		t.Fatalf("rate limit = %v", err)
	}
	partial := client(t, func(*http.Request) (*http.Response, error) { return response(200, fixture(t, "partial-failure")), nil })
	err = (Responder{partial}).Generate(context.Background(), request(), func(d providers.ResponseDraft) error {
		if d.Final {
			t.Fatal("partial failure finalized")
		}
		return nil
	})
	if !errors.Is(err, providers.ErrUnavailable) {
		t.Fatalf("partial failure = %v", err)
	}
}

func TestResponderRejectsInvalidInputBeforeSending(t *testing.T) {
	c := client(t, func(*http.Request) (*http.Response, error) { t.Fatal("invalid input reached network"); return nil, nil })
	never := func(providers.ResponseDraft) error { t.Fatal("callback for invalid input"); return nil }
	bad := request()
	bad.Instructions = []string{strings.Repeat("x", 1001)}
	long := request()
	long.Transcript[0].Text = strings.Repeat("x", 6001) // within the shared bound, over this adapter's
	for name, tc := range map[string]struct {
		r   Responder
		req providers.ResponseRequest
	}{
		"no client":         {Responder{}, request()},
		"long instruction":  {Responder{c}, bad},
		"empty transcript":  {Responder{c}, providers.ResponseRequest{CallID: request().CallID, Goal: request().Goal}},
		"adapter turn size": {Responder{c}, long},
	} {
		if err := tc.r.Generate(context.Background(), tc.req, never); !errors.Is(err, providers.ErrInvalid) {
			t.Errorf("%s: %v, want providers.ErrInvalid", name, err)
		}
	}
}
