package conversation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
)

const approvalKey = "00000000-0000-4000-8000-000000000001"

func suggestedLoop(t *testing.T) (*Loop, Approval) {
	t.Helper()
	l := connectedLoop(t, responderFunc(finalReply))
	if err := l.ObserveBusinessTurn(context.Background(), business("turn", "Is Friday available?", true)); err != nil {
		t.Fatal(err)
	}
	return l, Approval{ResponseID: l.Snapshot().Response.ID, IdempotencyKey: approvalKey}
}

type synthFunc func(context.Context, providers.SpeechRequest, func(providers.SpeechChunk) error) error

func (f synthFunc) Stream(ctx context.Context, r providers.SpeechRequest, emit func(providers.SpeechChunk) error) error {
	return f(ctx, r, emit)
}

func TestApprovalEditRetryAndSingleSynthesisHandoff(t *testing.T) {
	l, a := suggestedLoop(t)
	edit := "Could you check Saturday instead?"
	a.EditedText = &edit
	reply, err := l.ApproveResponse(context.Background(), a)
	if err != nil || reply.ID != a.ResponseID || reply.Text != edit || reply.Status != Approved {
		t.Fatalf("reply = %+v, err = %v", reply, err)
	}
	count := len(l.Events())
	retry, err := l.ApproveResponse(context.Background(), a)
	if err != nil || retry != reply || len(l.Events()) != count {
		t.Fatalf("retry changed approval: %+v %v", retry, err)
	}
	different := a
	different.IdempotencyKey = "00000000-0000-4000-8000-000000000002"
	if _, err := l.ApproveResponse(context.Background(), different); !errors.Is(err, ErrApprovalConflict) {
		t.Fatal(err)
	}
	changed := "different"
	different = a
	different.EditedText = &changed
	if _, err := l.ApproveResponse(context.Background(), different); !errors.Is(err, ErrApprovalConflict) {
		t.Fatal(err)
	}
	calls, chunks := 0, 0
	synth := synthFunc(func(ctx context.Context, req providers.SpeechRequest, emit func(providers.SpeechChunk) error) error {
		calls++
		if req.Text != edit || req.Token.ContextRevision != a.ContextRevision {
			t.Fatalf("wrong approved text/token: %+v", req)
		}
		return emit(providers.SpeechChunk{Audio: providers.AudioChunk{Seq: 1, Format: req.Format, Data: []byte{0xff}}, Token: req.Token, Last: true})
	})
	emit := func(c providers.SpeechChunk) error { chunks++; return nil }
	if err := l.SynthesizeApproved(context.Background(), a.ResponseID, synth, "server-voice", providers.TelephonyFormat, emit); err != nil {
		t.Fatal(err)
	}
	if err := l.SynthesizeApproved(context.Background(), a.ResponseID, synth, "server-voice", providers.TelephonyFormat, emit); !errors.Is(err, ErrApprovalConflict) {
		t.Fatal(err)
	}
	if calls != 1 || chunks != 1 || l.Snapshot().Response.Status != Approved {
		t.Fatalf("calls=%d chunks=%d state=%+v", calls, chunks, l.Snapshot())
	}
}

func TestApprovalRejectsStaleInstructionAndRemoteSpeech(t *testing.T) {
	for _, mutate := range []struct {
		name  string
		apply func(*Loop) error
		field string
	}{
		{"instruction", func(l *Loop) error { return l.Instruct(context.Background(), "Prefer Saturday", 0) }, "context_revision"},
		{"partial", func(l *Loop) error {
			return l.ObserveBusinessTurn(context.Background(), business("next", "Actually", false))
		}, "status"},
		{"pause", func(l *Loop) error { return l.Pause() }, "status"},
		{"end", func(l *Loop) error { return l.Transition(Ended) }, "status"},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			l, a := suggestedLoop(t)
			if err := mutate.apply(l); err != nil {
				t.Fatal(err)
			}
			_, err := l.ApproveResponse(context.Background(), a)
			var ae *ApprovalError
			if !errors.As(err, &ae) || ae.Field != mutate.field {
				t.Fatalf("err=%v (%+v)", err, ae)
			}
		})
	}
}

func TestApprovalInputBoundsUseUTF16AndRejectNoOpEmptyEdit(t *testing.T) {
	for _, edit := range []string{"", "  ", strings.Repeat("😀", 751), string([]byte{0xff})} {
		l, a := suggestedLoop(t)
		a.EditedText = &edit
		if _, err := l.ApproveResponse(context.Background(), a); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("err=%v", err)
		}
		if l.Snapshot().Response.Status != Suggested {
			t.Fatal("invalid input mutated response")
		}
	}
	l, a := suggestedLoop(t)
	edit := strings.Repeat("😀", 750)
	a.EditedText = &edit
	if _, err := l.ApproveResponse(context.Background(), a); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentApprovalAndInstructionNeverAuthorizeStaleSynthesis(t *testing.T) {
	for i := 0; i < 40; i++ {
		l, a := suggestedLoop(t)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = l.ApproveResponse(context.Background(), a) }()
		go func() { defer wg.Done(); _ = l.Instruct(context.Background(), "Prefer Saturday", 0) }()
		wg.Wait()
		synth := synthFunc(func(context.Context, providers.SpeechRequest, func(providers.SpeechChunk) error) error {
			t.Error("obsolete reply reached synthesis")
			return nil
		})
		if err := l.SynthesizeApproved(context.Background(), a.ResponseID, synth, "voice", providers.TelephonyFormat, func(providers.SpeechChunk) error { return nil }); !errors.Is(err, ErrApprovalConflict) {
			t.Fatal(err)
		}
	}
}

