package calls

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/conversation"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/mock"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

// mockVoice is the server-side voice id passed to the mock synthesizer.
const mockVoice = "mock-voice"

// session is one live mock call. Provider callbacks only enqueue work; a single
// worker applies transcript updates and playback marks in order. Operator actions
// run on request goroutines; the conversation loop serializes its own state and
// fences stale work with generation tokens.
type session struct {
	svc      *Service
	owner    string
	callID   string
	scenario string
	ctx      context.Context
	cancel   context.CancelFunc
	prov     *mock.Providers
	loop     *conversation.Loop
	fixture  mock.Fixture
	work     chan func()

	syncMu sync.Mutex // orders journal persistence
	synced int

	mu          sync.Mutex
	providerID  string
	stream      providers.MediaStream
	transcript  providers.TranscriptSession
	connectedAt time.Time
	turnIDs     map[string]string // provider turn id -> durable turn id
	turnStart   map[string]int64
	finals      map[string]bool
	pendingMark string
	speakingAt  int64
	ended       bool
}

func scenarioFor(call repository.Call) string {
	if call.Goal.Kind == "appointment" {
		return mock.AppointmentMissingDetail
	}
	return mock.RestaurantTimeConflict
}

func (s *Service) newMockSession(owner string, call repository.Call) (*session, error) {
	name := scenarioFor(call)
	prov, err := mock.NewProviders(name, s.cfg.Seed, s.cfg.Mock)
	if err != nil {
		return nil, err
	}
	responder, err := mock.NewResponder(name, s.cfg.Seed)
	if err != nil {
		return nil, err
	}
	// The scripted business and replies were written for the scenario's goal, so the
	// mock loop reasons over that goal. Real calls use the call's own goal.
	goal, _ := json.Marshal(prov.Fixture.Events[0].Payload.(mock.Call).Goal)
	loop, err := conversation.NewLoop(call.ID, goal, responder)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &session{svc: s, owner: owner, callID: call.ID, scenario: name, ctx: ctx, cancel: cancel, prov: prov, loop: loop,
		fixture: prov.Fixture, work: make(chan func(), 512), turnIDs: map[string]string{}, turnStart: map[string]int64{},
		finals: map[string]bool{}}, nil
}

func (s *session) elapsed() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connectedAt.IsZero() {
		return 0
	}
	return time.Since(s.connectedAt).Milliseconds()
}

func (s *session) enqueue(f func()) {
	select {
	case s.work <- f:
	case <-s.ctx.Done():
	}
}

func (s *session) worker() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case f := <-s.work:
			f()
		}
	}
}

func (s *session) run() {
	go s.worker()
	if err := s.loop.Transition(conversation.Dialing); err != nil {
		s.abort()
		return
	}
	pid, err := s.prov.Telephony.Start(s.ctx, providers.Dial{CallID: s.callID, Destination: s.destination(),
		Callbacks: providers.Callbacks{Voice: "https://mock.invalid/voice", Status: "https://mock.invalid/status", Media: "wss://mock.invalid/media"}})
	if err != nil {
		s.abort()
		return
	}
	ts, err := s.prov.Transcriber.Open(s.ctx, providers.TranscriptOpen{CallID: s.callID, Format: providers.TelephonyFormat}, s.onTranscript)
	if err != nil {
		s.abort()
		return
	}
	s.mu.Lock()
	s.providerID, s.transcript = pid, ts
	s.mu.Unlock()
	if d := s.svc.cfg.Mock.FrameDelay; d > 0 {
		// Let the mock line ring briefly so "dialing" is visible.
		select {
		case <-time.After(75 * d):
		case <-s.ctx.Done():
			return
		}
	}
	// Connect only if the operator has not ended the call while it was ringing.
	connected := false
	if perr := s.svc.internal(s.owner, s.callID, func(w *writer) (any, *Error, error) {
		c := w.tx.Call()
		if c.Status != "dialing" {
			return written, nil, nil
		}
		if err := w.tx.SetCall(c.ContextRevision, "connected", c.ContextRevision); err != nil {
			return nil, nil, err
		}
		connected = true
		return written, nil, w.emit("call.updated", w.tx.Call())
	}); perr != nil {
		s.abort()
		return
	}
	if !connected {
		return
	}
	if err := s.loop.Transition(conversation.Connected); err != nil {
		s.abort()
		return
	}
	s.mu.Lock()
	s.connectedAt = time.Now()
	s.mu.Unlock()
	stream, err := s.prov.Telephony.Media(s.ctx, pid, s.onMedia)
	if err != nil {
		s.abort()
		return
	}
	s.mu.Lock()
	s.stream = stream
	s.mu.Unlock()
}

