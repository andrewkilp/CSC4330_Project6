package mock

import (
	"fmt"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/conversation"
)

const timestampLayout = "2006-01-02T15:04:05.000Z"

// builder scripts one call. Every lifecycle and response step is checked against the
// pure state machine from #4, so fixtures cannot contain a transition the server rejects.
type builder struct {
	src       source
	base      time.Time
	elapsedMS int64
	connected int64
	state     conversation.State
	call      Call
	events    []Event
	expected  Expected
	turns     int
	responses int
	notes     int
	current   *Response
	token     conversation.Token
	err       error
}

func newBuilder(src source, base time.Time, call Call) *builder {
	call.ID = src.uuid("call")
	call.OwnerID = src.uuid("owner")
	call.Mode = "mock"
	call.Status = string(conversation.Draft)
	call.CreatedAt = base.UTC().Format(timestampLayout)
	call.UpdatedAt = call.CreatedAt
	b := &builder{src: src, base: base.UTC(), state: conversation.New(), call: call}
	b.emit("call.updated", b.call)
	return b
}

func (b *builder) fail(step string, err error) {
	if b.err == nil && err != nil {
		b.err = fmt.Errorf("mock scenario %q: %s: %w", b.src.scenario, step, err)
	}
}

func (b *builder) now() string {
	return b.base.Add(time.Duration(b.elapsedMS) * time.Millisecond).Format(timestampLayout)
}

// wait advances the scenario clock by a fixed base plus seeded jitter.
func (b *builder) wait(baseMS int64) {
	b.elapsedMS += baseMS + b.src.jitter(fmt.Sprintf("wait/%d", len(b.events)), 250)
}

func (b *builder) emit(kind string, payload any) {
	b.emitAt(b.state.Snapshot().ContextRevision, kind, payload)
}

func (b *builder) emitAt(revision uint64, kind string, payload any) {
	if b.err != nil {
		return
	}
	seq := uint64(len(b.events) + 1)
	b.events = append(b.events, Event{
		ID:              b.src.uuid(fmt.Sprintf("event/%d", seq)),
		CallID:          b.call.ID,
		Seq:             seq,
		Type:            kind,
		OccurredAt:      b.now(),
		ContextRevision: revision,
		Payload:         payload,
	})
}

func (b *builder) callMS() int64 { return b.elapsedMS - b.connected }

func (b *builder) transition(status conversation.CallStatus) {
	if b.err != nil {
		return
	}
	b.wait(800)
	next, err := b.state.Transition(status)
	if err != nil {
		b.fail("transition to "+string(status), err)
		return
	}
	b.state = next
	if status == conversation.Connected {
		b.connected = b.elapsedMS
	}
	b.call.Status = string(status)
	b.call.ContextRevision = b.state.Snapshot().ContextRevision
	b.call.UpdatedAt = b.now()
	b.emit("call.updated", b.call)
}

// business streams partial transcript updates for one turn, then its final text.
// It returns the turn id so a summary can cite it as evidence.
func (b *builder) business(partials []string, final string) string {
	b.turns++
	id := b.src.uuid(fmt.Sprintf("turn/%d", b.turns))
	b.wait(400)
	started := b.callMS()
	for _, text := range partials {
		b.wait(600)
		b.emit("turn.updated", Turn{ID: id, CallID: b.call.ID, Speaker: "business", Text: text, StartedAtMS: started})
	}
	b.wait(700)
	ended := b.callMS()
	b.emit("turn.updated", Turn{ID: id, CallID: b.call.ID, Speaker: "business", Text: final, IsFinal: true, StartedAtMS: started, EndedAtMS: &ended})
	return id
}

