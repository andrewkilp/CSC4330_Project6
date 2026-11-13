// Package integration runs complete restaurant and appointment journeys (#41) on the
// real pipeline components with the network-free mock providers (#6):
//
//	create -> start (telephony.StartService) -> media (mock telephony)
//	-> transcription (mock STT) -> conversation.Loop generation (#19, #24)
//	-> private instruction -> changed suggestion -> approval (#29)
//	-> synthesis -> playback mark -> spoken turn (#30)
//	-> pause/resume and end (control.Apply, telephony.Calls, #31) -> summary
//
// Every durable event is appended to an in-memory journal in the documented envelope,
// so a journey runs in CI without Postgres. The recorded run is then strict-decoded
// and checked by mock.Validate (#5) as a complete event transcript.
//
// The operator is scripted from the scenario's expected inputs. It approves each
// suggestion, sends the scenario's instruction where one is expected, pauses and
// resumes once on the first suggestion, and ends the call after the business's
// closing line. The summary is the scenario's scripted summary: summary generation
// is #39's, and nothing here validates a real provider.
package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/conversation"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/control"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/mock"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/telephony"
)

// Result is one recorded journey.
type Result struct {
	// Transcript is the run as a validated fixture: every durable event in order.
	Transcript mock.Fixture
	// Spoken is the assistant speech acknowledged by playback marks, in order.
	Spoken []string
	// Business is the finalized business speech, in order.
	Business []string
	// Instructions are the private operator instructions that were applied.
	Instructions []string
	// Paused reports that a suggestion was cancelled by pause and regenerated on resume.
	Paused bool
	// HungUp reports that end hung up the provider call and its stream stopped.
	HungUp bool
}

const voice = "mock-voice"

// Run executes one scenario. It never dials: the only telephony is the mock, which
// accepts fictional 555-01XX numbers only.
func Run(ctx context.Context, scenario string, seed uint64) (Result, error) {
	p, err := mock.NewProviders(scenario, seed, mock.Options{})
	if err != nil {
		return Result{}, err
	}
	responder, err := mock.NewResponder(scenario, seed)
	if err != nil {
		return Result{}, err
	}
	fixtureCall := p.Fixture.Events[0].Payload.(mock.Call)
	goal, _ := json.Marshal(fixtureCall.Goal)
	loop, err := conversation.NewLoop(fixtureCall.ID, goal, responder)
	if err != nil {
		return Result{}, err
	}
	j := &journey{p: p, loop: loop, calls: telephony.NewCalls(), owner: fixtureCall.OwnerID,
		store: newStore(fixtureCall), media: make(chan providers.MediaEvent, 1<<16),
		updates: make(chan providers.TranscriptUpdate, 1<<10), marks: make(chan string, 16),
		stopped: make(chan struct{}), acted: map[string]string{}}
	return j.run(ctx)
}

type journey struct {
	p       *mock.Providers
	loop    *conversation.Loop
	calls   *telephony.Calls
	store   *store
	owner   string
	stream  providers.MediaStream
	media   chan providers.MediaEvent
	updates chan providers.TranscriptUpdate
	marks   chan string
	stopped chan struct{}
	result  Result
	// acted maps a scripted reply id to the run's response the operator acted on.
	acted     map[string]string
	business  int
	connected time.Time
}

func (j *journey) callID() string { return j.loop.CallID() }

