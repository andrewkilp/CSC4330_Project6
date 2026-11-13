package mock

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	// North American 555-0100 through 555-0199 numbers are reserved for fiction.
	fictionalPhone = regexp.MustCompile(`^\+1[2-9]\d{2}55501\d{2}$`)
	responseOrder  = map[string]int{"generating": 0, "suggested": 1, "approved": 2, "speaking": 3, "spoken": 4}
	terminalCall   = map[string]bool{"ended": true, "failed": true, "cancelled": true}
	terminalReply  = map[string]bool{"spoken": true, "cancelled": true, "failed": true}
)

// ValidationError names the event and field that failed so a malformed fixture is easy to fix.
type ValidationError struct {
	Seq     uint64 // 0 for fixture-level problems
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Seq == 0 {
		return fmt.Sprintf("fixture %s: %s", e.Field, e.Message)
	}
	return fmt.Sprintf("event seq %d %s: %s", e.Seq, e.Field, e.Message)
}

// Encode renders a fixture as stable, indented JSON with a trailing newline.
func Encode(f Fixture) ([]byte, error) {
	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// Decode strictly parses fixture JSON. Unknown fields, unknown event types and
// trailing data are rejected; call Validate for the semantic checks.
func Decode(data []byte) (Fixture, error) {
	var f Fixture
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return Fixture{}, fmt.Errorf("decode fixture: %w", err)
	}
	if dec.More() {
		return Fixture{}, errors.New("decode fixture: unexpected data after the fixture object")
	}
	return f, nil
}