func (s *session) destination() string {
	call, err := s.svc.cfg.Repo.GetCall(s.ctx, s.owner, s.callID)
	if err != nil {
		return ""
	}
	return call.Destination
}

// abort fails the call when the mock runtime cannot continue.
func (s *session) abort() {
	slog.Error("mock call runtime stopped")
	s.svc.fail(s.owner, s.callID)
	s.shutdown()
	s.svc.mu.Lock()
	delete(s.svc.sessions, s.callID)
	s.svc.mu.Unlock()
}

func (s *session) shutdown() {
	s.mu.Lock()
	s.ended = true
	pid, ts, stream := s.providerID, s.transcript, s.stream
	s.mu.Unlock()
	_ = s.loop.Transition(conversation.Ended)
	if stream != nil {
		_, _ = stream.Clear(context.Background())
	}
	if pid != "" {
		_ = s.prov.Telephony.End(context.Background(), pid)
	}
	if ts != nil {
		_ = ts.Close()
	}
	s.sync()
	s.cancel()
}

// onMedia runs on the provider's media goroutine and must not block.
func (s *session) onMedia(e providers.MediaEvent) error {
	switch e.Kind {
	case providers.MediaAudio:
		s.mu.Lock()
		ts := s.transcript
		s.mu.Unlock()
		if ts != nil {
			if err := ts.Push(s.ctx, e.Audio); err != nil && !errors.Is(err, providers.ErrClosed) {
				slog.Warn("mock transcription rejected audio")
			}
		}
	case providers.MediaMark:
		name := e.Mark
		s.enqueue(func() { s.onMark(name) })
	case providers.MediaStop:
		s.enqueue(s.onStop)
	}
	return nil
}

// onTranscript runs inside Push; it hands the update to the worker.
func (s *session) onTranscript(u providers.TranscriptUpdate) error {
	s.enqueue(func() { s.applyTranscript(u) })
	return nil
}

func (s *session) applyTranscript(u providers.TranscriptUpdate) {
	s.mu.Lock()
	if s.ended || s.finals[u.TurnID] {
		s.mu.Unlock()
		return
	}
	id, ok := s.turnIDs[u.TurnID]
	if !ok {
		id = repository.NewID()
		s.turnIDs[u.TurnID] = id
		s.turnStart[u.TurnID] = 0
		if !s.connectedAt.IsZero() {
			s.turnStart[u.TurnID] = time.Since(s.connectedAt).Milliseconds()
		}
	}
	started := s.turnStart[u.TurnID]
	if u.IsFinal {
		s.finals[u.TurnID] = true
	}
	s.mu.Unlock()
	turn := repository.Turn{ID: id, CallID: s.callID, Speaker: "business", Text: u.Text, IsFinal: u.IsFinal, StartedAtMS: started}
	if u.IsFinal {
		ended := max(s.elapsed(), started)
		turn.EndedAtMS = &ended
	}
	if perr := s.svc.internal(s.owner, s.callID, func(w *writer) (any, *Error, error) {
		if err := w.tx.PutTurn(turn); err != nil {
			return nil, nil, err
		}
		return written, nil, w.emit("turn.updated", turn)
	}); perr != nil {
		return
	}
	// New remote speech invalidates anything still queued to play.
	s.clearPlayback()
	err := s.loop.ObserveBusinessTurn(s.ctx, conversation.BusinessTurn{ID: id, CallID: s.callID, Text: u.Text, IsFinal: u.IsFinal})
	if err != nil && !errors.Is(err, conversation.ErrGenerationFailed) && !errors.Is(err, context.Canceled) {
		slog.Warn("mock conversation did not accept a business turn")
	}
	s.sync()
}