func (j *journey) run(ctx context.Context) (Result, error) {
	// Start: the real start service reserves the call, places it through the mock
	// provider and records the provider call in the live registry.
	start, err := telephony.NewStartService(j.store, nil, j.p.Telephony, telephony.StartConfig{})
	if err != nil {
		return Result{}, err
	}
	start.WithLive(j.calls)
	if _, err := start.Start(ctx, j.owner, j.callID(), repository.NewID()); err != nil {
		return Result{}, fmt.Errorf("start: %w", err)
	}
	if err := j.loop.Transition(conversation.Dialing); err != nil {
		return Result{}, err
	}
	// The provider answers (Twilio: in-progress), then media attaches.
	j.store.setStatus("connected")
	if err := j.loop.Transition(conversation.Connected); err != nil {
		return Result{}, err
	}
	j.connected = time.Now()
	if err := j.calls.AttachLoop(j.callID(), j.loop); err != nil {
		return Result{}, err
	}
	session, err := j.p.Transcriber.Open(ctx, providers.TranscriptOpen{CallID: j.callID(), Format: providers.TelephonyFormat},
		func(u providers.TranscriptUpdate) error { j.updates <- u; return nil })
	if err != nil {
		return Result{}, err
	}
	defer session.Close()
	j.stream, err = j.p.Telephony.Media(ctx, j.store.providerID(), func(e providers.MediaEvent) error { j.media <- e; return nil })
	if err != nil {
		return Result{}, err
	}
	if err := j.calls.AttachMedia(j.callID(), j.stream); err != nil {
		return Result{}, err
	}
	go j.pump(ctx, session)

	for {
		var u providers.TranscriptUpdate
		select {
		case u = <-j.updates:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
		j.store.turn(repository.Turn{ID: u.TurnID, CallID: j.callID(), Speaker: "business", Text: u.Text, IsFinal: u.IsFinal,
			StartedAtMS: u.StartedAtMS, EndedAtMS: endedAt(u)})
		if !u.IsFinal {
			if err := j.loop.ObserveBusinessTurn(ctx, conversation.BusinessTurn{ID: u.TurnID, CallID: j.callID(), Text: u.Text}); err != nil {
				return Result{}, fmt.Errorf("partial turn: %w", err)
			}
			j.store.drain(j.loop, nil)
			continue
		}
		j.business++
		j.result.Business = append(j.result.Business, u.Text)
		if !j.scripted(j.business) {
			// The business's closing line: the operator ends the call.
			return j.end(ctx)
		}
		err := j.loop.ObserveBusinessTurn(ctx, conversation.BusinessTurn{ID: u.TurnID, CallID: j.callID(), Text: u.Text, IsFinal: true})
		j.store.drain(j.loop, nil)
		if err != nil {
			return Result{}, fmt.Errorf("generate after business turn %d: %w", j.business, err)
		}
		if err := j.operate(ctx); err != nil {
			return Result{}, err
		}
	}
}

func endedAt(u providers.TranscriptUpdate) *int64 {
	if !u.IsFinal {
		return nil
	}
	v := u.EndedAtMS
	return &v
}

// pump routes media: inbound audio to recognition, marks and stop to the journey.
func (j *journey) pump(ctx context.Context, session providers.TranscriptSession) {
	for e := range j.media {
		switch e.Kind {
		case providers.MediaAudio:
			_ = session.Push(ctx, e.Audio)
		case providers.MediaMark:
			j.marks <- e.Mark
		case providers.MediaStop:
			close(j.stopped)
			return
		}
	}
}

// scripted reports whether the scenario has a reply after n finalized business turns.
func (j *journey) scripted(n int) bool {
	for _, r := range j.p.Script() {
		if r.BusinessTurns == n {
			return true
		}
	}
	return false
}

func (j *journey) scriptFor(text string) (mock.ScriptedReply, bool) {
	for _, r := range j.p.Script() {
		if r.Text == text {
			return r, true
		}
	}
	return mock.ScriptedReply{}, false
}

func (j *journey) instructionFor(scriptID string) (string, bool) {
	for _, in := range j.p.Fixture.Expected.OperatorInputs {
		if in.Kind == "instruction" && in.ResponseID == scriptID {
			return in.Text, true
		}
	}
	return "", false
}

func (j *journey) suggestion() (*conversation.Response, error) {
	s := j.loop.Snapshot()
	if s.Response == nil || s.Response.Status != conversation.Suggested {
		return nil, errors.New("no suggestion to act on")
	}
	return s.Response, nil
}

// operate is the scripted operator for the current suggestion.
func (j *journey) operate(ctx context.Context) error {
	reply, err := j.suggestion()
	if err != nil {
		return err
	}
	text := j.store.responseText(reply.ID)
	script, ok := j.scriptFor(text)
	if !ok {
		return fmt.Errorf("suggestion %q is not in the scenario script", text)
	}

	if !j.result.Paused {
		if err := j.pauseAndResume(ctx, reply.ID); err != nil {
			return err
		}
		if reply, err = j.suggestion(); err != nil {
			return err
		}
	}

	if instruction, ok := j.instructionFor(script.ResponseID); ok {
		j.acted[script.ResponseID] = reply.ID
		if err := j.instruct(ctx, instruction); err != nil {
			return err
		}
		return j.operate(ctx)
	}
	j.acted[script.ResponseID] = reply.ID
	return j.approveAndSpeak(ctx, reply)
}

func (j *journey) control(ctx context.Context, action string) (control.Result, error) {
	return j.store.Control(ctx, j.owner, j.callID(), repository.NewID(), action)
}

// pauseAndResume exercises #31 on a waiting suggestion: pause cancels it and keeps
// the line; resume regenerates from the newest context in the background.
func (j *journey) pauseAndResume(ctx context.Context, cancelled string) error {
	if _, err := j.control(ctx, "pause"); err != nil {
		return fmt.Errorf("pause: %w", err)
	}
	if err := j.calls.Pause(ctx, j.callID()); err != nil {
		return err
	}
	j.store.drain(j.loop, nil)
	if _, err := j.control(ctx, "resume"); err != nil {
		return fmt.Errorf("resume: %w", err)
	}
	j.calls.Resume(j.callID())
	deadline := time.Now().Add(5 * time.Second)
	for {
		if s := j.loop.Snapshot(); s.Response != nil && s.Response.ID != cancelled && s.Response.Status == conversation.Suggested {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("resume did not regenerate a suggestion")
		}
		time.Sleep(time.Millisecond)
	}
	j.store.drain(j.loop, nil)
	j.result.Paused = true
	return nil
}

// instruct records a private instruction like #28: pending at revision N, the waiting
// suggestion cancelled at N, applied at N+1, then the changed suggestion at N+1.
func (j *journey) instruct(ctx context.Context, text string) error {
	revision := j.store.revision()
	id := repository.NewID()
	j.store.instruction(repository.Instruction{ID: id, CallID: j.callID(), Text: text, ContextRevision: int64(revision), Status: "pending", CreatedAt: time.Now().UTC()})
	err := j.loop.Instruct(ctx, text, revision)
	// Cancellations at the old revision come first, then the applied instruction.
	j.store.drain(j.loop, func(r conversation.Reply) bool { return r.ContextRevision <= revision })
	j.store.applyInstruction(repository.Instruction{ID: id, CallID: j.callID(), Text: text, ContextRevision: int64(revision + 1), Status: "applied", CreatedAt: time.Now().UTC()})
	j.store.drain(j.loop, nil)
	if err != nil {
		return fmt.Errorf("instruction: %w", err)
	}
	j.result.Instructions = append(j.result.Instructions, text)
	return nil
}

// approveAndSpeak approves the suggestion, streams its audio into the call behind a
// playback mark, and records the assistant turn only when the mark comes back.
func (j *journey) approveAndSpeak(ctx context.Context, reply *conversation.Response) error {
	if _, err := j.loop.ApproveResponse(ctx, conversation.Approval{ResponseID: reply.ID, ContextRevision: reply.Token.ContextRevision, IdempotencyKey: repository.NewID()}); err != nil {
		return fmt.Errorf("approve: %w", err)
	}
	j.store.drain(j.loop, nil)
	started := time.Since(j.connected).Milliseconds()
	err := j.loop.SynthesizeApproved(ctx, reply.ID, j.p.Synthesizer, voice, providers.TelephonyFormat, func(c providers.SpeechChunk) error {
		return j.stream.Send(ctx, c.Audio)
	})
	if err != nil {
		return fmt.Errorf("synthesize: %w", err)
	}
	if err := j.stream.Mark(ctx, reply.ID); err != nil {
		return err
	}
	if _, err := j.loop.BeginPlayback(reply.ID); err != nil {
		return fmt.Errorf("begin playback: %w", err)
	}
	j.store.drain(j.loop, nil)
	select {
	case mark := <-j.marks:
		if mark != reply.ID {
			return fmt.Errorf("playback mark %q, want %q", mark, reply.ID)
		}
	case <-time.After(10 * time.Second):
		return errors.New("playback mark never came back")
	case <-ctx.Done():
		return ctx.Err()
	}
	spoken, err := j.loop.CompletePlayback(reply.ID)
	if err != nil {
		return fmt.Errorf("complete playback: %w", err)
	}
	j.store.drain(j.loop, nil)
	ended := max(time.Since(j.connected).Milliseconds(), started)
	j.store.turn(repository.Turn{ID: repository.NewID(), CallID: j.callID(), Speaker: "assistant", Text: spoken.Text, IsFinal: true,
		StartedAtMS: started, EndedAtMS: &ended, ResponseID: spoken.ID})
	j.result.Spoken = append(j.result.Spoken, spoken.Text)
	return nil
}

// end hangs up through #31 and publishes the summary.
func (j *journey) end(ctx context.Context) (Result, error) {
	j.store.usage(mock.Usage{ElapsedSeconds: int64(time.Since(j.connected).Seconds()), EstimatedCostUSD: 0, EstimateOnly: true})
	ended, err := j.control(ctx, "end")
	if err != nil || ended.Call.Status != "ended" {
		return Result{}, fmt.Errorf("end: %v (status %q)", err, ended.Call.Status)
	}
	if err := j.calls.End(ctx, j.callID()); err != nil {
		return Result{}, fmt.Errorf("hang up: %w", err)
	}
	// End closes our stream before hanging up, so no stop event is expected: the
	// stream must refuse audio and the provider call must be gone.
	silence := providers.AudioChunk{Format: providers.TelephonyFormat, Seq: 1 << 20, Data: make([]byte, 160)}
	if err := j.stream.Send(ctx, silence); !errors.Is(err, providers.ErrClosed) {
		return Result{}, fmt.Errorf("stream still accepts audio after end: %v", err)
	}
	if _, err := j.p.Telephony.Media(ctx, j.store.providerID(), func(providers.MediaEvent) error { return nil }); !errors.Is(err, providers.ErrClosed) {
		return Result{}, fmt.Errorf("provider call still up after end: %v", err)
	}
	j.result.HungUp = true
	if j.loop.Snapshot().Status != conversation.Ended {
		return Result{}, errors.New("conversation loop survived end")
	}

	var summary mock.Summary
	for _, e := range j.p.Fixture.Events {
		if s, ok := e.Payload.(mock.Summary); ok {
			summary = s
		}
	}
	summary.CallID = j.callID()
	j.store.append("summary.ready", j.store.revision(), summary)

	expected := j.p.Fixture.Expected
	expected.OperatorInputs = nil
	for _, in := range j.p.Fixture.Expected.OperatorInputs {
		id, ok := j.acted[in.ResponseID]
		if !ok {
			return Result{}, fmt.Errorf("the operator never acted on scripted reply %s", in.ResponseID)
		}
		in.ResponseID = id
		expected.OperatorInputs = append(expected.OperatorInputs, in)
	}
	transcript := mock.Fixture{Scenario: j.p.Fixture.Scenario, Description: j.p.Fixture.Description, Seed: j.p.Fixture.Seed,
		Fictional: true, Expected: expected, Events: j.store.events()}
	// Round-trip through the strict decoder, then validate the whole transcript.
	data, err := mock.Encode(transcript)
	if err != nil {
		return Result{}, err
	}
	if transcript, err = mock.Decode(data); err != nil {
		return Result{}, err
	}
	if err := mock.Validate(transcript); err != nil {
		return Result{}, fmt.Errorf("event transcript: %w", err)
	}
	j.result.Transcript = transcript
	return j.result, nil
}

// ---------------------------------------------------------------------------
// store is the in-memory durable side: call state, responses and the event journal.
// It implements telephony.StartStore and control.Store (through control.Apply), so
// start and controls run their real transition logic.

type store struct {
	mu        sync.Mutex
	call      repository.Call
	sid       string
	responses map[string]repository.Response
	journal   []mock.Event
	last      time.Time
	drained   int
}

func newStore(c mock.Call) *store {
	g := c.Goal
	now := time.Now().UTC()
	s := &store{responses: map[string]repository.Response{}, call: repository.Call{ID: c.ID, OwnerID: c.OwnerID, Mode: c.Mode,
		Destination: c.Destination, BusinessName: c.BusinessName, Status: "draft", ApprovalMode: c.ApprovalMode, CreatedAt: now, UpdatedAt: now,
		Goal: repository.Goal{Kind: g.Kind, PreferredAt: g.PreferredAt, Timezone: g.Timezone, PartySize: g.PartySize, Service: g.Service,
			Constraints: g.Constraints, ContactName: g.ContactName, ContactPhone: g.ContactPhone}}}
	s.appendLocked("call.updated", 0, s.call)
	return s
}

func (s *store) appendLocked(kind string, revision uint64, payload any) {
	at := time.Now().UTC()
	if at.Before(s.last) {
		at = s.last
	}
	s.last = at
	s.journal = append(s.journal, mock.Event{ID: repository.NewID(), CallID: s.call.ID, Seq: uint64(len(s.journal) + 1), Type: kind,
		OccurredAt: at.Format(time.RFC3339Nano), ContextRevision: revision, Payload: payload})
}

func (s *store) append(kind string, revision uint64, payload any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendLocked(kind, revision, payload)
}

func (s *store) events() []mock.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mock.Event(nil), s.journal...)
}