// UnmarshalJSON decodes the payload into the struct named by the event type.
func (e *Event) UnmarshalJSON(data []byte) error {
	type envelope Event
	var raw struct {
		envelope
		Payload json.RawMessage `json:"payload"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	*e = Event(raw.envelope)
	var target any
	switch e.Type {
	case "call.updated":
		target = &Call{}
	case "turn.updated":
		target = &Turn{}
	case "instruction.updated":
		target = &Instruction{}
	case "response.updated":
		target = &Response{}
	case "summary.ready":
		target = &Summary{}
	case "usage.updated":
		target = &Usage{}
	default:
		return fmt.Errorf("event seq %d type: unsupported fixture event type %q", e.Seq, e.Type)
	}
	pdec := json.NewDecoder(bytes.NewReader(raw.Payload))
	pdec.DisallowUnknownFields()
	if err := pdec.Decode(target); err != nil {
		return fmt.Errorf("event seq %d payload: %w", e.Seq, err)
	}
	switch v := target.(type) {
	case *Call:
		e.Payload = *v
	case *Turn:
		e.Payload = *v
	case *Instruction:
		e.Payload = *v
	case *Response:
		e.Payload = *v
	case *Summary:
		e.Payload = *v
	case *Usage:
		e.Payload = *v
	}
	return nil
}

type validator struct {
	f            Fixture
	seq          uint64
	callID       string
	lastAt       time.Time
	lastRevision uint64
	callStatus   string
	turns        map[string]Turn
	responses    map[string]Response
	// played holds each response's text once approved, for interrupted speech (#26).
	played       map[string]string
	instructions map[string]Instruction
	summary      *Summary
}

func (v *validator) bad(field, format string, args ...any) error {
	return &ValidationError{Seq: v.seq, Field: field, Message: fmt.Sprintf(format, args...)}
}

// Validate checks contract shape, ordering, state progressions, fictional data and the
// scenario requirements (an unanswered question and a path that needs operator input).
func Validate(f Fixture) error {
	v := &validator{f: f, turns: map[string]Turn{}, responses: map[string]Response{}, played: map[string]string{}, instructions: map[string]Instruction{}}
	if strings.TrimSpace(f.Scenario) == "" {
		return v.bad("scenario", "is required")
	}
	if !f.Fictional {
		return v.bad("fictional", "must be true; fixtures may contain only fictional data")
	}
	if len(f.Events) == 0 {
		return v.bad("events", "must contain at least one event")
	}
	for i, e := range f.Events {
		v.seq = e.Seq
		if e.Seq != uint64(i+1) {
			v.seq = uint64(i + 1)
			return v.bad("seq", "got %d, want %d (sequences start at 1 and have no gaps or duplicates)", e.Seq, i+1)
		}
		if err := v.event(e); err != nil {
			return err
		}
	}
	v.seq = 0
	return v.expectations()
}

func (v *validator) event(e Event) error {
	if !uuidPattern.MatchString(e.ID) {
		return v.bad("id", "%q is not a lowercase UUID", e.ID)
	}
	if v.callID == "" {
		v.callID = e.CallID
	}
	if !uuidPattern.MatchString(e.CallID) || e.CallID != v.callID {
		return v.bad("call_id", "%q must be a UUID shared by every event", e.CallID)
	}
	at, err := time.Parse(time.RFC3339Nano, e.OccurredAt)
	if err != nil || !strings.HasSuffix(e.OccurredAt, "Z") {
		return v.bad("occurred_at", "%q must be an RFC 3339 UTC timestamp ending in Z", e.OccurredAt)
	}
	if at.Before(v.lastAt) {
		return v.bad("occurred_at", "%s is earlier than the previous event", e.OccurredAt)
	}
	v.lastAt = at
	if e.ContextRevision < v.lastRevision {
		return v.bad("context_revision", "went backward from %d to %d", v.lastRevision, e.ContextRevision)
	}
	v.lastRevision = e.ContextRevision
	if v.seq == 1 && e.Type != "call.updated" {
		return v.bad("type", "the first event must be call.updated, got %q", e.Type)
	}
	if v.summary != nil {
		return v.bad("type", "no event may follow summary.ready")
	}
	if terminalCall[v.callStatus] && e.Type != "summary.ready" {
		return v.bad("type", "%s after the call reached %s", e.Type, v.callStatus)
	}
	switch p := e.Payload.(type) {
	case Call:
		return v.callEvent(e, p)
	case Turn:
		return v.turnEvent(p)
	case Instruction:
		return v.instructionEvent(e, p)
	case Response:
		return v.responseEvent(e, p)
	case Summary:
		return v.summaryEvent(p)
	case Usage:
		if e.Type != "usage.updated" || !p.EstimateOnly || p.EstimatedCostUSD != 0 || p.ElapsedSeconds < 0 {
			return v.bad("payload", "mock usage must be an estimate_only zero-cost usage.updated payload")
		}
		return nil
	default:
		return v.bad("payload", "unsupported payload for %q", e.Type)
	}
}

func (v *validator) callEvent(e Event, c Call) error {
	if e.Type != "call.updated" {
		return v.bad("type", "call payload requires call.updated")
	}
	if c.ID != v.callID || !uuidPattern.MatchString(c.OwnerID) {
		return v.bad("payload.id", "call id must match the envelope and owner_id must be a UUID")
	}
	if c.ContextRevision != e.ContextRevision {
		return v.bad("payload.context_revision", "%d does not match the envelope %d", c.ContextRevision, e.ContextRevision)
	}
	if c.Mode != "mock" {
		return v.bad("payload.mode", "fixtures must use mock mode, got %q", c.Mode)
	}
	if !strings.Contains(c.BusinessName, "(mock)") || len(c.BusinessName) > 120 {
		return v.bad("payload.business_name", "%q must be at most 120 characters and visibly labelled (mock)", c.BusinessName)
	}
	if !fictionalPhone.MatchString(c.Destination) {
		return v.bad("payload.destination", "%q is not a fictional +1 NXX-555-01XX number", c.Destination)
	}
	if c.Goal.ContactPhone != "" && !fictionalPhone.MatchString(c.Goal.ContactPhone) {
		return v.bad("payload.goal.contact_phone", "%q is not a fictional +1 NXX-555-01XX number", c.Goal.ContactPhone)
	}
	if err := v.goal(c.Goal); err != nil {
		return err
	}
	if c.ApprovalMode != "always" && c.ApprovalMode != "uncertain" {
		return v.bad("payload.approval_mode", "unknown approval mode %q", c.ApprovalMode)
	}
	allowed := map[string][]string{
		"":          {"draft"},
		"draft":     {"draft", "dialing", "ended", "failed", "cancelled"},
		"dialing":   {"dialing", "connected", "ended", "failed", "cancelled"},
		"connected": {"connected", "paused", "ended", "failed", "cancelled"},
		// Paused is a substate of connected (#1): the line stays open, the assistant is silent.
		"paused": {"paused", "connected", "ended", "failed", "cancelled"},
	}
	if !slices.Contains(allowed[v.callStatus], c.Status) {
		return v.bad("payload.status", "illegal call transition %q -> %q", v.callStatus, c.Status)
	}
	v.callStatus = c.Status
	return nil
}

func (v *validator) goal(g Goal) error {
	if _, err := time.LoadLocation(g.Timezone); err != nil || g.Timezone == "" {
		return v.bad("payload.goal.timezone", "%q is not an IANA time zone", g.Timezone)
	}
	if _, err := time.Parse(time.RFC3339, g.PreferredAt); err != nil {
		return v.bad("payload.goal.preferred_at", "%q must include an offset", g.PreferredAt)
	}
	switch g.Kind {
	case "restaurant":
		if g.PartySize == nil || *g.PartySize < 1 || *g.PartySize > 20 {
			return v.bad("payload.goal.party_size", "restaurant goals need a party size from 1 to 20")
		}
	case "appointment":
		if n := len(g.Service); n < 1 || n > 160 {
			return v.bad("payload.goal.service", "appointment goals need a service of 1 to 160 characters")
		}
	default:
		return v.bad("payload.goal.kind", "unknown goal kind %q", g.Kind)
	}
	if len(g.Constraints) > 12 || strings.TrimSpace(g.ContactName) == "" {
		return v.bad("payload.goal", "needs a contact name and at most 12 constraints")
	}
	return nil
}

func (v *validator) turnEvent(t Turn) error {
	if v.callStatus != "connected" && v.callStatus != "paused" {
		return v.bad("type", "transcript turn before the call connected")
	}
	if !uuidPattern.MatchString(t.ID) || t.CallID != v.callID {
		return v.bad("payload.id", "turn needs a UUID and the envelope call id")
	}
	if t.Speaker != "assistant" && t.Speaker != "business" {
		return v.bad("payload.speaker", "unknown speaker %q", t.Speaker)
	}
	if strings.TrimSpace(t.Text) == "" {
		return v.bad("payload.text", "turn text must not be blank")
	}
	if prev, ok := v.turns[t.ID]; ok {
		if prev.IsFinal {
			return v.bad("payload.is_final", "turn %s changed after it was final", t.ID)
		}
		if prev.Speaker != t.Speaker || prev.StartedAtMS != t.StartedAtMS {
			return v.bad("payload", "a partial update must keep the same speaker and start time")
		}
	}
	if t.IsFinal != (t.EndedAtMS != nil) || (t.EndedAtMS != nil && *t.EndedAtMS < t.StartedAtMS) {
		return v.bad("payload.ended_at_ms", "only final turns end, and not before they start")
	}
	if t.Speaker == "assistant" {
		r, ok := v.responses[t.ResponseID]
		spoken := ok && r.Status == "spoken" && r.Text == t.Text
		// Interrupted speech (#26): the words heard before the business cut in, a
		// whole-word prefix of a response that was playing and then cancelled.
		full := v.played[t.ResponseID]
		interrupted := ok && r.Status == "cancelled" && full != "" && t.Text != "" && strings.HasPrefix(full, t.Text) &&
			(len(t.Text) == len(full) || full[len(t.Text)] == ' ')
		if !t.IsFinal || !(spoken || interrupted) {
			return v.bad("payload.response_id", "assistant speech must be the final text of a spoken response, or the heard prefix of interrupted speech")
		}
	} else if t.ResponseID != "" {
		return v.bad("payload.response_id", "business turns cannot reference a response")
	}
	v.turns[t.ID] = t
	return nil
}

func (v *validator) instructionEvent(e Event, n Instruction) error {
	if n.CallID != v.callID || !uuidPattern.MatchString(n.ID) || n.ContextRevision != e.ContextRevision {
		return v.bad("payload", "instruction needs a UUID, the envelope call id and the envelope revision")
	}
	if l := len(n.Text); l < 1 || l > 1000 {
		return v.bad("payload.text", "instruction text must be 1 to 1000 characters")
	}
	prev, seen := v.instructions[n.ID]
	switch {
	case !seen && n.Status == "pending":
	case seen && prev.Status == "pending" && n.Status == "applied" && n.ContextRevision == prev.ContextRevision+1:
	case seen && prev.Status == "pending" && n.Status == "failed" && n.ContextRevision == prev.ContextRevision:
	default:
		return v.bad("payload.status", "instruction must go pending -> applied (next revision) or failed")
	}
	if n.Status == "applied" {
		for id, r := range v.responses {
			if !terminalReply[r.Status] {
				return v.bad("payload", "response %s is still active when the instruction applies", id)
			}
		}
	}
	v.instructions[n.ID] = n
	return nil
}

func (v *validator) responseEvent(e Event, r Response) error {
	if v.callStatus != "connected" && v.callStatus != "paused" {
		return v.bad("type", "response before the call connected")
	}
	if v.callStatus == "paused" && r.Status != "cancelled" {
		return v.bad("payload.status", "a paused call can only cancel responses, not %q them", r.Status)
	}
	if r.CallID != v.callID || !uuidPattern.MatchString(r.ID) || r.ContextRevision != e.ContextRevision {
		return v.bad("payload", "response needs a UUID, the envelope call id and the envelope revision")
	}
	// Contract (#1): text may be empty only while generating or once cancelled/failed.
	retired := r.Status == "cancelled" || r.Status == "failed"
	if len(r.Text) > 1500 || (r.Status != "generating" && !retired && strings.TrimSpace(r.Text) == "") {
		return v.bad("payload.text", "response text must be present after generation and at most 1500 characters")
	}
	if r.NeedsApproval && strings.TrimSpace(r.Reason) == "" {
		return v.bad("payload.reason", "a response that needs approval must say why")
	}
	prev, seen := v.responses[r.ID]
	if !seen {
		if r.Status != "generating" {
			return v.bad("payload.status", "a new response must start generating, got %q", r.Status)
		}
		for id, other := range v.responses {
			if !terminalReply[other.Status] {
				return v.bad("payload.id", "response %s is still active", id)
			}
		}
	} else {
		if terminalReply[prev.Status] {
			return v.bad("payload.status", "response %s reopened from %q", r.ID, prev.Status)
		}
		// The approval requirement is decided when generation finishes, then fixed.
		if prev.ContextRevision != r.ContextRevision || (prev.Status != "generating" && prev.NeedsApproval != r.NeedsApproval) {
			return v.bad("payload", "a response keeps its revision and approval requirement")
		}
		// Streamed drafts repeat generating with growing text.
		draft := prev.Status == "generating" && r.Status == "generating"
		if !retired && !draft && responseOrder[r.Status] != responseOrder[prev.Status]+1 {
			return v.bad("payload.status", "illegal response transition %q -> %q", prev.Status, r.Status)
		}
		if prev.Status != "generating" && !retired && prev.Text != r.Text {
			return v.bad("payload.text", "suggested text changed without a new response")
		}
	}
	if r.Status == "approved" || r.Status == "speaking" {
		v.played[r.ID] = r.Text
	}
	v.responses[r.ID] = r
	return nil
}

func (v *validator) summaryEvent(s Summary) error {
	if !terminalCall[v.callStatus] {
		return v.bad("type", "summary.ready before the call ended")
	}
	if s.CallID != v.callID {
		return v.bad("payload.call_id", "summary must belong to the call")
	}
	switch s.Outcome {
	case "confirmed", "declined", "needs_follow_up", "failed", "cancelled":
	default:
		return v.bad("payload.outcome", "unknown outcome %q", s.Outcome)
	}
	if s.Outcome == "confirmed" && len(s.EvidenceTurnIDs) == 0 {
		return v.bad("payload.evidence_turn_ids", "a confirmed outcome needs evidence")
	}
	for _, id := range s.EvidenceTurnIDs {
		t, ok := v.turns[id]
		if !ok || !t.IsFinal || t.Speaker != "business" {
			return v.bad("payload.evidence_turn_ids", "%s is not a finalized business turn", id)
		}
	}
	if d := s.ConfirmedDetails.ScheduledAt; d != "" {
		if _, err := time.Parse(time.RFC3339, d); err != nil {
			return v.bad("payload.confirmed_details.scheduled_at", "%q must include an offset", d)
		}
	}
	v.summary = &s
	return nil
}

func (v *validator) expectations() error {
	if v.summary == nil {
		return v.bad("events", "must end with summary.ready")
	}
	ex := v.f.Expected
	if ex.Outcome != v.summary.Outcome {
		return v.bad("expected.outcome", "%q does not match the summary outcome %q", ex.Outcome, v.summary.Outcome)
	}
	if len(ex.UnansweredQuestions) == 0 {
		return v.bad("expected.unanswered_questions", "each scenario needs at least one unanswered question")
	}
	for _, q := range ex.UnansweredQuestions {
		if !slices.Contains(v.summary.UnresolvedQuestions, q) {
			return v.bad("expected.unanswered_questions", "%q is missing from the summary", q)
		}
	}
	hasInstruction := false
	for _, in := range ex.OperatorInputs {
		r, ok := v.responses[in.ResponseID]
		if !ok || !r.NeedsApproval {
			return v.bad("expected.operator_inputs", "%s input targets %s, which is not a response that needs operator input", in.Kind, in.ResponseID)
		}
		switch in.Kind {
		case "instruction":
			hasInstruction = true
			if r.Status != "cancelled" || strings.TrimSpace(in.Text) == "" {
				return v.bad("expected.operator_inputs", "an instruction needs text and must replace the waiting response")
			}
		case "approval":
			if r.Status != "spoken" {
				return v.bad("expected.operator_inputs", "approved response %s was never spoken", in.ResponseID)
			}
		default:
			return v.bad("expected.operator_inputs", "unknown operator input kind %q", in.Kind)
		}
	}
	if !hasInstruction {
		return v.bad("expected.operator_inputs", "each scenario needs a path that requires an operator instruction")
	}
	return nil
}