// suggest generates a response and stops at suggested.
func (b *builder) suggest(text string, needsApproval bool, reason string) string {
	if b.err != nil {
		return ""
	}
	b.responses++
	id := b.src.uuid(fmt.Sprintf("response/%d", b.responses))
	b.wait(300)
	next, token, err := b.state.BeginResponse(id)
	if err != nil {
		b.fail("begin response", err)
		return id
	}
	b.state, b.token = next, token
	b.current = &Response{ID: id, CallID: b.call.ID, ContextRevision: token.ContextRevision, Status: string(conversation.Generating), NeedsApproval: needsApproval, Reason: reason}
	b.emit("response.updated", *b.current)
	b.wait(900)
	if b.state, err = b.state.MarkSuggested(id, token); err != nil {
		b.fail("mark suggested", err)
		return id
	}
	b.current.Text = text
	b.current.Status = string(conversation.Suggested)
	b.emit("response.updated", *b.current)
	return id
}

// approve approves the current suggestion and plays it. operatorReason is empty when
// server policy approves a response that needs no operator input.
func (b *builder) approve(operatorReason string) {
	if b.err != nil || b.current == nil {
		return
	}
	r, token := b.current, b.token
	if operatorReason != "" {
		b.expected.OperatorInputs = append(b.expected.OperatorInputs, OperatorInput{Kind: "approval", ResponseID: r.ID, Reason: operatorReason})
		b.wait(2500)
	}
	var err error
	steps := []struct {
		status conversation.ResponseStatus
		apply  func() (conversation.State, error)
	}{
		{conversation.Approved, func() (conversation.State, error) { return b.state.Approve(r.ID, token, token.ContextRevision) }},
		{conversation.Speaking, func() (conversation.State, error) { return b.state.StartSpeaking(r.ID, token) }},
	}
	for _, step := range steps {
		b.wait(200)
		if b.state, err = step.apply(); err != nil {
			b.fail("advance response to "+string(step.status), err)
			return
		}
		r.Status = string(step.status)
		b.emit("response.updated", *r)
	}
	started := b.callMS()
	b.wait(int64(len(r.Text)) * 55)
	if b.state, err = b.state.MarkSpoken(r.ID, token); err != nil {
		b.fail("mark spoken", err)
		return
	}
	r.Status = string(conversation.Spoken)
	b.emit("response.updated", *r)
	// The transcript records speech only after playback acknowledgement.
	b.turns++
	ended := b.callMS()
	b.emit("turn.updated", Turn{ID: b.src.uuid(fmt.Sprintf("turn/%d", b.turns)), CallID: b.call.ID, Speaker: "assistant", Text: r.Text, IsFinal: true, StartedAtMS: started, EndedAtMS: &ended, ResponseID: r.ID})
	b.current = nil
}

// instruct records a private operator instruction. It cancels the waiting suggestion
// at the old revision, then applies the instruction at the next revision.
func (b *builder) instruct(text, reason string) {
	if b.err != nil {
		return
	}
	b.notes++
	before := b.state.Snapshot().ContextRevision
	cancelled := b.current
	if cancelled != nil {
		b.expected.OperatorInputs = append(b.expected.OperatorInputs, OperatorInput{Kind: "instruction", ResponseID: cancelled.ID, Text: text, Reason: reason})
	}
	b.wait(4000)
	note := Instruction{ID: b.src.uuid(fmt.Sprintf("instruction/%d", b.notes)), CallID: b.call.ID, Text: text, ContextRevision: before, Status: "pending", CreatedAt: b.now()}
	b.emitAt(before, "instruction.updated", note)
	next, err := b.state.ApplyInstruction(before)
	if err != nil {
		b.fail("apply instruction", err)
		return
	}
	if cancelled != nil {
		cancelled.Status = string(conversation.ResponseCancelled)
		b.wait(50)
		b.emitAt(before, "response.updated", *cancelled)
		b.current = nil
	}
	b.state = next
	b.wait(100)
	note.ContextRevision = b.state.Snapshot().ContextRevision
	note.Status = "applied"
	b.emit("instruction.updated", note)
}

// end hangs up, reports a zero-cost mock usage estimate and publishes the summary.
func (b *builder) end(summary Summary) {
	b.wait(600)
	b.emit("usage.updated", Usage{ElapsedSeconds: b.callMS() / 1000, EstimatedCostUSD: 0, EstimateOnly: true})
	b.transition(conversation.Ended)
	summary.CallID = b.call.ID
	b.wait(1200)
	b.emit("summary.ready", summary)
	b.expected.Outcome = summary.Outcome
}
