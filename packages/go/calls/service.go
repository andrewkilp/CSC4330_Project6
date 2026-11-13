// Package calls is the call service behind the /v1/calls routes. It owns call state
// transitions in the repository, publishes committed events to the hub, and runs the
// live call runtime. This MVP composition runs mock calls only: it drives the #5
// scenarios through the #6 mock providers and the real conversation loop (#19, #24,
// #29), so every transcript turn, suggestion, approval, instruction, pause and end is
// a real server operation on fictional data. Real calls are rejected until the real
// telephony start (#11) and media bridge (#21) are composed here.
package calls

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/conversation"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/events"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/mock"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

type Config struct {
	Repo *repository.Repository
	Hub  *events.Hub
	// RequireReal gates real-mode creation (#40). Nil means real calling is unavailable.
	RequireReal func() error
	// Mock pacing for the scripted business. The zero value is fast (tests).
	Mock mock.Options
	Seed uint64
}

type Service struct {
	cfg Config

	mu       sync.Mutex
	sessions map[string]*session
}

func New(cfg Config) (*Service, error) {
	if cfg.Repo == nil || cfg.Hub == nil {
		return nil, errors.New("call service requires a repository and hub")
	}
	if cfg.Seed == 0 {
		cfg.Seed = mock.DefaultSeed
	}
	return &Service{cfg: cfg, sessions: map[string]*session{}}, nil
}

// Scenario is a mock call preset. Its goal and fictional destination are what the
// scripted business and scripted suggestions were written for.
type Scenario struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	BusinessName string          `json:"business_name"`
	Destination  string          `json:"destination"`
	ApprovalMode string          `json:"approval_mode"`
	Goal         json.RawMessage `json:"goal"`
	// Instructions are the operator instructions the script anticipates. Other text is
	// accepted, but the scripted business has no reply for it.
	Instructions []string `json:"instructions"`
}

func (s *Service) Scenarios() ([]Scenario, error) {
	var out []Scenario
	for _, name := range mock.Names() {
		f, err := mock.Generate(name, s.cfg.Seed)
		if err != nil {
			return nil, err
		}
		call := f.Events[0].Payload.(mock.Call)
		goal, _ := json.Marshal(call.Goal)
		sc := Scenario{Name: name, Description: f.Description, BusinessName: call.BusinessName, Destination: call.Destination,
			ApprovalMode: call.ApprovalMode, Goal: goal, Instructions: []string{}}
		for _, e := range f.Events {
			if in, ok := e.Payload.(mock.Instruction); ok && in.Status == "applied" {
				sc.Instructions = append(sc.Instructions, in.Text)
			}
		}
		out = append(out, sc)
	}
	return out, nil
}

func (s *Service) publish(evs []repository.Event) { s.cfg.Hub.Publish(evs...) }

func storeErr(err error) *Error {
	var e *Error
	switch {
	case errors.As(err, &e):
		return e
	case errors.Is(err, repository.ErrNotFound), errors.Is(err, repository.ErrInvalid):
		// Malformed ids are indistinguishable from missing or foreign calls.
		return errNotFound
	case errors.Is(err, repository.ErrConflict):
		return conflict("idempotency_key", "This request conflicts with an earlier one. Refresh and try again.")
	}
	slog.Error("call store operation failed")
	return errInternal
}

// writer collects the events appended in one transaction.
type writer struct {
	tx     *repository.Tx
	events []repository.Event
}

func (w *writer) emit(kind string, payload any) error {
	e, err := w.tx.AppendEvent(kind, payload)
	if err == nil {
		w.events = append(w.events, e)
	}
	return err
}

// write runs one idempotent transaction on an owned call and publishes its events
// only after commit. fn reports policy failures as *Error; ran is false when the
// repository replayed an earlier identical request without calling fn.
func (s *Service) write(ctx context.Context, owner, id, action, key string, request any,
	fn func(*writer) (any, *Error, error)) (result json.RawMessage, ran bool, perr *Error) {
	var w *writer
	var policy *Error
	res, err := s.cfg.Repo.Update(ctx, owner, id, action, key, request, func(tx *repository.Tx) (any, error) {
		w = &writer{tx: tx}
		v, pe, err := fn(w)
		if pe != nil {
			policy = pe
			return nil, repository.ErrConflict
		}
		return v, err
	})
	if policy != nil {
		return nil, false, policy
	}
	if err != nil {
		return nil, false, storeErr(err)
	}
	if w != nil {
		s.publish(w.events)
	}
	return res, w != nil, nil
}

// internal is a server-originated write (runtime events), keyed uniquely.
func (s *Service) internal(owner, id string, fn func(*writer) (any, *Error, error)) *Error {
	key := repository.NewID()
	_, _, err := s.write(context.Background(), owner, id, "runtime", key, map[string]string{"key": key}, fn)
	return err
}

