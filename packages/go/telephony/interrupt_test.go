package telephony

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/conversation"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/mock"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/telephony/media"
)

// speakingCall approves the scenario's first reply and starts streaming it into a
// slow mock call with one mark per chunk. Marks arrive on the returned channel.
func speakingCall(t *testing.T, ctx context.Context, frameDelay time.Duration) (*Calls, *conversation.Loop, *media.Playback, *mock.ScenarioMediaStream, chan string, string) {
	t.Helper()
	loop, f, err := mock.NewConversation(mock.RestaurantTimeConflict, mock.DefaultSeed)
	if err != nil {
		t.Fatal(err)
	}
	callID := loop.CallID()
	var first mock.Turn
	for _, e := range f.Events {
		if turn, ok := e.Payload.(mock.Turn); ok && turn.Speaker == "business" && turn.IsFinal {
			first = turn
			break
		}
	}
	if err := loop.ObserveBusinessTurn(ctx, conversation.BusinessTurn{ID: first.ID, CallID: callID, Text: first.Text, IsFinal: true}); err != nil {
		t.Fatal(err)
	}
	reply := loop.Snapshot().Response
	if _, err := loop.ApproveResponse(ctx, conversation.Approval{ResponseID: reply.ID, ContextRevision: reply.Token.ContextRevision, IdempotencyKey: repository.NewID()}); err != nil {
		t.Fatal(err)
	}
	// 20 ms per 20 ms frame: real-time playback, so most of the reply is still queued.
	p, err := mock.NewProviders(mock.RestaurantTimeConflict, mock.DefaultSeed, mock.Options{FrameDelay: frameDelay})
	if err != nil {
		t.Fatal(err)
	}
	call := f.Events[0].Payload.(mock.Call)
	sid, err := p.Telephony.Start(ctx, providers.Dial{CallID: callID, Destination: call.Destination, Callbacks: providers.Callbacks{
		Voice: "https://switchboard.test/v", Status: "https://switchboard.test/s", Media: "wss://switchboard.test/m"}})
	if err != nil {
		t.Fatal(err)
	}
	marks := make(chan string, 256)
	stream, err := p.Telephony.Media(ctx, sid, func(e providers.MediaEvent) error {
		if e.Kind == providers.MediaMark {
			marks <- e.Mark
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := NewCalls()
	_ = calls.Started(callID, p.Telephony, sid)
	_ = calls.AttachLoop(callID, loop)
	_ = calls.AttachMedia(callID, stream)
	playback, err := media.NewPlayback(reply.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := calls.BeginSpeech(callID, playback); err != nil {
		t.Fatal(err)
	}
	err = loop.SynthesizeApproved(ctx, reply.ID, p.Synthesizer, "mock-voice", providers.TelephonyFormat, func(c providers.SpeechChunk) error {
		if err := stream.Send(ctx, c.Audio); err != nil {
			return err
		}
		mark, err := playback.Queue(len(c.Audio.Data))
		if err != nil {
			return err
		}
		return stream.Mark(ctx, mark)
	})
	if err != nil {
		t.Fatal(err)
	}
	playback.Finish()
	if _, err := loop.BeginPlayback(reply.ID); err != nil {
		t.Fatal(err)
	}
	return calls, loop, playback, stream.(*mock.ScenarioMediaStream), marks, reply.ID
}

func TestRemoteSpeechInterruptsClearsAudioAndKeepsTheHeardWords(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	calls, loop, playback, stream, marks, rid := speakingCall(t, ctx, 20*time.Millisecond)
	callID := loop.CallID()
	text := loop.Events()[len(loop.Events())-1].Payload.Text

	// Exactly two chunks are acknowledged as heard.
	for range 2 {
		mark := <-marks
		if id, done, err := calls.Acknowledge(callID, mark); err != nil || done || id != rid {
			t.Fatalf("ack %s: %s done=%v %v", mark, id, done, err)
		}
	}
	played, queued := playback.Progress()

	// An empty interim result is not speech.
	if got, err := calls.ObserveRemoteSpeech(ctx, callID, providers.TranscriptUpdate{TurnID: "b2", Text: "  "}); got != nil || err != nil {
		t.Fatalf("blank update interrupted: %+v %v", got, err)
	}
	got, err := calls.ObserveRemoteSpeech(ctx, callID, providers.TranscriptUpdate{TurnID: "b2", Text: "Sorry, one"})
	if err != nil || got == nil {
		t.Fatalf("interrupt = %+v %v", got, err)
	}
	want := conversation.SpokenPortion(text, played, queued)
	if got.ResponseID != rid || got.Spoken != want || got.Spoken == "" || got.Spoken == text || !strings.HasPrefix(text, got.Spoken) {
		t.Fatalf("spoken %q, want %q (prefix of %q)", got.Spoken, want, text)
	}
	if got.PlayedBytes != played || got.DroppedBytes == 0 {
		t.Fatalf("played %d dropped %d", got.PlayedBytes, got.DroppedBytes)
	}

	// Queued audio stopped: nothing more plays.
	time.Sleep(30 * time.Millisecond)
	before := stream.Played()
	time.Sleep(150 * time.Millisecond)
	if stream.Played() != before {
		t.Fatal("cleared audio kept playing")
	}
	if s := loop.Snapshot(); s.Response.Status != conversation.ResponseCancelled || s.Status != conversation.Connected {
		t.Fatalf("after interruption: %+v", s)
	}

	// A late old mark, whether already in flight or invented, cannot finish the speech...
	late := []string{rid + ".3", rid + "." + "99"}
	select {
	case m := <-marks:
		late = append([]string{m}, late...)
	default:
	}
	for _, m := range late {
		if _, _, err := calls.Acknowledge(callID, m); !errors.Is(err, media.ErrStaleMark) {
			t.Fatalf("late mark %s accepted: %v", m, err)
		}
	}
	if _, err := loop.CompletePlayback(rid); !errors.Is(err, conversation.ErrStaleGeneration) {
		t.Fatalf("late completion = %v", err)
	}
	// ...or restart it.
	if err := loop.SynthesizeApproved(ctx, rid, nil, "", providers.TelephonyFormat, nil); err == nil {
		t.Fatal("cancelled speech could be synthesized again")
	}
	if _, err := loop.BeginPlayback(rid); !errors.Is(err, conversation.ErrStaleGeneration) {
		t.Fatalf("cancelled speech restarted: %v", err)
	}
	// The provider never reports marks for cleared audio.
	time.Sleep(100 * time.Millisecond)
	select {
	case m := <-marks:
		t.Fatalf("mark %s reported after clear", m)
	default:
	}
	// Interrupting again is a no-op.
	if again, err := calls.Interrupt(ctx, callID); again != nil || err != nil {
		t.Fatalf("second interrupt = %+v %v", again, err)
	}
	// The business's turn continues normally and produces a new response.
	if err := loop.ObserveBusinessTurn(ctx, conversation.BusinessTurn{ID: repository.NewID(), CallID: callID, Text: "Sorry, one moment."}); err != nil {
		t.Fatal(err)
	}
}

// The race: marks keep arriving on the provider goroutine while the business starts
// speaking. Whatever the interleaving, exactly one of complete or interrupt wins, and
// an interrupted response is never completed.
func TestInterruptRacesWithArrivingMarks(t *testing.T) {
	interrupted, finished := 0, 0
	for i := range 24 {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		// 1 ms per frame: the reply finishes in well under a second, so the
		// interruption lands before, during or after completion across runs.
		calls, loop, _, _, marks, rid := speakingCall(t, ctx, time.Millisecond)
		callID := loop.CallID()
		var completed atomic.Bool
		stop := make(chan struct{})
		acked := make(chan struct{})
		go func() {
			defer close(acked)
			for {
				select {
				case m := <-marks:
					if _, done, err := calls.Acknowledge(callID, m); err == nil && done {
						if _, err := loop.CompletePlayback(rid); err == nil {
							completed.Store(true)
						}
					}
				case <-stop:
					return
				}
			}
		}()
		if i < 20 {
			time.Sleep(time.Duration(i*25) * time.Millisecond)
		} else {
			// The last runs let playback finish first, on any machine speed.
			deadline := time.Now().Add(10 * time.Second)
			for !completed.Load() && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
		}
		got, err := calls.ObserveRemoteSpeech(ctx, callID, providers.TranscriptUpdate{TurnID: "b", Text: "Hello?"})
		time.Sleep(50 * time.Millisecond)
		close(stop)
		<-acked
		if err != nil {
			t.Fatal(err)
		}
		status := loop.Snapshot().Response.Status
		switch {
		case got != nil && (completed.Load() || status != conversation.ResponseCancelled):
			t.Fatalf("run %d: interrupted response also completed (status %s)", i, status)
		case got == nil && (!completed.Load() || status != conversation.Spoken):
			t.Fatalf("run %d: neither interrupted nor completed (status %s)", i, status)
		case got != nil:
			interrupted++
		default:
			finished++
		}
		cancel()
	}
	t.Logf("%d runs interrupted mid-speech, %d finished before the business spoke", interrupted, finished)
	if interrupted == 0 || finished == 0 {
		t.Fatalf("race not exercised: %d interrupted, %d finished", interrupted, finished)
	}
}