// sync persists new loop journal entries as durable response events, in order.
func (s *session) sync() {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	journal := s.loop.Events()
	if len(journal) <= s.synced {
		return
	}
	batch := journal[s.synced:]
	if perr := s.svc.internal(s.owner, s.callID, func(w *writer) (any, *Error, error) {
		current := w.tx.Call().ContextRevision
		for _, e := range batch {
			r := toResponse(e.Payload)
			if err := w.tx.PutResponse(r); err != nil {
				return nil, nil, err
			}
			// An event's envelope carries the call's revision, so only responses of
			// the current revision get one. Older ones are already retired for
			// clients by the newer revision; their rows still record the outcome.
			if r.ContextRevision == current {
				if err := w.emit("response.updated", r); err != nil {
					return nil, nil, err
				}
			}
		}
		return written, nil, nil
	}); perr != nil {
		slog.Error("response events could not be stored")
		return
	}
	s.synced = len(journal)
}

func toResponse(r conversation.Reply) repository.Response {
	return repository.Response{ID: r.ID, CallID: r.CallID, Text: r.Text, ContextRevision: int64(r.ContextRevision),
		Status: string(r.Status), NeedsApproval: r.NeedsApproval, Reason: r.Reason}
}

// speak synthesizes the approved reply into the call, then marks it so playback can
// be acknowledged. Nothing becomes a transcript turn until the mark returns.
func (s *session) speak(responseID string) {
	go func() {
		s.mu.Lock()
		stream := s.stream
		s.mu.Unlock()
		if stream == nil {
			return
		}
		err := s.loop.SynthesizeApproved(s.ctx, responseID, s.prov.Synthesizer, mockVoice, providers.TelephonyFormat,
			func(c providers.SpeechChunk) error { return stream.Send(s.ctx, c.Audio) })
		if err != nil {
			s.sync()
			return
		}
		s.mu.Lock()
		s.pendingMark = responseID
		s.mu.Unlock()
		started := s.elapsed()
		if _, err := s.loop.BeginPlayback(responseID); err != nil {
			s.sync()
			return
		}
		s.mu.Lock()
		s.speakingAt = started
		s.mu.Unlock()
		s.sync()
		if err := stream.Mark(s.ctx, responseID); err != nil {
			slog.Warn("mock playback mark was not queued")
		}
	}()
}

// onMark records a spoken assistant turn only for the current playback.
func (s *session) onMark(name string) {
	s.mu.Lock()
	if name != s.pendingMark || s.ended {
		s.mu.Unlock()
		return
	}
	s.pendingMark = ""
	started := s.speakingAt
	s.mu.Unlock()
	reply, err := s.loop.CompletePlayback(name)
	if err != nil {
		s.sync()
		return
	}
	ended := max(s.elapsed(), started)
	turn := repository.Turn{ID: repository.NewID(), CallID: s.callID, Speaker: "assistant", Text: reply.Text, IsFinal: true,
		StartedAtMS: started, EndedAtMS: &ended, ResponseID: reply.ID}
	s.sync()
	_ = s.svc.internal(s.owner, s.callID, func(w *writer) (any, *Error, error) {
		if err := w.tx.PutTurn(turn); err != nil {
			return nil, nil, err
		}
		return written, nil, w.emit("turn.updated", turn)
	})
}

func (s *session) clearPlayback() {
	s.mu.Lock()
	stream := s.stream
	pending := s.pendingMark != ""
	s.pendingMark = ""
	s.mu.Unlock()
	if stream != nil && pending {
		_, _ = stream.Clear(s.ctx)
	}
}

func (s *session) instruct(text string, expected uint64) {
	s.clearPlayback()
	go func() {
		err := s.loop.Instruct(s.ctx, text, expected)
		if err != nil && !errors.Is(err, conversation.ErrGenerationFailed) && !errors.Is(err, context.Canceled) {
			slog.Warn("mock conversation did not apply an instruction")
		}
		s.sync()
	}()
}