var written = map[string]bool{"ok": true}

func (s *Service) Create(ctx context.Context, owner string, req CreateRequest) (repository.Call, *Error) {
	if req.Call.Mode == "real" && (s.cfg.RequireReal == nil || s.cfg.RequireReal() != nil) {
		return repository.Call{}, errUnavailable
	}
	// The repository stores the call, its goal, call.updated (seq 1) and the
	// idempotency result together. Subscribers replay seq 1 from storage.
	call, err := s.cfg.Repo.Create(ctx, owner, req.IdempotencyKey, req.Call)
	if errors.Is(err, repository.ErrConflict) {
		return repository.Call{}, conflict("idempotency_key", "This idempotency key was used for a different request.")
	}
	if errors.Is(err, repository.ErrInvalid) {
		return repository.Call{}, invalid("request")
	}
	if err != nil {
		return repository.Call{}, storeErr(err)
	}
	return call, nil
}

func (s *Service) Get(ctx context.Context, owner, id string) (repository.Call, *Error) {
	call, err := s.cfg.Repo.GetCall(ctx, owner, id)
	if err != nil {
		return repository.Call{}, storeErr(err)
	}
	return call, nil
}

func (s *Service) Summary(ctx context.Context, owner, id string) (json.RawMessage, *Error) {
	call, err := s.cfg.Repo.GetCall(ctx, owner, id)
	if err != nil {
		return nil, storeErr(err)
	}
	switch call.Status {
	case "ended", "failed", "cancelled":
	default:
		return nil, conflict("status", "The summary is available after the call ends.")
	}
	var summary json.RawMessage
	for after := int64(0); ; {
		evs, err := s.cfg.Repo.EventsAfter(ctx, owner, id, after, 100)
		if err != nil {
			return nil, storeErr(err)
		}
		for _, e := range evs {
			if e.Type == "summary.ready" {
				summary = e.Payload
			}
		}
		if len(evs) < 100 {
			break
		}
		after = evs[len(evs)-1].Seq
	}
	if summary == nil {
		return nil, errNotFound
	}
	return summary, nil
}

func (s *Service) session(id string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

// Start is the explicit dial action. Creating a call never dials.
func (s *Service) Start(ctx context.Context, owner, id string, a Action) (repository.Call, *Error) {
	res, ran, perr := s.write(ctx, owner, id, "start", a.Key, a.Request, func(w *writer) (any, *Error, error) {
		c := w.tx.Call()
		if c.Status != "draft" {
			return nil, conflict("status", "This call has already started."), nil
		}
		if c.Mode != "mock" {
			return nil, errUnavailable, nil
		}
		if err := w.tx.SetCall(c.ContextRevision, "dialing", c.ContextRevision); err != nil {
			return nil, nil, err
		}
		call := w.tx.Call()
		return call, nil, w.emit("call.updated", call)
	})
	if perr != nil {
		return repository.Call{}, perr
	}
	var call repository.Call
	if repository.DecodeResult(res, &call) != nil {
		return repository.Call{}, errInternal
	}
	if !ran {
		return call, nil
	}
	sess, err := s.newMockSession(owner, call)
	if err != nil {
		slog.Error("mock session could not start")
		s.fail(owner, id)
		return call, errInternal
	}
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()
	go sess.run()
	return call, nil
}

// fail marks a call failed when its runtime cannot continue.
func (s *Service) fail(owner, id string) {
	_ = s.internal(owner, id, func(w *writer) (any, *Error, error) {
		c := w.tx.Call()
		if c.Status == "ended" || c.Status == "failed" || c.Status == "cancelled" {
			return written, nil, nil
		}
		if err := w.tx.SetCall(c.ContextRevision, "failed", c.ContextRevision); err != nil {
			return nil, nil, err
		}
		return written, nil, w.emit("call.updated", w.tx.Call())
	})
}

// instructionID is derived from the call and idempotency key, so a retried request
// finds the instruction it created.
func instructionID(callID, key string) string {
	sum := hashOf("instruction-id", callID, key)
	sum[6] = (sum[6] & 0x0f) | 0x40
	sum[8] = (sum[8] & 0x3f) | 0x80
	b := sum[:16]
	return hexUUID(b)
}

func hexUUID(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, c := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hexdigits[c>>4], hexdigits[c&0x0f])
	}
	return string(out)
}

