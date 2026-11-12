package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/claude"
)

type responderFunc func(context.Context, claude.Input, func(claude.Update) error) error

func (f responderFunc) Generate(ctx context.Context, in claude.Input, emit func(claude.Update) error) error {
	return f(ctx, in, emit)
}

func connectedLoop(t *testing.T, r Responder) *Loop {
	t.Helper()
	l, err := NewLoop("call-a", json.RawMessage(`{"kind":"restaurant"}`), r)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []CallStatus{Dialing, Connected} {
		if err = l.Transition(status); err != nil {
			t.Fatal(err)
		}
	}
	return l
}
func business(id, text string, final bool) BusinessTurn {
	return BusinessTurn{ID: id, CallID: "call-a", Text: text, IsFinal: final}
}
func finalReply(ctx context.Context, in claude.Input, emit func(claude.Update) error) error {
	return emit(claude.Update{Text: "Can you clarify?", ContextRevision: in.ContextRevision, Final: true})
}
func awaitResult(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("generation did not finish")
		return nil
	}
}

func TestLoopFinalsOnlyAndReplay(t *testing.T) {
	calls := 0
	l := connectedLoop(t, responderFunc(func(ctx context.Context, in claude.Input, emit func(claude.Update) error) error {
		calls++
		if len(in.Transcript) != 1 || in.Transcript[0].Text != "Final business text" {
			t.Fatal("partial entered prompt")
		}
		return finalReply(ctx, in, emit)
	}))
	ctx := context.Background()
	for _, turn := range []BusinessTurn{business("turn", "Partial", false), business("turn", "Final business text", true), business("turn", "Final business text", true)} {
		if err := l.ObserveBusinessTurn(ctx, turn); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("provider called %d times", calls)
	}
	snap := l.Snapshot()
	if snap.Response.Status != Suggested {
		t.Fatalf("not awaiting approval: %+v", snap)
	}
	// The loop has no approval or playback method, so it cannot cross this gate.
	for i, event := range l.Events() {
		if event.Seq != uint64(i+1) || event.Type != "response.updated" || event.CallID != "call-a" || !event.Payload.NeedsApproval {
			t.Fatalf("invalid event: %+v", event)
		}
		if event.Payload.Status == Speaking || event.Payload.Status == Spoken || event.Payload.Status == Approved {
			t.Fatal("crossed approval")
		}
	}
	if err := l.ObserveBusinessTurn(ctx, business("turn", "Changed final", true)); err != ErrInvalidInput {
		t.Fatal(err)
	}
	events := l.Events()
	events[0].Payload.Text = "mutated"
	if l.Events()[0].Payload.Text == "mutated" {
		t.Fatal("mutable journal")
	}
}

func TestNewRevisionCancelsAndFencesLateCallbacks(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	late := make(chan error, 1)
	r := responderFunc(func(ctx context.Context, in claude.Input, emit func(claude.Update) error) error {
		if in.ContextRevision == 0 {
			started <- ctx
			<-release
			late <- emit(claude.Update{Text: "obsolete", Final: true})
			return nil
		}
		if len(in.Instructions) != 1 || in.Instructions[0] != "Ask for later" {
			return ErrInvalidInput
		}
		return emit(claude.Update{Text: "Is later available?", ContextRevision: in.ContextRevision, Final: true})
	})
	l := connectedLoop(t, r)
	done := make(chan error, 1)
	go func() { done <- l.ObserveBusinessTurn(context.Background(), business("turn", "What time?", true)) }()
	var oldCtx context.Context
	select {
	case oldCtx = <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("not started")
	}
	if err := l.Instruct(context.Background(), "Ask for later", 0); err != nil {
		t.Fatal(err)
	}
	if oldCtx.Err() != context.Canceled {
		t.Fatal("old context not cancelled")
	}
	close(release)
	if err := awaitResult(t, done); err != ErrStaleGeneration {
		t.Fatal(err)
	}
	if err := awaitResult(t, late); err != ErrStaleGeneration {
		t.Fatal(err)
	}
	snap := l.Snapshot()
	if snap.ContextRevision != 1 || snap.Response.Status != Suggested {
		t.Fatalf("%+v", snap)
	}
	events := l.Events()
	if events[len(events)-1].Payload.Text != "Is later available?" {
		t.Fatal("obsolete callback won")
	}
	for _, e := range events {
		if e.Payload.Text == "obsolete" {
			t.Fatal("late draft escaped")
		}
	}
	if err := l.Instruct(context.Background(), "stale", 0); err != ErrStaleRevision {
		t.Fatal(err)
	}
}

