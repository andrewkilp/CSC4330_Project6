package telephony

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/claude"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/conversation"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/mock"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

// gatedProvider is a telephony provider whose Start blocks until released and whose
// End is counted (and can fail).
type gatedProvider struct {
	release chan struct{}
	started chan struct{}
	ends    atomic.Int32
	fail    atomic.Bool
}

func newGatedProvider() *gatedProvider {
	return &gatedProvider{release: make(chan struct{}), started: make(chan struct{}, 1)}
}

func (p *gatedProvider) Start(ctx context.Context, _ providers.Dial) (string, error) {
	p.started <- struct{}{}
	select {
	case <-p.release:
		return "CA" + "0123456789abcdef0123456789abcdef", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func (p *gatedProvider) End(context.Context, string) error {
	p.ends.Add(1)
	if p.fail.Load() {
		return errors.New("vendor detail")
	}
	return nil
}
func (p *gatedProvider) Media(context.Context, string, func(providers.MediaEvent) error) (providers.MediaStream, error) {
	return nil, providers.ErrUnavailable
}

type startOnlyStore struct{ call repository.Call }

func (s startOnlyStore) ReserveCallStart(_ context.Context, _, callID, _ string, _ repository.StartPolicy, intent repository.StartIntent) (repository.StartAttempt, error) {
	return repository.StartAttempt{Call: s.call, AttemptID: intent.AttemptID, Fresh: true}, nil
}
func (s startOnlyStore) ConfirmCallStart(context.Context, string, string, string, string) (repository.Call, error) {
	return s.call, nil
}
func (startOnlyStore) MarkCallStartAmbiguous(context.Context, string, string, string) error {
	return nil
}

func TestEndDuringDialHangsUpWhenProviderReturns(t *testing.T) {
	callID := repository.NewID()
	provider := newGatedProvider()
	calls := NewCalls()
	service, err := NewStartService(startOnlyStore{repository.Call{ID: callID, Mode: "mock", Destination: "+12255550142", Status: "dialing"}}, nil, provider, StartConfig{})
	if err != nil {
		t.Fatal(err)
	}
	service.WithLive(calls)
	done := make(chan error, 1)
	go func() {
		_, err := service.Start(context.Background(), repository.NewID(), callID, repository.NewID())
		done <- err
	}()
	<-provider.started
	// The operator ends while the provider is still dialing: nothing to hang up yet.
	if err := calls.End(context.Background(), callID); err != nil {
		t.Fatal(err)
	}
	close(provider.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := provider.ends.Load(); n != 1 {
		t.Fatalf("late provider call hung up %d times, want 1", n)
	}
	if _, ok := calls.Media(callID); ok || calls.Context(callID).Err() == nil {
		t.Fatal("ended call still has live work")
	}
}

func TestRepeatedEndHangsUpOnceAndRetriesAfterFailure(t *testing.T) {
	calls := NewCalls()
	callID := repository.NewID()
	provider := newGatedProvider()
	provider.fail.Store(true)
	if err := calls.Started(callID, provider, "CA1"); err != nil {
		t.Fatal(err)
	}
	err := calls.End(context.Background(), callID)
	if !errors.Is(err, providers.ErrUnavailable) || strings.Contains(err.Error(), "vendor") {
		t.Fatalf("failed hang-up = %v", err)
	}
	provider.fail.Store(false)
	if err := calls.End(context.Background(), callID); err != nil {
		t.Fatalf("retry: %v", err)
	}
	for range 3 {
		if err := calls.End(context.Background(), callID); err != nil {
			t.Fatal(err)
		}
	}
	if n := provider.ends.Load(); n != 2 {
		t.Fatalf("provider End called %d times, want 2 (failure + retry)", n)
	}
}

// Pause during synthesis cancels the stream at a chunk boundary, drops queued audio
// from the call, and keeps the line open.
func TestPauseDuringSynthesisStopsAudio(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
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
	if reply == nil || reply.Status != conversation.Suggested {
		t.Fatalf("no suggestion: %+v", reply)
	}
	if _, err := loop.ApproveResponse(ctx, conversation.Approval{ResponseID: reply.ID, ContextRevision: reply.Token.ContextRevision, IdempotencyKey: repository.NewID()}); err != nil {
		t.Fatal(err)
	}

	// The synthesizer waits at each chunk until the test lets it continue.
	step := make(chan struct{})
	p, err := mock.NewProviders(mock.RestaurantTimeConflict, mock.DefaultSeed, mock.Options{FrameDelay: 20 * time.Millisecond, Pace: func(ctx context.Context) error {
		select {
		case <-step:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	call := f.Events[0].Payload.(mock.Call)
	providerID, err := p.Telephony.Start(ctx, providers.Dial{CallID: callID, Destination: call.Destination, Callbacks: providers.Callbacks{
		Voice: "https://switchboard.test/v", Status: "https://switchboard.test/s", Media: "wss://switchboard.test/m"}})
	if err != nil {
		t.Fatal(err)
	}
	// Media pacing also uses Pace; release it freely on a separate gate-free provider set.
	free, _ := mock.NewProviders(mock.RestaurantTimeConflict, mock.DefaultSeed, mock.Options{FrameDelay: 20 * time.Millisecond})
	freeID, _ := free.Telephony.Start(ctx, providers.Dial{CallID: callID, Destination: call.Destination, Callbacks: providers.Callbacks{
		Voice: "https://switchboard.test/v", Status: "https://switchboard.test/s", Media: "wss://switchboard.test/m"}})
	stream, err := free.Telephony.Media(ctx, freeID, func(providers.MediaEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	calls := NewCalls()
	if err := calls.Started(callID, p.Telephony, providerID); err != nil {
		t.Fatal(err)
	}
	if err := calls.AttachLoop(callID, loop); err != nil {
		t.Fatal(err)
	}
	if err := calls.AttachMedia(callID, stream); err != nil {
		t.Fatal(err)
	}

	var sent atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- loop.SynthesizeApproved(ctx, reply.ID, p.Synthesizer, "mock-voice", providers.TelephonyFormat, func(c providers.SpeechChunk) error {
			sent.Add(1)
			return stream.Send(ctx, c.Audio)
		})
	}()
	step <- struct{}{}
	step <- struct{}{}
	waitFor(t, func() bool { return sent.Load() == 2 })
	// Two chunks (400 ms of audio) are queued and playing slowly.
	if err := calls.Pause(ctx, callID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("synthesis completed after pause")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pause did not stop synthesis")
	}
	if n := sent.Load(); n != 2 {
		t.Fatalf("%d chunks reached the call, want 2", n)
	}
	ms := stream.(*mock.ScenarioMediaStream)
	time.Sleep(30 * time.Millisecond)
	played := ms.Played()
	time.Sleep(150 * time.Millisecond)
	if ms.Played() != played {
		t.Fatal("queued audio kept playing after pause")
	}
	s := loop.Snapshot()
	if !s.Paused || s.Status != conversation.Connected || s.Response.Status != conversation.ResponseCancelled {
		t.Fatalf("after pause: %+v", s)
	}
	// The line is still open: new audio can be sent after resume.
	if err := stream.Send(ctx, providers.AudioChunk{Format: providers.TelephonyFormat, Seq: 99, Data: make([]byte, 160)}); err != nil {
		t.Fatalf("pause closed the line: %v", err)
	}
}

// blockingResponder holds generation until released, then answers.
type blockingResponder struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (r *blockingResponder) Generate(ctx context.Context, in claude.Input, emit func(claude.Update) error) error {
	r.calls.Add(1)
	r.entered <- struct{}{}
	<-r.release
	// A late provider answers even though the call ended; the loop must reject it.
	return emit(claude.Update{Text: "Booked for four.", ContextRevision: in.ContextRevision, Final: true})
}

func connectedLoop(t *testing.T, r conversation.Responder) *conversation.Loop {
	t.Helper()
	loop, err := conversation.NewLoop(repository.NewID(), []byte(`{"kind":"restaurant","party_size":4}`), r)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []conversation.CallStatus{conversation.Dialing, conversation.Connected} {
		if err := loop.Transition(s); err != nil {
			t.Fatal(err)
		}
	}
	return loop
}

func TestEndCancelsGenerationAndRejectsLateCallbacks(t *testing.T) {
	responder := &blockingResponder{entered: make(chan struct{}, 4), release: make(chan struct{})}
	loop := connectedLoop(t, responder)
	callID := loop.CallID()
	calls := NewCalls()
	provider := newGatedProvider()
	_ = calls.Started(callID, provider, "CA1")
	_ = calls.AttachLoop(callID, loop)

	done := make(chan error, 1)
	go func() {
		done <- loop.ObserveBusinessTurn(context.Background(), conversation.BusinessTurn{ID: repository.NewID(), CallID: callID, Text: "Table for four?", IsFinal: true})
	}()
	<-responder.entered
	if err := calls.End(context.Background(), callID); err != nil {
		t.Fatal(err)
	}
	close(responder.release)
	<-done
	s := loop.Snapshot()
	if s.Status != conversation.Ended || (s.Response != nil && s.Response.Status != conversation.ResponseCancelled) {
		t.Fatalf("late generation survived end: %+v", s)
	}
	for _, e := range loop.Events() {
		if e.Payload.Status == conversation.Suggested {
			t.Fatal("a late callback produced a suggestion after end")
		}
	}
	// Late arrivals for the ended call are shut down, never registered.
	late := &closeCount{}
	if err := calls.AttachMedia(callID, late); !errors.Is(err, ErrCallEnded) || late.closed.Load() != 1 {
		t.Fatalf("late media: %v closed=%d", err, late.closed.Load())
	}
	other := connectedLoop(t, responder)
	if err := calls.AttachLoop(other.CallID(), other); err != nil {
		t.Fatal(err)
	}
	lateLoop, _ := conversation.NewLoop(callID, []byte(`{}`), responder)
	if err := calls.AttachLoop(callID, lateLoop); !errors.Is(err, ErrCallEnded) || lateLoop.Snapshot().Status != conversation.Ended {
		t.Fatalf("late loop: %v %s", err, lateLoop.Snapshot().Status)
	}
	if err := calls.Started(callID, provider, "CA2"); !errors.Is(err, ErrCallEnded) || provider.ends.Load() != 2 {
		t.Fatalf("late start: %v ends=%d", err, provider.ends.Load())
	}
	calls.Resume(callID) // no loop: must not start generation
	time.Sleep(20 * time.Millisecond)
	if n := responder.calls.Load(); n != 1 {
		t.Fatalf("generation ran %d times", n)
	}
}

func TestResumeGeneratesInBackgroundAndEndCancelsIt(t *testing.T) {
	responder := &blockingResponder{entered: make(chan struct{}, 4), release: make(chan struct{})}
	loop := connectedLoop(t, responder)
	callID := loop.CallID()
	calls := NewCalls()
	_ = calls.AttachLoop(callID, loop)
	if err := calls.Pause(context.Background(), callID); err != nil {
		t.Fatal(err)
	}
	// A business turn while paused is recorded but does not generate.
	if err := loop.ObserveBusinessTurn(context.Background(), conversation.BusinessTurn{ID: repository.NewID(), CallID: callID, Text: "Are you there?", IsFinal: true}); err != nil {
		t.Fatal(err)
	}
	if responder.calls.Load() != 0 {
		t.Fatal("generated while paused")
	}
	calls.Resume(callID) // returns immediately
	select {
	case <-responder.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("resume did not generate from the latest turn")
	}
	if err := calls.End(context.Background(), callID); err != nil {
		t.Fatal(err)
	}
	close(responder.release)
	waitFor(t, func() bool { return loop.Snapshot().Status == conversation.Ended })
	for _, e := range loop.Events() {
		if e.Payload.Status == conversation.Suggested {
			t.Fatal("resumed generation finished after end")
		}
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

type closeCount struct{ closed atomic.Int32 }

func (*closeCount) Send(context.Context, providers.AudioChunk) error { return nil }
func (*closeCount) Mark(context.Context, string) error               { return nil }
func (*closeCount) Clear(context.Context) (int, error)               { return 0, nil }
func (c *closeCount) Close() error                                   { c.closed.Add(1); return nil }
