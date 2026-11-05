// Package demo runs fictional conversations locally. It never calls a provider.
package demo

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	c "github.com/andrewkilp/CSC4330_Project6/packages/go/conversation"
)

var (
	ErrNotFound = errors.New("demo session not found")
	ErrConflict = errors.New("the conversation changed; refresh and try again")
	ErrInput    = errors.New("choose a valid scenario or enter a bounded instruction or reply")
	ErrCapacity = errors.New("demo session limit reached; restart the demo server")
)

type Action struct {
	Action     string `json:"action"`
	Text       string `json:"text,omitempty"`
	ResponseID string `json:"response_id,omitempty"`
	Revision   uint64 `json:"context_revision"`
}
type Event struct {
	ID       string          `json:"id"`
	CallID   string          `json:"call_id"`
	Seq      int             `json:"seq"`
	Type     string          `json:"type"`
	At       string          `json:"occurred_at"`
	Revision uint64          `json:"context_revision"`
	Payload  json.RawMessage `json:"payload"`
}
type Turn struct {
	ID         string `json:"id"`
	CallID     string `json:"call_id"`
	Speaker    string `json:"speaker"`
	Text       string `json:"text"`
	Final      bool   `json:"is_final"`
	Start      int64  `json:"started_at_ms"`
	End        *int64 `json:"ended_at_ms,omitempty"`
	ResponseID string `json:"response_id,omitempty"`
}
type Reply struct {
	ID            string           `json:"id"`
	CallID        string           `json:"call_id"`
	Text          string           `json:"text"`
	Revision      uint64           `json:"context_revision"`
	Status        c.ResponseStatus `json:"status"`
	NeedsApproval bool             `json:"needs_approval"`
	Reason        string           `json:"reason,omitempty"`
}
type Instruction struct {
	ID       string `json:"id"`
	CallID   string `json:"call_id"`
	Text     string `json:"text"`
	Revision uint64 `json:"context_revision"`
	Status   string `json:"status"`
	At       string `json:"created_at"`
}
type session struct {
	id, scenario, phase, notice, directive string
	state                                  c.State
	created                                time.Time
	finished                               time.Time
	events                                 []Event
	turns                                  []Turn
	replies                                []Reply
	instructions                           []Instruction
	summary                                json.RawMessage
	work, stream                           uint64
}
type Service struct {
	mu       sync.Mutex
	sessions map[string]*session
	step     time.Duration
}

