package conversation

import (
	"context"
	"errors"
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
)

// approvedAndSynthesized drives a loop to the point where every chunk of the
// approved reply has been handed to the (fake) media stream.
func approvedAndSynthesized(t *testing.T) (*Loop, string) {
	t.Helper()
	l, a := suggestedLoop(t)
	if _, err := l.ApproveResponse(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	synth := synthFunc(func(ctx context.Context, req providers.SpeechRequest, emit func(providers.SpeechChunk) error) error {
		return emit(providers.SpeechChunk{Audio: providers.AudioChunk{Format: providers.TelephonyFormat, Seq: 1, Data: []byte{0xff}}, Token: req.Token, Last: true})
	})
	if err := l.SynthesizeApproved(context.Background(), a.ResponseID, synth, "voice", providers.TelephonyFormat, func(providers.SpeechChunk) error { return nil }); err != nil {
		t.Fatal(err)
	}
	return l, a.ResponseID
}

func TestPlaybackAcknowledgementMarksSpokenOnce(t *testing.T) {
	l, id := approvedAndSynthesized(t)
	if _, err := l.CompletePlayback(id); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("spoken before playback began: %v", err)
	}
	speaking, err := l.BeginPlayback(id)
	if err != nil || speaking.Status != Speaking {
		t.Fatalf("begin = %+v, %v", speaking, err)
	}
	spoken, err := l.CompletePlayback(id)
	if err != nil || spoken.Status != Spoken || spoken.Text != "Can you clarify?" {
		t.Fatalf("complete = %+v, %v", spoken, err)
	}
	if _, err := l.CompletePlayback(id); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("duplicate mark accepted: %v", err)
	}
	events := l.Events()
	if got := events[len(events)-1].Payload.Status; got != Spoken {
		t.Fatalf("last journal status = %s", got)
	}
}

func TestLateMarkAfterPauseOrInstructionCannotFinalize(t *testing.T) {
	for name, cancel := range map[string]func(*Loop) error{
		"pause":       func(l *Loop) error { return l.Pause() },
		"instruction": func(l *Loop) error { return l.Instruct(context.Background(), "Ask about Saturday.", 0) },
		"remote speech": func(l *Loop) error {
			return l.ObserveBusinessTurn(context.Background(), business("next", "Sorry, one second", false))
		},
		"end": func(l *Loop) error { return l.Transition(Ended) },
	} {
		t.Run(name, func(t *testing.T) {
			l, id := approvedAndSynthesized(t)
			if _, err := l.BeginPlayback(id); err != nil {
				t.Fatal(err)
			}
			if err := cancel(l); err != nil {
				t.Fatal(err)
			}
			if _, err := l.CompletePlayback(id); err == nil {
				t.Fatal("cleared audio was recorded as spoken")
			}
			for _, e := range l.Events() {
				if e.Payload.ID == id && e.Payload.Status == Spoken {
					t.Fatal("spoken event for cancelled playback")
				}
			}
		})
	}
}

func TestPlaybackNeverStartsForUnapprovedText(t *testing.T) {
	l, a := suggestedLoop(t)
	if _, err := l.BeginPlayback(a.ResponseID); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("suggestion began playback: %v", err)
	}
}