func (s *store) revision() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return uint64(s.call.ContextRevision)
}

func (s *store) providerID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sid
}

func (s *store) responseText(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.responses[id].Text
}

// setStatus records a provider status callback (for example in-progress).
func (s *store) setStatus(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.call.Status, s.call.UpdatedAt = status, time.Now().UTC()
	s.appendLocked("call.updated", uint64(s.call.ContextRevision), s.call)
}

func (s *store) turn(t repository.Turn) { s.append("turn.updated", s.revision(), t) }

func (s *store) usage(u mock.Usage) { s.append("usage.updated", s.revision(), u) }

func (s *store) instruction(n repository.Instruction) {
	s.append("instruction.updated", uint64(n.ContextRevision), n)
}

// applyInstruction advances the call's context revision and records the instruction.
func (s *store) applyInstruction(n repository.Instruction) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.call.ContextRevision = n.ContextRevision
	s.appendLocked("instruction.updated", uint64(n.ContextRevision), n)
}

// drain journals the loop's new response updates in order. A response that is already
// terminal here (for example cancelled by pause in the control transaction) ignores
// the loop's own later notice of the same cancellation. With until set, it stops at
// the first update that does not match.
func (s *store) drain(loop *conversation.Loop, until func(conversation.Reply) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	updates := loop.Events()
	for ; s.drained < len(updates); s.drained++ {
		r := updates[s.drained].Payload
		if until != nil && !until(r) {
			return
		}
		if prev, ok := s.responses[r.ID]; ok && (prev.Status == "spoken" || prev.Status == "cancelled" || prev.Status == "failed") {
			continue
		}
		v := repository.Response{ID: r.ID, CallID: r.CallID, Text: r.Text, ContextRevision: int64(r.ContextRevision),
			Status: string(r.Status), NeedsApproval: r.NeedsApproval, Reason: r.Reason}
		s.responses[r.ID] = v
		s.appendLocked("response.updated", r.ContextRevision, v)
	}
}