func New(step time.Duration) *Service {
	if step <= 0 {
		step = 180 * time.Millisecond
	}
	return &Service{sessions: make(map[string]*session), step: step}
}
func uuid() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func encode(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func (s *session) emit(kind string, payload any, revision uint64) {
	s.events = append(s.events, Event{uuid(), s.id, len(s.events) + 1, kind, now(), revision, encode(payload)})
}
func (s *session) call() map[string]any {
	kind, business, preferred, service := "restaurant", "Marigold", "2026-11-13T19:00:00-06:00", ""
	goal := map[string]any{"kind": kind, "preferred_at": preferred, "timezone": "America/Chicago", "party_size": 4, "constraints": []string{"Confirm preferences before booking"}, "contact_name": "Demo Guest"}
	if s.scenario == "appointment" {
		kind, business, preferred, service = "appointment", "Juniper Studio", "2026-11-12T15:00:00-06:00", "Haircut"
		goal["kind"] = kind
		goal["preferred_at"] = preferred
		goal["service"] = service
		delete(goal, "party_size")
	}
	return map[string]any{"id": s.id, "owner_id": "22222222-2222-4222-8222-222222222222", "mode": "mock", "destination": "+12025550123", "business_name": business, "goal": goal, "status": s.state.Status(), "approval_mode": "always", "context_revision": s.state.Snapshot().ContextRevision, "created_at": s.created.UTC().Format(time.RFC3339Nano), "updated_at": now()}
}
func (s *session) callEvent()         { s.emit("call.updated", s.call(), s.state.Snapshot().ContextRevision) }
func (s *session) replyEvent(r Reply) { s.emit("response.updated", r, r.Revision) }
func (s *session) addTurn(speaker, text, responseID string) {
	elapsed := time.Since(s.created).Milliseconds()
	t := Turn{ID: uuid(), CallID: s.id, Speaker: speaker, Text: text, Final: true, Start: elapsed, End: &elapsed, ResponseID: responseID}
	s.turns = append(s.turns, t)
	s.emit("turn.updated", t, s.state.Snapshot().ContextRevision)
}
func (svc *Service) Start(scenario string) ([]byte, error) {
	if scenario != "restaurant" && scenario != "appointment" {
		return nil, ErrInput
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if len(svc.sessions) >= 32 {
		return nil, ErrCapacity
	}
	s := &session{id: uuid(), scenario: scenario, phase: "listening", notice: "The fictional business is answering. Watch the transcript arrive.", state: c.New(), created: time.Now(), events: []Event{}, turns: []Turn{}, replies: []Reply{}, instructions: []Instruction{}}
	svc.sessions[s.id] = s
	s.callEvent()
	s.state, _ = s.state.Transition(c.Dialing)
	s.callEvent()
	s.state, _ = s.state.Transition(c.Connected)
	s.callEvent()
	intro, question := "Hi, this is Switchboard, an AI assistant calling for Demo Guest. I'd like a table for four this Friday around 7 pm.", "Thanks for calling Marigold. Seven is fully booked, but 7:30 is available. Would you prefer indoor or patio seating?"
	if scenario == "appointment" {
		intro = "Hi, this is Switchboard, an AI assistant calling for Demo Guest. I'd like to schedule a haircut on Thursday afternoon."
		question = "Juniper Studio here. We have Thursday at 3 pm. Would your guest like a full haircut or just a trim?"
	}
	s.addTurn("assistant", intro, "")
	svc.streamBusiness(s, question, false, "", nil)
	return svc.frame(s, 0), nil
}
func (svc *Service) Read(id string, after int) ([]byte, error) {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	s, ok := svc.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	if after < 0 || after > len(s.events) {
		return nil, ErrInput
	}
	return svc.frame(s, after), nil
}
func (svc *Service) frame(s *session, after int) []byte {
	snapshot := map[string]any{"call": s.call(), "last_seq": len(s.events), "turns": s.turns, "instructions": s.instructions, "responses": s.replies}
	if len(s.summary) > 0 {
		snapshot["summary"] = s.summary
	}
	elapsed := time.Since(s.created)
	if !s.finished.IsZero() {
		elapsed = s.finished.Sub(s.created)
	}
	return encode(map[string]any{"snapshot": snapshot, "events": s.events[after:], "demo": map[string]any{"scenario": s.scenario, "phase": s.phase, "paused": s.state.Snapshot().Paused, "notice": s.notice, "elapsed_seconds": int(elapsed.Seconds())}})
}
func (s *session) cancelReply() {
	if len(s.replies) == 0 {
		return
	}
	i := len(s.replies) - 1
	r := s.replies[i]
	if r.Status == c.Spoken || r.Status == c.ResponseCancelled || r.Status == c.ResponseFailed {
		return
	}
	r.Status = c.ResponseCancelled
	s.replies[i] = r
	s.replyEvent(r)
}
func (s *session) suggestion() {
	if s.state.Snapshot().Paused || s.state.Status() != c.Connected {
		return
	}
	text := "7:30 works. We'd like patio seating for four, please."
	if s.scenario == "appointment" {
		text = "Thursday at 3 pm works. A full haircut, please."
	}
	d := strings.ToLower(s.directive)
	switch {
	case strings.Contains(d, "decline") || strings.Contains(d, "don't book") || strings.Contains(d, "do not book") || strings.Contains(d, "cancel"):
		text = "No thank you. Please don't make a booking."
	case s.scenario == "restaurant" && (strings.Contains(d, "indoor") || strings.Contains(d, "inside seating") || strings.Contains(d, "sit inside")):
		text = "7:30 works. We'd like indoor seating for four, please."
	case s.scenario == "restaurant" && (strings.Contains(d, "patio") || strings.Contains(d, "outside seating") || strings.Contains(d, "outdoor seating") || strings.Contains(d, "sit outside")):
		text = "7:30 works. We'd like patio seating for four, please."
	case s.scenario == "appointment" && strings.Contains(d, "trim"):
		text = "Thursday at 3 pm works. Just a trim, please."
	case s.scenario == "appointment" && (strings.Contains(d, "full haircut") || strings.Contains(d, "full cut")):
		text = "Thursday at 3 pm works. A full haircut, please."
	case strings.TrimSpace(d) != "":
		text = "I need to check a detail with my guest before confirming. Could you hold for a moment?"
	}
	id := uuid()
	next, _, err := s.state.BeginResponse(id)
	if err != nil {
		return
	}
	s.state = next
	r := Reply{ID: id, CallID: s.id, Text: "", Revision: s.state.Snapshot().ContextRevision, Status: c.Generating, NeedsApproval: true, Reason: "Review this simulated reply before it is delivered."}
	s.replies = append(s.replies, r)
	s.replyEvent(r)
	token := s.state.Snapshot().Response.Token
	s.state, _ = s.state.MarkSuggested(id, token)
	r.Text = text
	r.Status = c.Suggested
	s.replies[len(s.replies)-1] = r
	s.replyEvent(r)
	s.phase = "awaiting_approval"
	s.notice = "A question needs your direction. Steer the reply, edit it, then approve."
	if s.directive != "" {
		s.notice = "Instruction applied. The previous suggestion is invalid. Review the revised reply."
	}
}
func (svc *Service) Act(id string, a Action) ([]byte, error) {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	s, ok := svc.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	if s.state.Status() != c.Connected {
		return nil, ErrConflict
	}
	before := len(s.events)
	switch a.Action {
	case "instruction":
		if len(strings.TrimSpace(a.Text)) == 0 || len(a.Text) > 1000 {
			return nil, ErrInput
		}
		if s.phase != "awaiting_approval" && s.phase != "speaking" {
			return nil, ErrConflict
		}
		next, err := s.state.ApplyInstruction(a.Revision)
		if err != nil {
			return nil, ErrConflict
		}
		s.state = next
		s.work++
		s.cancelReply()
		s.directive = strings.TrimSpace(a.Text)
		i := Instruction{uuid(), s.id, s.directive, s.state.Snapshot().ContextRevision, "applied", now()}
		s.instructions = append(s.instructions, i)
		s.emit("instruction.updated", i, i.Revision)
		s.phase = "awaiting_approval"
		s.suggestion()
		if s.state.Snapshot().Paused {
			s.notice = "Instruction applied while paused. Resume to see the revised reply."
		}
	case "approve":
		if len(strings.TrimSpace(a.Text)) == 0 || len(a.Text) > 1500 {
			return nil, ErrInput
		}
		if s.phase != "awaiting_approval" || len(s.replies) == 0 {
			return nil, ErrConflict
		}
		i := len(s.replies) - 1
		r := s.replies[i]
		token := s.state.Snapshot().Response.Token
		next, err := s.state.Approve(a.ResponseID, token, a.Revision)
		if err != nil {
			return nil, ErrConflict
		}
		s.state = next
		r.Text = strings.TrimSpace(a.Text)
		r.Status = c.Approved
		s.replies[i] = r
		s.replyEvent(r)
		s.state, _ = s.state.StartSpeaking(r.ID, token)
		r.Status = c.Speaking
		s.replies[i] = r
		s.replyEvent(r)
		s.phase = "speaking"
		s.notice = "Delivering your approved reply in the simulation…"
		s.work++
		work := s.work
		go svc.finishReply(s.id, r.ID, token, work)
	case "pause":
		next, err := s.state.Pause()
		if err != nil {
			return nil, ErrConflict
		}
		s.state = next
		s.work++
		s.cancelReply()
		if s.phase == "speaking" {
			s.phase = "awaiting_approval"
		}
		s.notice = "Demo paused. You can change an instruction before resuming."
		s.callEvent()
	case "resume":
		next, err := s.state.Resume()
		if err != nil {
			return nil, ErrConflict
		}
		s.state = next
		if s.phase == "awaiting_approval" {
			s.suggestion()
		} else {
			s.notice = "Demo resumed. The transcript will continue."
		}
		s.callEvent()
	case "end":
		s.state, _ = s.state.Transition(c.Ended)
		s.finished = time.Now()
		s.work++
		s.stream++
		s.cancelReply()
		s.callEvent()
		s.phase = "ended"
		s.notice = "Demo ended. No real phone call was made."
		svc.summary(s, "cancelled", "", nil)
	default:
		return nil, ErrInput
	}
	return svc.frame(s, before), nil
}
func (svc *Service) finishReply(id, responseID string, token c.Token, work uint64) {
	time.Sleep(7 * svc.step)
	svc.mu.Lock()
	defer svc.mu.Unlock()
	s := svc.sessions[id]
	if s == nil || s.work != work {
		return
	}
	next, err := s.state.MarkSpoken(responseID, token)
	if err != nil {
		return
	}
	s.state = next
	i := len(s.replies) - 1
	r := s.replies[i]
	r.Status = c.Spoken
	s.replies[i] = r
	s.replyEvent(r)
	s.addTurn("assistant", r.Text, r.ID)
	answer, outcome, details := confirmation(s, r.Text)
	s.phase = "confirming"
	s.notice = "The fictional business is responding…"
	svc.streamBusiness(s, answer, true, outcome, details)
}
func confirmation(s *session, text string) (string, string, map[string]any) {
	d := strings.ToLower(text)
	details := map[string]any{"business_name": s.call()["business_name"]}
	if strings.Contains(d, "don't make a booking") || strings.Contains(d, "don't book") || strings.Contains(d, "do not book") || strings.Contains(d, "no thank") {
		return "Understood. We won't make a booking. Thanks for letting us know.", "declined", details
	}
	if s.scenario == "restaurant" && strings.Contains(d, "7:30 works") && (strings.Contains(d, "indoor") || strings.Contains(d, "patio")) {
		seating := "patio"
		if strings.Contains(d, "indoor") {
			seating = "indoor"
		}
		details["scheduled_at"] = "2026-11-13T19:30:00-06:00"
		details["timezone"] = "America/Chicago"
		details["party_size"] = 4
		details["confirmation_reference"] = "DEMO-MAR-014"
		return "You're all set: Friday at 7:30 pm, four guests, " + seating + " seating. Your demo confirmation is MAR-014.", "confirmed", details
	}
	if s.scenario == "appointment" && strings.Contains(d, "3 pm works") && (strings.Contains(d, "trim") || strings.Contains(d, "full haircut")) {
		service := "full haircut"
		if strings.Contains(d, "trim") {
			service = "trim"
		}
		details["scheduled_at"] = "2026-11-12T15:00:00-06:00"
		details["timezone"] = "America/Chicago"
		details["service"] = service
		details["confirmation_reference"] = "DEMO-JUN-028"
		return "Confirmed for Thursday at 3 pm: a " + service + " at Juniper Studio. Your demo confirmation is JUN-028.", "confirmed", details
	}
	return "Of course. We can wait for clarification. Nothing is booked yet.", "needs_follow_up", details
}
func (svc *Service) streamBusiness(s *session, text string, finish bool, outcome string, details map[string]any) {
	s.stream++
	stream := s.stream
	id := s.id
	turn := Turn{ID: uuid(), CallID: id, Speaker: "business", Start: time.Since(s.created).Milliseconds()}
	words := strings.Fields(text)
	go func() {
		index := 0
		for index < len(words) {
			time.Sleep(svc.step)
			svc.mu.Lock()
			current := svc.sessions[id]
			if current == nil || current.stream != stream || current.state.Status().Terminal() {
				svc.mu.Unlock()
				return
			}
			if current.state.Snapshot().Paused {
				svc.mu.Unlock()
				continue
			}
			index += 3
			if index > len(words) {
				index = len(words)
			}
			turn.Text = strings.Join(words[:index], " ")
			turn.Final = index == len(words)
			if turn.Final {
				end := time.Since(current.created).Milliseconds()
				turn.End = &end
			}
			found := false
			for i, t := range current.turns {
				if t.ID == turn.ID {
					current.turns[i] = turn
					found = true
					break
				}
			}
			if !found {
				current.turns = append(current.turns, turn)
			}
			current.emit("turn.updated", turn, current.state.Snapshot().ContextRevision)
			if turn.Final {
				if finish {
					current.state, _ = current.state.Transition(c.Ended)
					current.finished = time.Now()
					current.callEvent()
					current.phase = "ended"
					current.notice = "Simulation complete. Review the outcome and transcript below."
					if details == nil {
						details = map[string]any{"business_name": current.call()["business_name"]}
					}
					svc.summary(current, outcome, turn.ID, details)
				} else {
					current.suggestion()
				}
			}
			svc.mu.Unlock()
		}
	}()
}
func (svc *Service) summary(s *session, outcome, evidence string, details map[string]any) {
	if details == nil {
		details = map[string]any{"business_name": s.call()["business_name"]}
	}
	ids, questions, next := []string{}, []string{}, []string{}
	if evidence != "" {
		ids = append(ids, evidence)
	}
	if outcome == "needs_follow_up" || outcome == "cancelled" {
		questions = append(questions, "The booking was not confirmed.")
		next = append(next, "Start another demo to try a different response.")
	}
	s.summary = encode(map[string]any{"call_id": s.id, "outcome": outcome, "confirmed_details": details, "evidence_turn_ids": ids, "unresolved_questions": questions, "next_steps": next})
	s.emit("summary.ready", s.summary, s.state.Snapshot().ContextRevision)
}
