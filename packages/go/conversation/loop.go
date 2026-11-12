package conversation

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/claude"
)

// Responder uses #14's existing cancellable boundary until #6 owns the shared
// interface. Both *claude.Client and the deterministic mock implement it.
type Responder interface {
	Generate(context.Context, claude.Input, func(claude.Update) error) error
}

var _ Responder = (*claude.Client)(nil)

var (
	ErrInvalidInput     = errors.New("invalid conversation input")
	ErrGenerationFailed = errors.New("response generation failed")
)

// BusinessTurn updates replace partials by ID. Finalized IDs are immutable.
type BusinessTurn struct {
	ID      string
	CallID  string
	Text    string
	IsFinal bool
}

type Reply struct {
	ID              string         `json:"id"`
	CallID          string         `json:"call_id"`
	Text            string         `json:"text"`
	ContextRevision uint64         `json:"context_revision"`
	Status          ResponseStatus `json:"status"`
	NeedsApproval   bool           `json:"needs_approval"`
	Reason          string         `json:"reason,omitempty"`
}

// ResponseEvent is the documented response.updated envelope. The local journal
// is not durable SSE; #23 owns persistence, replay and fanout.
type ResponseEvent struct {
	ID              string `json:"id"`
	CallID          string `json:"call_id"`
	Seq             uint64 `json:"seq"`
	Type            string `json:"type"`
	OccurredAt      string `json:"occurred_at"`
	ContextRevision uint64 `json:"context_revision"`
	Payload         Reply  `json:"payload"`
}

// Loop serializes one call's state. Provider work runs outside the lock; every
// callback is fenced by call identity, response ID and generation token.
// Public operations may run concurrently. ObserveBusinessTurn, Instruct and
// Resume block until generation returns, so their contexts must be cancellable.
type Loop struct {
	mu           sync.Mutex
	callID       string
	goal         json.RawMessage
	responder    Responder
	state        State
	turns        []BusinessTurn
	instructions []string
	reply        Reply
	cancel       context.CancelFunc
	events       []ResponseEvent
}

func NewLoop(callID string, goal json.RawMessage, responder Responder) (*Loop, error) {
	if strings.TrimSpace(callID) == "" || !json.Valid(goal) || len(goal) > 16*1024 || responder == nil {
		return nil, ErrInvalidInput
	}
	return &Loop{callID: callID, goal: append(json.RawMessage(nil), goal...), responder: responder, state: New()}, nil
}

func (l *Loop) Snapshot() Snapshot { l.mu.Lock(); defer l.mu.Unlock(); return l.state.Snapshot() }

// Events returns a detached local journal. Never treat it as durable replay.
func (l *Loop) Events() []ResponseEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]ResponseEvent(nil), l.events...)
}

func (l *Loop) emitLocked() {
	l.events = append(l.events, ResponseEvent{ID: uuid(), CallID: l.callID, Seq: uint64(len(l.events) + 1), Type: "response.updated", OccurredAt: time.Now().UTC().Format(time.RFC3339Nano), ContextRevision: l.reply.ContextRevision, Payload: l.reply})
}

func uuid() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("cannot allocate conversation identifier")
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// syncLocked executes cancellation after an invalidating state transition.
func (l *Loop) syncLocked(next State) {
	l.state = next
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
	if s := next.Snapshot(); s.Response != nil && s.Response.Status == ResponseCancelled && l.reply.Status != ResponseCancelled {
		l.reply.Status = ResponseCancelled
		l.reply.Text = ""
		l.emitLocked()
	}
}

func (l *Loop) Transition(status CallStatus) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	next, err := l.state.Transition(status)
	if err != nil {
		return err
	}
	if status.Terminal() {
		l.syncLocked(next)
	} else {
		l.state = next
	}
	return nil
}
func (l *Loop) Pause() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	next, err := l.state.Pause()
	if err == nil {
		l.syncLocked(next)
	}
	return err
}
func (l *Loop) Interrupt() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	next, err := l.state.Interrupt()
	if err == nil {
		l.syncLocked(next)
	}
	return err
}

func (l *Loop) ObserveBusinessTurn(ctx context.Context, turn BusinessTurn) error {
	l.mu.Lock()
	if ctx.Err() != nil {
		l.mu.Unlock()
		return ctx.Err()
	}
	if turn.CallID != l.callID || turn.ID == "" || strings.TrimSpace(turn.Text) == "" || len(turn.Text) > 6000 || !utf8.ValidString(turn.Text) {
		l.mu.Unlock()
		return ErrInvalidInput
	}
	if l.state.Status() != Connected {
		l.mu.Unlock()
		return ErrInvalidTransition
	}
	index := -1
	for i, t := range l.turns {
		if t.ID == turn.ID {
			index = i
			if t.IsFinal {
				l.mu.Unlock()
				if turn.IsFinal && turn.Text == t.Text {
					return nil
				}
				return ErrInvalidInput
			}
			break
		}
	}
	if index < 0 {
		if len(l.turns) >= 200 {
			l.mu.Unlock()
			return ErrInvalidInput
		}
		l.turns = append(l.turns, turn)
	} else {
		l.turns[index] = turn
	}
	// Any new remote speech makes an older suggestion obsolete, including a
	// partial turn. Only final business text starts a replacement generation.
	next, _ := l.state.Interrupt()
	l.syncLocked(next)
	if !turn.IsFinal || l.state.Snapshot().Paused {
		l.mu.Unlock()
		return nil
	}
	return l.generateLocked(ctx)
}

