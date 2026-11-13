package mock

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/claude"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/conversation"
)

var ErrUnscripted = errors.New("mock scenario has no reply for this context")

type scriptedReply struct {
	business     string
	instructions []string
	text         string
}

// Responder is network-free and immutable. It derives reply text from #5's
// fixtures, never from an independent second copy of the scenario scripts.
type Responder struct {
	goal    string
	replies []scriptedReply
}

var _ conversation.Responder = (*Responder)(nil)

func NewResponder(name string, seed uint64) (*Responder, error) {
	f, err := Generate(name, seed)
	if err != nil {
		return nil, err
	}
	r := &Responder{}
	var business string
	var instructions []string
	for _, event := range f.Events {
		switch p := event.Payload.(type) {
		case Call:
			if r.goal == "" {
				b, _ := json.Marshal(p.Goal)
				r.goal = string(b)
			}
		case Turn:
			if p.IsFinal && p.Speaker == "business" {
				business = p.Text
			}
		case Instruction:
			if p.Status == "applied" {
				instructions = append(instructions, p.Text)
			}
		case Response:
			if p.Status == "suggested" {
				r.replies = append(r.replies, scriptedReply{business, append([]string(nil), instructions...), p.Text})
			}
		}
	}
	return r, nil
}

func (r *Responder) Generate(ctx context.Context, in claude.Input, emit func(claude.Update) error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if emit == nil || len(in.Transcript) == 0 || in.ContextRevision != int64(len(in.Instructions)) {
		return ErrUnscripted
	}
	var goal Goal
	if json.Unmarshal(in.Goal, &goal) != nil {
		return ErrUnscripted
	}
	encoded, _ := json.Marshal(goal)
	if string(encoded) != r.goal {
		return ErrUnscripted
	}
	last := in.Transcript[len(in.Transcript)-1]
	if last.Speaker != "business" {
		return ErrUnscripted
	}
	for _, reply := range r.replies {
		if reply.business != last.Text || !slices.Equal(reply.instructions, in.Instructions) {
			continue
		}
		// Cumulative drafts use the same boundary as Claude, with no sleeps or audio.
		text := []rune(reply.text)
		if err := emit(claude.Update{Text: string(text[:len(text)/2]), ContextRevision: in.ContextRevision}); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return emit(claude.Update{Text: reply.text, ContextRevision: in.ContextRevision, Final: true})
	}
	return ErrUnscripted
}

// NewConversation explicitly selects mock generation. Real call composition
// must supply its own responder; there is no silent fallback from real to mock.
func NewConversation(name string, seed uint64) (*conversation.Loop, Fixture, error) {
	f, err := Generate(name, seed)
	if err != nil {
		return nil, Fixture{}, err
	}
	r, err := NewResponder(name, seed)
	if err != nil {
		return nil, Fixture{}, err
	}
	call := f.Events[0].Payload.(Call)
	goal, _ := json.Marshal(call.Goal)
	loop, err := conversation.NewLoop(call.ID, goal, r)
	if err != nil {
		return nil, Fixture{}, err
	}
	for _, status := range []conversation.CallStatus{conversation.Dialing, conversation.Connected} {
		if err = loop.Transition(status); err != nil {
			return nil, Fixture{}, err
		}
	}
	return loop, f, nil
}

// RunConversation emits actual loop-generated response events, stopping at the
// first approval boundary. Fixture approval/audio events are never replayed.
func RunConversation(ctx context.Context, name string, seed uint64, out io.Writer) error {
	loop, f, err := NewConversation(name, seed)
	if err != nil {
		return err
	}
	defer loop.Transition(conversation.Ended)
	for _, event := range f.Events {
		turn, ok := event.Payload.(Turn)
		if !ok || turn.Speaker != "business" {
			continue
		}
		if err = loop.ObserveBusinessTurn(ctx, conversation.BusinessTurn{ID: turn.ID, CallID: turn.CallID, Text: turn.Text, IsFinal: turn.IsFinal}); err != nil {
			return err
		}
		if loop.Snapshot().Response == nil {
			continue
		}
		encoder := json.NewEncoder(out)
		for _, event := range loop.Events() {
			if err = encoder.Encode(event); err != nil {
				return err
			}
		}
		return nil
	}
	return ErrUnscripted
}