// telephony.StartStore

func (s *store) ReserveCallStart(_ context.Context, owner, callID, _ string, _ repository.StartPolicy, intent repository.StartIntent) (repository.StartAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owner != s.call.OwnerID || callID != s.call.ID {
		return repository.StartAttempt{}, repository.ErrNotFound
	}
	if s.call.Status != "draft" {
		return repository.StartAttempt{Call: s.call}, nil
	}
	s.call.Status, s.call.UpdatedAt = "dialing", time.Now().UTC()
	s.appendLocked("call.updated", uint64(s.call.ContextRevision), s.call)
	return repository.StartAttempt{Call: s.call, AttemptID: intent.AttemptID, Fresh: true}, nil
}

func (s *store) ConfirmCallStart(_ context.Context, _, _, _, sid string) (repository.Call, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sid = sid
	return s.call, nil
}

func (s *store) MarkCallStartAmbiguous(context.Context, string, string, string) error { return nil }

// control.Store

func (s *store) Control(_ context.Context, owner, callID, key, action string) (control.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owner != s.call.OwnerID || callID != s.call.ID {
		return control.Result{}, repository.ErrNotFound
	}
	call, events, err := control.Apply(tx{s}, action)
	if err != nil {
		return control.Result{}, err
	}
	raw, _ := json.Marshal(call)
	return control.Result{Raw: raw, Call: call, Events: events}, nil
}