func (s *session) pause() {
	_ = s.loop.Pause()
	s.mu.Lock()
	stream := s.stream
	s.pendingMark = ""
	s.mu.Unlock()
	if stream != nil {
		_, _ = stream.Clear(s.ctx)
	}
	s.sync()
}

func (s *session) resume() {
	go func() {
		if err := s.loop.Resume(s.ctx); err != nil && !errors.Is(err, conversation.ErrGenerationFailed) {
			slog.Warn("mock conversation did not resume")
		}
		s.sync()
	}()
}

func (s *session) end() {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.shutdown()
	s.svc.summarize(s.owner, s.callID, s)
}

// onStop handles a hang-up reported by the provider.
func (s *session) onStop() {
	s.mu.Lock()
	ended := s.ended
	s.mu.Unlock()
	if ended {
		return
	}
	_ = s.svc.internal(s.owner, s.callID, func(w *writer) (any, *Error, error) {
		c := w.tx.Call()
		if c.Status == "ended" || c.Status == "failed" || c.Status == "cancelled" {
			return written, nil, nil
		}
		if err := w.tx.SetCall(c.ContextRevision, "ended", c.ContextRevision); err != nil {
			return nil, nil, err
		}
		return written, nil, w.emit("call.updated", w.tx.Call())
	})
	s.end()
	s.svc.mu.Lock()
	delete(s.svc.sessions, s.callID)
	s.svc.mu.Unlock()
}

// summarize publishes summary.ready for a terminal call. For a mock call that reached
// the scripted outcome, the scenario's summary is used with its evidence mapped to
// this call's finalized business turns. Otherwise the summary records only that the
// goal was not confirmed. Nothing is inferred from assistant speech.
func (s *Service) summarize(owner, id string, sess *session) {
	type details struct {
		BusinessName          string `json:"business_name"`
		ScheduledAt           string `json:"scheduled_at,omitempty"`
		Timezone              string `json:"timezone,omitempty"`
		PartySize             *int   `json:"party_size,omitempty"`
		Service               string `json:"service,omitempty"`
		ConfirmationReference string `json:"confirmation_reference,omitempty"`
	}
	type summary struct {
		CallID              string   `json:"call_id"`
		Outcome             string   `json:"outcome"`
		ConfirmedDetails    details  `json:"confirmed_details"`
		EvidenceTurnIDs     []string `json:"evidence_turn_ids"`
		UnresolvedQuestions []string `json:"unresolved_questions"`
		NextSteps           []string `json:"next_steps"`
	}
	_ = s.internal(owner, id, func(w *writer) (any, *Error, error) {
		c := w.tx.Call()
		out := summary{CallID: id, Outcome: "needs_follow_up", ConfirmedDetails: details{BusinessName: c.BusinessName},
			EvidenceTurnIDs: []string{}, UnresolvedQuestions: []string{},
			NextSteps: []string{"The call ended before the goal was confirmed. Call again to finish."}}
		if sess != nil {
			for _, e := range sess.fixture.Events {
				fs, isSummary := e.Payload.(mock.Summary)
				if !isSummary {
					continue
				}
				sess.mu.Lock()
				mapped := []string{}
				for _, fid := range fs.EvidenceTurnIDs {
					if our, found := sess.turnIDs[fid]; found && sess.finals[fid] {
						mapped = append(mapped, our)
					}
				}
				sess.mu.Unlock()
				if len(mapped) == len(fs.EvidenceTurnIDs) && len(mapped) > 0 {
					d := fs.ConfirmedDetails
					out = summary{CallID: id, Outcome: fs.Outcome, ConfirmedDetails: details{BusinessName: d.BusinessName,
						ScheduledAt: d.ScheduledAt, Timezone: d.Timezone, PartySize: d.PartySize, Service: d.Service,
						ConfirmationReference: d.ConfirmationReference}, EvidenceTurnIDs: mapped,
						UnresolvedQuestions: append([]string{}, fs.UnresolvedQuestions...), NextSteps: append([]string{}, fs.NextSteps...)}
				}
			}
		}
		return written, nil, w.emit("summary.ready", out)
	})
}