func TestInstructionCancelsInFlightSynthesisAndFencesLateChunks(t *testing.T) {
	l, a := suggestedLoop(t)
	if _, err := l.ApproveResponse(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	late := make(chan error, 1)
	done := make(chan error, 1)
	synth := synthFunc(func(ctx context.Context, req providers.SpeechRequest, emit func(providers.SpeechChunk) error) error {
		started <- ctx
		<-release
		late <- emit(providers.SpeechChunk{Audio: providers.AudioChunk{Seq: 1, Format: req.Format, Data: []byte{0xff}}, Token: req.Token, Last: true})
		return nil
	})
	go func() {
		done <- l.SynthesizeApproved(context.Background(), a.ResponseID, synth, "voice", providers.TelephonyFormat, func(providers.SpeechChunk) error { return fmt.Errorf("obsolete audio was emitted") })
	}()
	ctx := <-started
	if err := l.Instruct(context.Background(), "Prefer Saturday", 0); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil {
		t.Fatal("synthesis was not cancelled")
	}
	close(release)
	if err := <-late; !errors.Is(err, context.Canceled) {
		t.Fatalf("late chunk err=%v", err)
	}
	if err := awaitResult(t, done); !errors.Is(err, ErrStaleGeneration) {
		t.Fatal(err)
	}
	if l.Snapshot().ContextRevision != 1 || l.Snapshot().Response.Status != Suggested {
		t.Fatal("late stream changed newest response")
	}
}

func TestSynthesisRejectsMalformedOrIncompleteProviderOutput(t *testing.T) {
	for _, mode := range []string{"wrong-token", "sequence-gap", "after-last", "missing-last", "ignored-callback-error"} {
		t.Run(mode, func(t *testing.T) {
			l, a := suggestedLoop(t)
			if _, err := l.ApproveResponse(context.Background(), a); err != nil {
				t.Fatal(err)
			}
			consumerErr := errors.New("consumer stopped")
			synth := synthFunc(func(ctx context.Context, req providers.SpeechRequest, emit func(providers.SpeechChunk) error) error {
				c := providers.SpeechChunk{Audio: providers.AudioChunk{Seq: 1, Format: req.Format, Data: []byte{0xff}}, Token: req.Token, Last: true}
				switch mode {
				case "wrong-token":
					c.Token.Generation++
				case "sequence-gap":
					c.Audio.Seq = 2
				case "missing-last":
					c.Last = false
				}
				_ = emit(c)
				if mode == "after-last" {
					c.Audio.Seq++
					_ = emit(c)
				}
				return nil
			})
			err := l.SynthesizeApproved(context.Background(), a.ResponseID, synth, "voice", providers.TelephonyFormat, func(providers.SpeechChunk) error {
				if mode == "ignored-callback-error" {
					return consumerErr
				}
				return nil
			})
			want := ErrInvalidInput
			if mode == "ignored-callback-error" {
				want = consumerErr
			}
			if !errors.Is(err, want) || l.Snapshot().Response.Status != ResponseFailed {
				t.Fatalf("err=%v state=%+v", err, l.Snapshot())
			}
		})
	}
}

func TestApprovalRetryAfterInstructionIsHistoricalAckOnly(t *testing.T) {
	l, a := suggestedLoop(t)
	original, err := l.ApproveResponse(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Instruct(context.Background(), "Saturday instead", 0); err != nil {
		t.Fatal(err)
	}
	retry, err := l.ApproveResponse(context.Background(), a)
	if err != nil || retry != original {
		t.Fatalf("retry=%+v err=%v", retry, err)
	}
	if l.Snapshot().Response.ID == original.ID || l.Snapshot().Response.Status != Suggested {
		t.Fatal("retry changed current suggestion")
	}
}

func TestApprovalJournalLimitNeverEvictsRetry(t *testing.T) {
	l, first := suggestedLoop(t)
	original, err := l.ApproveResponse(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 128; i++ {
		if err := l.ObserveBusinessTurn(context.Background(), business(fmt.Sprintf("turn-%d", i), "Can you clarify?", true)); err != nil {
			t.Fatal(err)
		}
		a := Approval{ResponseID: l.Snapshot().Response.ID, IdempotencyKey: fmt.Sprintf("00000000-0000-4000-8000-%012x", i+1)}
		_, err := l.ApproveResponse(context.Background(), a)
		if i == 128 {
			if !errors.Is(err, ErrApprovalLimit) {
				t.Fatal(err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
	if got, err := l.ApproveResponse(context.Background(), first); err != nil || got != original {
		t.Fatalf("retry=%+v err=%v", got, err)
	}
}