// tx adapts the store to control.Tx; the caller holds s.mu.
type tx struct{ s *store }

func (t tx) Call() repository.Call { return t.s.call }
func (t tx) Responses() ([]repository.Response, error) {
	out := make([]repository.Response, 0, len(t.s.responses))
	for _, r := range t.s.responses {
		out = append(out, r)
	}
	return out, nil
}
func (t tx) PutResponse(r repository.Response) error {
	if _, ok := t.s.responses[r.ID]; !ok {
		return repository.ErrNotFound
	}
	t.s.responses[r.ID] = r
	return nil
}
func (t tx) SetCall(expected int64, status string, revision int64) error {
	if expected != t.s.call.ContextRevision {
		return repository.ErrConflict
	}
	t.s.call.Status, t.s.call.ContextRevision, t.s.call.UpdatedAt = status, revision, time.Now().UTC()
	return nil
}
func (t tx) AppendEvent(kind string, payload any) (repository.Event, error) {
	t.s.appendLocked(kind, uint64(t.s.call.ContextRevision), payload)
	e := t.s.journal[len(t.s.journal)-1]
	return repository.Event{ID: e.ID, CallID: e.CallID, Seq: int64(e.Seq), Type: kind, ContextRevision: int64(e.ContextRevision)}, nil
}