// Instruct applies a private operator instruction: one atomic context revision,
// then the loop cancels obsolete work and regenerates against the newest context.
func (s *Service) Instruct(ctx context.Context, owner, id string, a Action) (repository.Instruction, *Error) {
	res, ran, perr := s.write(ctx, owner, id, "instruction", a.Key, a.Request, func(w *writer) (any, *Error, error) {
		c := w.tx.Call()
		if c.Status != "connected" && c.Status != "paused" {
			return nil, conflict("status", "Instructions can be sent while the call is connected."), nil
		}
		if a.ExpectedRevision != c.ContextRevision {
			return nil, conflict("expected_revision", "The call changed. Review the latest suggestion and send the instruction again."), nil
		}
		if s.session(id) == nil {
			return nil, errNotActive, nil
		}
		next := c.ContextRevision + 1
		if err := w.tx.SetCall(c.ContextRevision, c.Status, next); err != nil {
			return nil, nil, err
		}
		in := repository.Instruction{ID: instructionID(id, a.Key), CallID: id, Text: a.Text, ContextRevision: next, Status: "applied"}
		if err := w.tx.PutInstruction(in); err != nil {
			return nil, nil, err
		}
		stored, err := w.tx.Instruction(in.ID)
		if err != nil {
			return nil, nil, err
		}
		// Contract metadata timestamps are UTC with Z.
		stored.CreatedAt = stored.CreatedAt.UTC()
		if err := w.emit("instruction.updated", stored); err != nil {
			return nil, nil, err
		}
		return stored, nil, w.emit("call.updated", w.tx.Call())
	})
	if perr != nil {
		return repository.Instruction{}, perr
	}
	var in repository.Instruction
	if repository.DecodeResult(res, &in) != nil {
		return repository.Instruction{}, errInternal
	}
	if ran {
		if sess := s.session(id); sess != nil {
			sess.instruct(a.Text, uint64(a.ExpectedRevision))
		}
	}
	return in, nil
}

// Approve binds approval to one response id and context revision (#29), then hands
// the approved text to synthesis and playback (#30).
func (s *Service) Approve(ctx context.Context, owner, id, responseID string, a Action) (repository.Response, *Error) {
	if _, err := s.cfg.Repo.GetCall(ctx, owner, id); err != nil {
		return repository.Response{}, storeErr(err)
	}
	sess := s.session(id)
	if sess == nil {
		return repository.Response{}, errNotActive
	}
	reply, err := sess.loop.ApproveResponse(ctx, conversation.Approval{ResponseID: responseID, ContextRevision: uint64(a.ContextRevision),
		EditedText: a.EditedText, IdempotencyKey: a.Key})
	if err != nil {
		var approval *conversation.ApprovalError
		switch {
		case errors.As(err, &approval) && errors.Is(err, conversation.ErrInvalidInput):
			return repository.Response{}, invalid(approval.Field)
		case errors.As(err, &approval):
			msg := "This response cannot be approved in its current state."
			switch approval.Field {
			case "context_revision":
				msg = "This suggestion is out of date. Review the newest suggestion before approving."
			case "idempotency_key":
				msg = "This idempotency key was used for a different request."
			}
			return repository.Response{}, conflict(approval.Field, msg)
		case errors.Is(err, conversation.ErrResponseNotFound):
			return repository.Response{}, &Error{404, "not_found", "Response not found.", ""}
		case errors.Is(err, conversation.ErrApprovalLimit):
			return repository.Response{}, &Error{429, "rate_limited", "This call has reached its approval limit.", ""}
		}
		return repository.Response{}, errInternal
	}
	sess.sync()
	sess.speak(reply.ID)
	return toResponse(reply), nil
}

// Control pauses, resumes or ends a call (#31).
func (s *Service) Control(ctx context.Context, owner, id string, a Action) (repository.Call, *Error) {
	from := ""
	res, ran, perr := s.write(ctx, owner, id, "control", a.Key, a.Request, func(w *writer) (any, *Error, error) {
		c := w.tx.Call()
		from = c.Status
		next := ""
		switch {
		case a.Control == "pause" && c.Status == "connected":
			next = "paused"
		case a.Control == "resume" && c.Status == "paused":
			next = "connected"
		case a.Control == "end" && c.Status == "draft":
			next = "cancelled"
		case a.Control == "end" && (c.Status == "dialing" || c.Status == "connected" || c.Status == "paused"):
			next = "ended"
		default:
			return nil, conflict("action", "That action is not available in the call's current state."), nil
		}
		if err := w.tx.SetCall(c.ContextRevision, next, c.ContextRevision); err != nil {
			return nil, nil, err
		}
		call := w.tx.Call()
		return call, nil, w.emit("call.updated", call)
	})
	if perr != nil {
		return repository.Call{}, perr
	}
	var call repository.Call
	if repository.DecodeResult(res, &call) != nil {
		return repository.Call{}, errInternal
	}
	if !ran {
		return call, nil
	}
	sess := s.session(id)
	switch {
	case sess == nil && a.Control == "end" && from != "draft":
		s.summarize(owner, id, nil)
	case sess == nil:
	case a.Control == "pause":
		sess.pause()
	case a.Control == "resume":
		sess.resume()
	case a.Control == "end":
		sess.end()
		s.mu.Lock()
		delete(s.sessions, id)
		s.mu.Unlock()
	}
	return call, nil
}
