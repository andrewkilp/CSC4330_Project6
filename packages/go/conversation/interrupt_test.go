package conversation

import (
	"context"
	"errors"
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/claude"
)

func TestSpokenPortionNeverOverstates(t *testing.T) {
	text := "Hi there, table for four at seven?"
	cases := []struct {
		played, queued int
		want           string
	}{
		{0, 100, ""},
		{10, 0, ""},
		{100, 100, text},
		{150, 100, text},
		{6, 34, "Hi"},         // budget 6 runes: "Hi the" -> "there," unfinished
		{2, 34, "Hi"},         // exactly the end of a word
		{1, 34, ""},           // half a word is not a word
		{10, 34, "Hi there,"}, // budget 10: "Hi there, " ends on a space
		{33, 34, "Hi there, table for four at"},
	}
	for _, c := range cases {
		if got := SpokenPortion(text, c.played, c.queued); got != c.want {
			t.Errorf("SpokenPortion(%d/%d) = %q, want %q", c.played, c.queued, got, c.want)
		}
	}
	if got := SpokenPortion("¿Mañana a las siete?", 10, 20); got != "¿Mañana a" {
		t.Errorf("multibyte = %q", got)
	}
}

type fixedResponder struct{ text string }

func (f fixedResponder) Generate(_ context.Context, in claude.Input, emit func(claude.Update) error) error {
	return emit(claude.Update{Text: f.text, ContextRevision: in.ContextRevision, Final: true})
}

func speakingLoop(t *testing.T) (*Loop, string) {
	t.Helper()
	l, err := NewLoop("call-1", []byte(`{"kind":"restaurant","party_size":4}`), fixedResponder{"Could we book a table for four at seven tonight?"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []CallStatus{Dialing, Connected} {
		if err := l.Transition(s); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if err := l.ObserveBusinessTurn(ctx, BusinessTurn{ID: "t1", CallID: "call-1", Text: "Hello, how can I help?", IsFinal: true}); err != nil {
		t.Fatal(err)
	}
	r := l.Snapshot().Response
	if r == nil || r.Status != Suggested {
		t.Fatalf("no suggestion: %+v", r)
	}
	if _, err := l.ApproveResponse(ctx, Approval{ResponseID: r.ID, ContextRevision: r.Token.ContextRevision, IdempotencyKey: "11111111-1111-4111-8111-111111111111"}); err != nil {
		t.Fatal(err)
	}
	return l, r.ID
}

func TestInterruptPlaybackCancelsAndReturnsWhatWasHeard(t *testing.T) {
	l, id := speakingLoop(t)
	if _, err := l.BeginPlayback(id); err != nil {
		t.Fatal(err)
	}
	got, err := l.InterruptPlayback(id, 40, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reply.ID != id || got.Spoken != "Could we book a" { // 40% of 48 runes cuts "table"
		t.Fatalf("interruption = %+v", got)
	}
	if s := l.Snapshot(); s.Response.Status != ResponseCancelled || s.Status != Connected {
		t.Fatalf("after interruption: %+v", s)
	}
	// The interrupted response can never finish or restart.
	if _, err := l.CompletePlayback(id); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("late completion = %v", err)
	}
	if _, err := l.InterruptPlayback(id, 100, 100); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("second interruption = %v", err)
	}
}

func TestInterruptPlaybackOnlyForPlayingResponses(t *testing.T) {
	l, id := speakingLoop(t)
	if _, err := l.InterruptPlayback("other", 1, 2); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("wrong id = %v", err)
	}
	// Approved but not yet speaking still counts: its audio may already be queued.
	if got, err := l.InterruptPlayback(id, 0, 50); err != nil || got.Spoken != "" {
		t.Fatalf("approved = %+v %v", got, err)
	}
}