func TestInvalidationStopsGeneration(t *testing.T) {
	for _, action := range []string{"pause", "end", "interrupt", "partial", "new final"} {
		t.Run(action, func(t *testing.T) {
			started := make(chan context.Context, 1)
			r := responderFunc(func(ctx context.Context, in claude.Input, emit func(claude.Update) error) error {
				if in.Transcript[len(in.Transcript)-1].Text == "New final" {
					return finalReply(ctx, in, emit)
				}
				started <- ctx
				<-ctx.Done()
				if err := emit(claude.Update{Text: "late", Final: true}); err == nil {
					t.Error("accepted cancelled callback")
				}
				return ctx.Err()
			})
			l := connectedLoop(t, r)
			done := make(chan error, 1)
			go func() { done <- l.ObserveBusinessTurn(context.Background(), business("first", "Question", true)) }()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("not started")
			}
			var err error
			switch action {
			case "pause":
				err = l.Pause()
			case "end":
				err = l.Transition(Ended)
			case "interrupt":
				err = l.Interrupt()
			case "partial":
				err = l.ObserveBusinessTurn(context.Background(), business("second", "New partial", false))
			case "new final":
				err = l.ObserveBusinessTurn(context.Background(), business("second", "New final", true))
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = awaitResult(t, done); err != ErrStaleGeneration {
				t.Fatal(err)
			}
			if action != "new final" && l.Snapshot().Response.Status != ResponseCancelled {
				t.Fatal("not cancelled")
			}
		})
	}
}

func TestPausedInstructionsResumeNewestContext(t *testing.T) {
	var seen claude.Input
	l := connectedLoop(t, responderFunc(func(ctx context.Context, in claude.Input, emit func(claude.Update) error) error {
		seen = in
		return finalReply(ctx, in, emit)
	}))
	if err := l.Pause(); err != nil {
		t.Fatal(err)
	}
	if err := l.ObserveBusinessTurn(context.Background(), business("turn", "Question", true)); err != nil {
		t.Fatal(err)
	}
	if err := l.Instruct(context.Background(), "Private instruction", 0); err != nil {
		t.Fatal(err)
	}
	if len(l.Events()) != 0 {
		t.Fatal("generated while paused")
	}
	if err := l.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen.ContextRevision != 1 || len(seen.Instructions) != 1 || seen.Transcript[0].Text != "Question" {
		t.Fatalf("%+v", seen)
	}
	if err := l.Transition(Ended); err != nil {
		t.Fatal(err)
	}
	if err := l.ObserveBusinessTurn(context.Background(), business("late", "Late", true)); err != ErrInvalidTransition {
		t.Fatal(err)
	}
}

func TestFailedStreamsNeverSuggest(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    responderFunc
	}{
		{"partial failure", func(ctx context.Context, in claude.Input, emit func(claude.Update) error) error {
			_ = emit(claude.Update{Text: "partial"})
			return errors.New("secret vendor details")
		}},
		{"final then failure", func(ctx context.Context, in claude.Input, emit func(claude.Update) error) error {
			_ = finalReply(ctx, in, emit)
			return errors.New("secret")
		}},
		{"no final", func(context.Context, claude.Input, func(claude.Update) error) error { return nil }},
		{"wrong revision", func(ctx context.Context, in claude.Input, emit func(claude.Update) error) error {
			_ = emit(claude.Update{Text: "wrong", ContextRevision: 9, Final: true})
			return nil
		}},
		{"oversized", func(ctx context.Context, in claude.Input, emit func(claude.Update) error) error {
			return emit(claude.Update{Text: strings.Repeat("x", 1501), Final: true})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := connectedLoop(t, tc.r)
			if err := l.ObserveBusinessTurn(context.Background(), business("turn", "Question", true)); err != ErrGenerationFailed {
				t.Fatal(err)
			}
			events := l.Events()
			last := events[len(events)-1].Payload
			if last.Status != ResponseFailed || last.Text != "" {
				t.Fatalf("%+v", last)
			}
			for _, e := range events {
				if e.Payload.Status == Suggested {
					t.Fatal("suggested failed generation")
				}
			}
		})
	}
}

func TestParentCancellationAndWrongCall(t *testing.T) {
	l := connectedLoop(t, responderFunc(func(ctx context.Context, in claude.Input, emit func(claude.Update) error) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	turn := business("turn", "Question", true)
	turn.CallID = "other"
	if err := l.ObserveBusinessTurn(context.Background(), turn); err != ErrInvalidInput {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := l.ObserveBusinessTurn(ctx, business("turn", "Question", true)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if l.Snapshot().Response.Status != ResponseFailed {
		t.Fatal("cancelled parent left active response")
	}
}

func TestLongTranscriptFitsRequestCap(t *testing.T) {
	var last claude.Input
	l := connectedLoop(t, responderFunc(func(ctx context.Context, in claude.Input, emit func(claude.Update) error) error {
		last = in
		return finalReply(ctx, in, emit)
	}))
	ctx := context.Background()
	for i := range 40 {
		text := strings.Repeat(string(rune('a'+i%26)), 5000)
		if err := l.ObserveBusinessTurn(ctx, business("turn-"+strconv.Itoa(i), text, true)); err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
	}
	b, err := json.Marshal(last)
	if err != nil || len(b) > maxRequestBytes {
		t.Fatalf("request is %d bytes, want <= %d", len(b), maxRequestBytes)
	}
	if n := len(last.Transcript); n == 40 || n < 2 {
		t.Fatalf("expected oldest turns trimmed, kept %d", n)
	}
	if got := last.Transcript[len(last.Transcript)-1].Text; got != strings.Repeat("n", 5000) {
		t.Fatal("newest business turn was not kept last")
	}
	if l.Snapshot().Response.Status != Suggested {
		t.Fatal("long call should still reach a suggestion")
	}
}
