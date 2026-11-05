// Package conversation defines value-based call and response transitions without I/O.
// The future orchestrator must serialize writes and use the returned state after each action.
package conversation

import (
	"errors"
	"strings"
)

type CallStatus string

const (
	Draft     CallStatus = "draft"
	Dialing   CallStatus = "dialing"
	Connected CallStatus = "connected"
	Ended     CallStatus = "ended"
	Failed    CallStatus = "failed"
	Cancelled CallStatus = "cancelled"
)

type ResponseStatus string

const (
	Generating        ResponseStatus = "generating"
	Suggested         ResponseStatus = "suggested"
	Approved          ResponseStatus = "approved"
	Speaking          ResponseStatus = "speaking"
	Spoken            ResponseStatus = "spoken"
	ResponseCancelled ResponseStatus = "cancelled"
	ResponseFailed    ResponseStatus = "failed"
)

var (
	ErrInvalidTransition = errors.New("invalid conversation transition")
	ErrStaleRevision     = errors.New("stale context revision")
	ErrStaleGeneration   = errors.New("stale response generation")
	ErrInvalidResponseID = errors.New("response id is required")
)

// Token fences callbacks from a previous response, instruction, pause or interruption.
// It belongs to one call; the orchestrator must also check the call's identity.
type Token struct {
	ContextRevision uint64
	Generation      uint64
}

type Response struct {
	ID     string
	Status ResponseStatus
	Token  Token
}

type Snapshot struct {
	Status          CallStatus
	Paused          bool
	ContextRevision uint64
	Generation      uint64
	Response        *Response
}

// State is immutable to callers. Its zero value is the same draft state as New.
type State struct {
	status   CallStatus
	paused   bool
	revision uint64
	epoch    uint64
	response Response
	hasReply bool
}

func New() State { return State{status: Draft} }

func (s State) Status() CallStatus {
	if s.status == "" {
		return Draft
	}
	return s.status
}

// Snapshot returns a detached copy so consumers cannot mutate the machine.
func (s State) Snapshot() Snapshot {
	out := Snapshot{Status: s.Status(), Paused: s.paused, ContextRevision: s.revision, Generation: s.epoch}
	if s.hasReply {
		copy := s.response
		out.Response = &copy
	}
	return out
}

func (status CallStatus) Terminal() bool {
	return status == Ended || status == Failed || status == Cancelled
}

// Transition rejects out-of-order provider callbacks. Repeated current states are idempotent.
func (s State) Transition(next CallStatus) (State, error) {
	current := s.Status()
	if current == next {
		return s, nil
	}
	allowed := false
	switch current {
	case Draft:
		allowed = next == Dialing || next.Terminal()
	case Dialing:
		allowed = next == Connected || next.Terminal()
	case Connected:
		allowed = next.Terminal()
	}
	if !allowed {
		return s, ErrInvalidTransition
	}
	s.status = next
	if next.Terminal() {
		s = s.invalidate()
		s.paused = false
	}
	return s, nil
}

func (s State) Pause() (State, error) {
	if s.Status() != Connected {
		return s, ErrInvalidTransition
	}
	if s.paused {
		return s, nil
	}
	s = s.invalidate()
	s.paused = true
	return s, nil
}

func (s State) Resume() (State, error) {
	if s.Status() != Connected {
		return s, ErrInvalidTransition
	}
	s.paused = false
	return s, nil
}

// ApplyInstruction increments context once. Persistence owns request idempotency.
// Instructions can arrive while paused, but no generation starts until resume.
func (s State) ApplyInstruction(expectedRevision uint64) (State, error) {
	if s.Status() != Connected {
		return s, ErrInvalidTransition
	}
	if expectedRevision != s.revision {
		return s, ErrStaleRevision
	}
	s.revision++
	return s.invalidate(), nil
}

// Interrupt cancels obsolete work when remote speech starts, without changing the goal.
func (s State) Interrupt() (State, error) {
	if s.Status() != Connected {
		return s, ErrInvalidTransition
	}
	return s.invalidate(), nil
}

func (s State) BeginResponse(id string) (State, Token, error) {
	if strings.TrimSpace(id) == "" {
		return s, Token{}, ErrInvalidResponseID
	}
	if s.Status() != Connected || s.paused || (s.hasReply && !s.response.Status.terminal()) {
		return s, Token{}, ErrInvalidTransition
	}
	s.epoch++
	token := Token{ContextRevision: s.revision, Generation: s.epoch}
	s.response = Response{ID: id, Status: Generating, Token: token}
	s.hasReply = true
	return s, token, nil
}

func (s State) MarkSuggested(id string, token Token) (State, error) {
	return s.advance(id, token, Generating, Suggested)
}

func (s State) Approve(id string, token Token, expectedRevision uint64) (State, error) {
	if expectedRevision != s.revision {
		return s, ErrStaleRevision
	}
	return s.advance(id, token, Suggested, Approved)
}

func (s State) StartSpeaking(id string, token Token) (State, error) {
	return s.advance(id, token, Approved, Speaking)
}

func (s State) MarkSpoken(id string, token Token) (State, error) {
	return s.advance(id, token, Speaking, Spoken)
}

func (s State) FailResponse(id string, token Token) (State, error) {
	if err := s.checkResponse(id, token); err != nil {
		return s, err
	}
	s.response.Status = ResponseFailed
	return s, nil
}

func (s State) advance(id string, token Token, from, to ResponseStatus) (State, error) {
	if err := s.checkResponse(id, token); err != nil {
		return s, err
	}
	if s.response.Status != from {
		return s, ErrInvalidTransition
	}
	s.response.Status = to
	return s, nil
}

func (s State) checkResponse(id string, token Token) error {
	if !s.hasReply || s.response.ID != id || s.response.Token != token || token.ContextRevision != s.revision || token.Generation != s.epoch {
		return ErrStaleGeneration
	}
	if s.Status() != Connected || s.paused || s.response.Status.terminal() {
		return ErrInvalidTransition
	}
	return nil
}

func (s State) invalidate() State {
	s.epoch++
	if s.hasReply && !s.response.Status.terminal() {
		s.response.Status = ResponseCancelled
	}
	return s
}

func (status ResponseStatus) terminal() bool {
	return status == Spoken || status == ResponseCancelled || status == ResponseFailed
}