// Instruct is an in-memory orchestration hook, not #28's atomic repository/API
// implementation. The owner must persist and deduplicate before invoking it.
func (l *Loop) Instruct(ctx context.Context, text string, expectedRevision uint64) error {
	l.mu.Lock()
	if ctx.Err() != nil {
		l.mu.Unlock()
		return ctx.Err()
	}
	if strings.TrimSpace(text) == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 1000 || len(l.instructions) >= 100 {
		l.mu.Unlock()
		return ErrInvalidInput
	}
	next, err := l.state.ApplyInstruction(expectedRevision)
	if err != nil {
		l.mu.Unlock()
		return err
	}
	l.syncLocked(next)
	l.instructions = append(l.instructions, text)
	if next.Snapshot().Paused {
		l.mu.Unlock()
		return nil
	}
	return l.generateLocked(ctx)
}

func (l *Loop) Resume(ctx context.Context) error {
	l.mu.Lock()
	if ctx.Err() != nil {
		l.mu.Unlock()
		return ctx.Err()
	}
	wasPaused := l.state.Snapshot().Paused
	next, err := l.state.Resume()
	if err != nil {
		l.mu.Unlock()
		return err
	}
	l.state = next
	if !wasPaused {
		l.mu.Unlock()
		return nil
	}
	return l.generateLocked(ctx)
}

// generateLocked consumes the lock and always releases it before returning.
func (l *Loop) generateLocked(parent context.Context) error {
	// Instructions and resume must not answer an older question while a
	// business turn is still arriving. Its final update will trigger generation.
	for _, turn := range l.turns {
		if !turn.IsFinal {
			l.mu.Unlock()
			return nil
		}
	}
	if len(l.events) >= 4094 {
		l.mu.Unlock()
		return ErrInvalidInput
	}
	in := claude.Input{Goal: append(json.RawMessage(nil), l.goal...), Instructions: append([]string(nil), l.instructions...), ContextRevision: int64(l.state.Snapshot().ContextRevision)}
	for _, t := range l.turns {
		if t.IsFinal {
			in.Transcript = append(in.Transcript, claude.Turn{Speaker: "business", Text: t.Text})
		}
	}
	if len(in.Transcript) == 0 {
		l.mu.Unlock()
		return nil
	}
	id := uuid()
	next, token, err := l.state.BeginResponse(id)
	if err != nil {
		l.mu.Unlock()
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	l.state = next
	l.cancel = cancel
	l.reply = Reply{ID: id, CallID: l.callID, ContextRevision: token.ContextRevision, Status: Generating, NeedsApproval: true, Reason: "Operator approval required before speech."}
	l.emitLocked()
	l.mu.Unlock()
	defer cancel()
	final := false
	callbackErr := error(nil)
	// The mutex also guards completion against concurrent or late callbacks from
	// a misbehaving provider. Never publish suggested until Generate succeeds.
	finished := false
	err = l.responder.Generate(ctx, in, func(update claude.Update) error {
		l.mu.Lock()
		defer l.mu.Unlock()
		if finished {
			return ErrStaleGeneration
		}
		if e := l.state.checkResponse(id, token); e != nil {
			return e
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if callbackErr != nil {
			return callbackErr
		}
		if final || len(l.events) >= 4094 || update.ContextRevision != in.ContextRevision || !utf8.ValidString(update.Text) || utf8.RuneCountInString(update.Text) > 1500 || (update.Final && strings.TrimSpace(update.Text) == "") {
			callbackErr = ErrInvalidInput
			return callbackErr
		}
		l.reply.Text = update.Text
		final = update.Final
		l.emitLocked()
		return nil
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	finished = true
	if e := l.state.checkResponse(id, token); e != nil {
		return ErrStaleGeneration
	}
	l.cancel = nil
	if err != nil || callbackErr != nil || !final || ctx.Err() != nil {
		l.state, _ = l.state.FailResponse(id, token)
		l.reply.Status = ResponseFailed
		l.reply.Text = ""
		l.emitLocked()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrGenerationFailed
	}
	l.state, err = l.state.MarkSuggested(id, token)
	if err != nil {
		return err
	}
	l.reply.Status = Suggested
	l.emitLocked()
	return nil
}
