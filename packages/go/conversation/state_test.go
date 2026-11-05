package conversation_test

import (
	"errors"
	"reflect"
	"testing"

	c "github.com/andrewkilp/CSC4330_Project6/packages/go/conversation"
)

func must(t *testing.T, state c.State, err error) c.State {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func at(t *testing.T, status c.CallStatus) c.State {
	t.Helper()
	s := c.New()
	if status == c.Draft {
		return s
	}
	next, err := s.Transition(c.Dialing)
	s = must(t, next, err)
	if status == c.Dialing {
		return s
	}
	next, err = s.Transition(c.Connected)
	s = must(t, next, err)
	if status == c.Connected {
		return s
	}
	next, err = s.Transition(status)
	return must(t, next, err)
}

func begin(t *testing.T, state c.State, id string) (c.State, c.Token) {
	t.Helper()
	s, token, err := state.BeginResponse(id)
	if err != nil {
		t.Fatal(err)
	}
	return s, token
}

func TestCallTransitions(t *testing.T) {
	statuses := []c.CallStatus{c.Draft, c.Dialing, c.Connected, c.Ended, c.Failed, c.Cancelled}
	allowed := map[c.CallStatus][]c.CallStatus{
		c.Draft:     {c.Draft, c.Dialing, c.Ended, c.Failed, c.Cancelled},
		c.Dialing:   {c.Dialing, c.Connected, c.Ended, c.Failed, c.Cancelled},
		c.Connected: {c.Connected, c.Ended, c.Failed, c.Cancelled},
		c.Ended:     {c.Ended}, c.Failed: {c.Failed}, c.Cancelled: {c.Cancelled},
	}
	for _, from := range statuses {
		for _, to := range statuses {
			t.Run(string(from)+"/"+string(to), func(t *testing.T) {
				s := at(t, from)
				before := s.Snapshot()
				next, err := s.Transition(to)
				wantAllowed := false
				for _, want := range allowed[from] {
					wantAllowed = wantAllowed || to == want
				}
				if wantAllowed {
					if err != nil || next.Status() != to {
						t.Fatalf("transition: %s, %v", next.Status(), err)
					}
				} else if !errors.Is(err, c.ErrInvalidTransition) || !reflect.DeepEqual(before, next.Snapshot()) {
					t.Fatalf("invalid transition changed state: %+v, %v", next.Snapshot(), err)
				}
				if !reflect.DeepEqual(before, s.Snapshot()) {
					t.Fatal("input state was mutated")
				}
			})
		}
	}
}

func TestApprovalAndPlaybackOrder(t *testing.T) {
	s, token := begin(t, at(t, c.Connected), "reply-1")
	if _, err := s.StartSpeaking("reply-1", token); !errors.Is(err, c.ErrInvalidTransition) {
		t.Fatalf("unapproved generation can speak: %v", err)
	}
	if _, err := s.Approve("reply-1", token, 0); !errors.Is(err, c.ErrInvalidTransition) {
		t.Fatalf("incomplete generation can be approved: %v", err)
	}
	next, err := s.MarkSuggested("reply-1", token)
	s = must(t, next, err)
	if _, err := s.MarkSpoken("reply-1", token); !errors.Is(err, c.ErrInvalidTransition) {
		t.Fatalf("unplayed suggestion can become spoken: %v", err)
	}
	next, err = s.Approve("reply-1", token, 0)
	s = must(t, next, err)
	if _, err := s.Approve("reply-1", token, 0); !errors.Is(err, c.ErrInvalidTransition) {
		t.Fatalf("duplicate approval must be handled by request idempotency: %v", err)
	}
	next, err = s.StartSpeaking("reply-1", token)
	s = must(t, next, err)
	next, err = s.MarkSpoken("reply-1", token)
	s = must(t, next, err)
	if s.Snapshot().Response.Status != c.Spoken {
		t.Fatal("playback was not finalized")
	}
	if _, err := s.FailResponse("reply-1", token); !errors.Is(err, c.ErrInvalidTransition) {
		t.Fatalf("late failure rewrote spoken response: %v", err)
	}
	s, token2 := begin(t, s, "reply-2")
	if token2.Generation <= token.Generation {
		t.Fatal("generation was reused")
	}
	if _, err := s.MarkSpoken("reply-1", token); !errors.Is(err, c.ErrStaleGeneration) {
		t.Fatalf("late playback mark accepted: %v", err)
	}
}

func TestInvalidationFencesEveryAsyncStage(t *testing.T) {
	stages := []c.ResponseStatus{c.Generating, c.Suggested, c.Approved, c.Speaking}
	commands := []struct {
		name string
		run  func(c.State) (c.State, error)
	}{
		{"pause", func(s c.State) (c.State, error) { return s.Pause() }},
		{"instruction", func(s c.State) (c.State, error) { return s.ApplyInstruction(0) }},
		{"interrupt", func(s c.State) (c.State, error) { return s.Interrupt() }},
		{"end", func(s c.State) (c.State, error) { return s.Transition(c.Ended) }},
		{"provider failure", func(s c.State) (c.State, error) { return s.Transition(c.Failed) }},
	}
	for _, stage := range stages {
		for _, command := range commands {
			t.Run(string(stage)+"/"+command.name, func(t *testing.T) {
				s, token := begin(t, at(t, c.Connected), "reply")
				if stage != c.Generating {
					next, err := s.MarkSuggested("reply", token)
					s = must(t, next, err)
				}
				if stage == c.Approved || stage == c.Speaking {
					next, err := s.Approve("reply", token, 0)
					s = must(t, next, err)
				}
				if stage == c.Speaking {
					next, err := s.StartSpeaking("reply", token)
					s = must(t, next, err)
				}
				before := s.Snapshot()
				next, err := command.run(s)
				s = must(t, next, err)
				if s.Snapshot().Generation <= token.Generation || s.Snapshot().Response.Status != c.ResponseCancelled {
					t.Fatal("command did not fence and cancel active response")
				}
				if before.Response.Status != stage {
					t.Fatal("command mutated a prior snapshot")
				}
				callbacks := []func() error{
					func() error { _, err := s.MarkSuggested("reply", token); return err },
					func() error { _, err := s.Approve("reply", token, s.Snapshot().ContextRevision); return err },
					func() error { _, err := s.StartSpeaking("reply", token); return err },
					func() error { _, err := s.MarkSpoken("reply", token); return err },
					func() error { _, err := s.FailResponse("reply", token); return err },
				}
				for _, callback := range callbacks {
					if !errors.Is(callback(), c.ErrStaleGeneration) {
						t.Fatal("obsolete callback accepted")
					}
				}
			})
		}
	}
}

func TestPausedInstructionAndResume(t *testing.T) {
	s, old := begin(t, at(t, c.Connected), "before-pause")
	next, err := s.Pause()
	s = must(t, next, err)
	paused := s.Snapshot()
	if paused.Status != c.Connected || !paused.Paused {
		t.Fatal("pause lost the connection")
	}
	next, err = s.Pause()
	s = must(t, next, err)
	if !reflect.DeepEqual(paused, s.Snapshot()) {
		t.Fatal("duplicate pause changed generation")
	}
	if _, _, err := s.BeginResponse("during-pause"); !errors.Is(err, c.ErrInvalidTransition) {
		t.Fatalf("generated while paused: %v", err)
	}
	next, err = s.ApplyInstruction(0)
	s = must(t, next, err)
	if !s.Snapshot().Paused || s.Snapshot().ContextRevision != 1 {
		t.Fatal("instruction resumed the call or lost its revision")
	}
	if _, err := s.ApplyInstruction(0); !errors.Is(err, c.ErrStaleRevision) {
		t.Fatalf("stale instruction accepted: %v", err)
	}
	next, err = s.Resume()
	s = must(t, next, err)
	s, current := begin(t, s, "after-resume")
	if current.ContextRevision != 1 || current.Generation <= old.Generation {
		t.Fatal("resume did not use newest context")
	}
	if _, err := s.Approve("before-pause", old, 0); !errors.Is(err, c.ErrStaleRevision) {
		t.Fatalf("stale approval accepted: %v", err)
	}
}

func TestEndWhileDialingRejectsLateConnect(t *testing.T) {
	s := at(t, c.Dialing)
	next, err := s.Transition(c.Ended)
	s = must(t, next, err)
	if _, err := s.Transition(c.Connected); !errors.Is(err, c.ErrInvalidTransition) {
		t.Fatalf("late connect reopened ended call: %v", err)
	}
	for _, command := range []func() error{
		func() error { _, e := s.Resume(); return e },
		func() error { _, e := s.Pause(); return e },
		func() error { _, e := s.ApplyInstruction(0); return e },
		func() error { _, e := s.Interrupt(); return e },
		func() error { _, _, e := s.BeginResponse("late"); return e },
	} {
		if !errors.Is(command(), c.ErrInvalidTransition) {
			t.Fatal("terminal call accepted work")
		}
	}
}

func TestResponseFailureAndDetachedSnapshots(t *testing.T) {
	s, token := begin(t, at(t, c.Connected), "reply")
	copy := s.Snapshot()
	copy.Response.ID = "tampered"
	copy.Response.Status = c.Approved
	if s.Snapshot().Response.ID != "reply" || s.Snapshot().Response.Status != c.Generating {
		t.Fatal("snapshot mutated internal state")
	}
	if _, err := s.MarkSuggested("different-id", token); !errors.Is(err, c.ErrStaleGeneration) {
		t.Fatalf("wrong response accepted: %v", err)
	}
	if _, _, err := s.BeginResponse("overlapping"); !errors.Is(err, c.ErrInvalidTransition) {
		t.Fatalf("overlapping generation accepted: %v", err)
	}
	next, err := s.FailResponse("reply", token)
	s = must(t, next, err)
	if s.Snapshot().Response.Status != c.ResponseFailed || s.Status() != c.Connected {
		t.Fatal("response failure ended the call")
	}
	_, _ = begin(t, s, "recovery")
	if _, _, err := at(t, c.Connected).BeginResponse("  "); !errors.Is(err, c.ErrInvalidResponseID) {
		t.Fatalf("empty id accepted: %v", err)
	}
	var zero c.State
	if zero.Status() != c.Draft {
		t.Fatal("zero value is not draft")
	}
	if _, err := zero.Transition(c.CallStatus("unknown")); !errors.Is(err, c.ErrInvalidTransition) {
		t.Fatal("unknown state accepted")
	}
}
