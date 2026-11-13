package calls

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

// Error is a user-safe API failure. Message never contains request values.
type Error struct {
	Status  int
	Code    string
	Message string
	Field   string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func invalid(field string) *Error {
	return &Error{400, "invalid_argument", "Check the highlighted field and try again.", field}
}
func conflict(field, message string) *Error { return &Error{409, "conflict", message, field} }

var (
	errNotFound    = &Error{404, "not_found", "Call not found.", ""}
	errInternal    = &Error{500, "internal", "The request could not be completed.", ""}
	errUnavailable = &Error{503, "provider_unavailable", "Real calling is unavailable right now. You can start a mock call instead.", ""}
	errNotActive   = conflict("status", "This call is not active on the server.")
)

var (
	e164       = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)
	fictional  = regexp.MustCompile(`^\+1[2-9][0-9]{2}55501[0-9]{2}$`)
	uuidInput  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	fieldName  = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
	offsetTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$`)
)

// units counts UTF-16 code units, matching JavaScript length (docs/contracts.md).
func units(s string) int { return len(utf16.Encode([]rune(s))) }

func boundedText(s string, max int) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && units(s) <= max
}

// decodeObject strictly decodes a JSON object of at most 16 KiB, rejecting unknown or
// duplicate fields by name.
func decodeObject(data []byte, allowed ...string) (map[string]json.RawMessage, *Error) {
	if len(data) > 16*1024 || !utf8.Valid(data) {
		return nil, invalid("request")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
		return nil, invalid("request")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		tok, err := d.Token()
		key, ok := tok.(string)
		if err != nil || !ok {
			return nil, invalid("request")
		}
		known := false
		for _, a := range allowed {
			known = known || a == key
		}
		if !known {
			if len(key) > 80 || !fieldName.MatchString(key) {
				key = "request"
			}
			return nil, invalid(key)
		}
		if _, dup := fields[key]; dup {
			return nil, invalid(key)
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return nil, invalid(key)
		}
		fields[key] = raw
	}
	if _, err := d.Token(); err != nil {
		return nil, invalid("request")
	}
	if d.More() {
		return nil, invalid("request")
	}
	return fields, nil
}

func str(fields map[string]json.RawMessage, key string, required bool) (string, bool, *Error) {
	raw, ok := fields[key]
	if !ok {
		if required {
			return "", false, invalid(key)
		}
		return "", false, nil
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false, invalid(key)
	}
	return s, true, nil
}

// CreateRequest is a validated POST /v1/calls body.
type CreateRequest struct {
	Call           repository.Draft
	IdempotencyKey string
}

// ParseCreate validates POST /v1/calls against docs/contracts.md.
func ParseCreate(data []byte) (CreateRequest, *Error) {
	f, e := decodeObject(data, "mode", "destination", "business_name", "goal", "approval_mode", "consent_acknowledged", "idempotency_key")
	if e != nil {
		return CreateRequest{}, e
	}
	var out CreateRequest
	c := &out.Call
	var err *Error
	if c.Mode, _, err = str(f, "mode", true); err != nil || (c.Mode != "mock" && c.Mode != "real") {
		return out, invalid("mode")
	}
	if c.Destination, _, err = str(f, "destination", true); err != nil || !e164.MatchString(c.Destination) ||
		(c.Mode == "mock" && !fictional.MatchString(c.Destination)) {
		return out, invalid("destination")
	}
	if c.BusinessName, _, err = str(f, "business_name", true); err != nil || !boundedText(c.BusinessName, 120) {
		return out, invalid("business_name")
	}
	approval, present, err := str(f, "approval_mode", false)
	if err != nil || (present && approval != "always" && approval != "uncertain") {
		return out, invalid("approval_mode")
	}
	if !present {
		approval = "always"
	}
	c.ApprovalMode = approval
	if string(f["consent_acknowledged"]) != "true" {
		return out, invalid("consent_acknowledged")
	}
	c.ConsentAcknowledged = true
	if out.IdempotencyKey, _, err = str(f, "idempotency_key", true); err != nil || !uuidInput.MatchString(out.IdempotencyKey) {
		return out, invalid("idempotency_key")
	}
	raw, ok := f["goal"]
	if !ok {
		return out, invalid("goal")
	}
	goal, e := parseGoal(raw)
	if e != nil {
		return out, e
	}
	c.Goal = goal
	return out, nil
}

func parseGoal(raw json.RawMessage) (repository.Goal, *Error) {
	f, e := decodeObject(raw, "kind", "preferred_at", "timezone", "party_size", "service", "constraints", "contact_name", "contact_phone")
	if e != nil {
		if e.Field == "request" {
			e.Field = "goal"
		} else {
			e.Field = "goal." + e.Field
		}
		return repository.Goal{}, e
	}
	var g repository.Goal
	var err *Error
	if g.Kind, _, err = str(f, "kind", true); err != nil || (g.Kind != "restaurant" && g.Kind != "appointment") {
		return g, invalid("goal.kind")
	}
	if g.PreferredAt, _, err = str(f, "preferred_at", true); err != nil || !offsetTime.MatchString(g.PreferredAt) {
		return g, invalid("goal.preferred_at")
	}
	if _, perr := time.Parse(time.RFC3339, g.PreferredAt); perr != nil {
		return g, invalid("goal.preferred_at")
	}
	if g.Timezone, _, err = str(f, "timezone", true); err != nil || g.Timezone == "" || g.Timezone == "Local" || len(g.Timezone) > 100 {
		return g, invalid("goal.timezone")
	}
	loc, lerr := time.LoadLocation(g.Timezone)
	if lerr != nil {
		return g, invalid("goal.timezone")
	}
	// The offset must be the business zone's offset at that moment.
	at, _ := time.Parse(time.RFC3339, g.PreferredAt)
	if _, have := at.Zone(); func() bool { _, want := at.In(loc).Zone(); return have != want }() {
		return g, invalid("goal.preferred_at")
	}
	if raw, ok := f["party_size"]; ok {
		var n int
		if json.Unmarshal(raw, &n) != nil || n < 1 || n > 20 || g.Kind != "restaurant" {
			return g, invalid("goal.party_size")
		}
		g.PartySize = &n
	} else if g.Kind == "restaurant" {
		return g, invalid("goal.party_size")
	}
	if s, ok, err := str(f, "service", false); err != nil || (ok && (!boundedText(s, 160) || g.Kind != "appointment")) {
		return g, invalid("goal.service")
	} else if ok {
		g.Service = s
	} else if g.Kind == "appointment" {
		return g, invalid("goal.service")
	}
	g.Constraints = []string{}
	if raw, ok := f["constraints"]; ok {
		if json.Unmarshal(raw, &g.Constraints) != nil || len(g.Constraints) > 12 {
			return g, invalid("goal.constraints")
		}
		for _, s := range g.Constraints {
			if !boundedText(s, 200) {
				return g, invalid("goal.constraints")
			}
		}
	}
	if g.ContactName, _, err = str(f, "contact_name", true); err != nil || !boundedText(g.ContactName, 120) {
		return g, invalid("goal.contact_name")
	}
	if s, ok, err := str(f, "contact_phone", false); err != nil || (ok && !e164.MatchString(s)) {
		return g, invalid("goal.contact_phone")
	} else if ok {
		g.ContactPhone = s
	}
	return g, nil
}

// Action is a validated mutation body with an idempotency key.
type Action struct {
	Key string
	// Request is the idempotency-hashed request body, without the key.
	Request map[string]any
	// Instruction
	Text             string
	ExpectedRevision int64
	// Approval
	ContextRevision int64
	EditedText      *string
	// Control
	Control string
}

func keyed(f map[string]json.RawMessage) (string, *Error) {
	key, _, err := str(f, "idempotency_key", true)
	if err != nil || !uuidInput.MatchString(key) {
		return "", invalid("idempotency_key")
	}
	return key, nil
}

func revision(f map[string]json.RawMessage, key string) (int64, *Error) {
	var n int64
	raw, ok := f[key]
	if !ok || json.Unmarshal(raw, &n) != nil || n < 0 || n > 1<<53-1 {
		return 0, invalid(key)
	}
	return n, nil
}

func hashOf(parts ...any) []byte {
	data, _ := json.Marshal(parts)
	sum := sha256.Sum256(data)
	return sum[:]
}

func ParseStart(data []byte) (Action, *Error) {
	f, e := decodeObject(data, "idempotency_key")
	if e != nil {
		return Action{}, e
	}
	key, e := keyed(f)
	return Action{Key: key, Request: map[string]any{}}, e
}

func ParseInstruction(data []byte) (Action, *Error) {
	f, e := decodeObject(data, "text", "expected_revision", "idempotency_key")
	if e != nil {
		return Action{}, e
	}
	var a Action
	var err *Error
	if a.Text, _, err = str(f, "text", true); err != nil || !boundedText(a.Text, 1000) {
		return a, invalid("text")
	}
	if a.ExpectedRevision, e = revision(f, "expected_revision"); e != nil {
		return a, e
	}
	if a.Key, e = keyed(f); e != nil {
		return a, e
	}
	a.Request = map[string]any{"text": a.Text, "expected_revision": a.ExpectedRevision}
	return a, nil
}

func ParseApproval(data []byte) (Action, *Error) {
	f, e := decodeObject(data, "context_revision", "edited_text", "idempotency_key")
	if e != nil {
		return Action{}, e
	}
	var a Action
	if a.ContextRevision, e = revision(f, "context_revision"); e != nil {
		return a, e
	}
	if s, ok, err := str(f, "edited_text", false); err != nil || (ok && !boundedText(s, 1500)) {
		return a, invalid("edited_text")
	} else if ok {
		a.EditedText = &s
	}
	if a.Key, e = keyed(f); e != nil {
		return a, e
	}
	return a, nil
}

func ParseControl(data []byte) (Action, *Error) {
	f, e := decodeObject(data, "action", "idempotency_key")
	if e != nil {
		return Action{}, e
	}
	var a Action
	var err *Error
	if a.Control, _, err = str(f, "action", true); err != nil || (a.Control != "pause" && a.Control != "resume" && a.Control != "end") {
		return a, invalid("action")
	}
	if a.Key, e = keyed(f); e != nil {
		return a, e
	}
	a.Request = map[string]any{"action": a.Control}
	return a, nil
}
