// Package repository stores owner-scoped calls and their durable events in Postgres.
// Service layers own lifecycle/approval policy; this package owns atomicity, owner
// fences, immutable finalized turns and idempotency at the database boundary.
package repository

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

var (
	ErrNotFound    = errors.New("resource not found")
	ErrConflict    = errors.New("repository conflict")
	ErrInvalid     = errors.New("invalid repository input")
	ErrUnavailable = errors.New("repository unavailable")
)

type Goal struct {
	Kind         string   `json:"kind"`
	PreferredAt  string   `json:"preferred_at"`
	Timezone     string   `json:"timezone"`
	PartySize    *int     `json:"party_size,omitempty"`
	Service      string   `json:"service,omitempty"`
	Constraints  []string `json:"constraints"`
	ContactName  string   `json:"contact_name"`
	ContactPhone string   `json:"contact_phone,omitempty"`
}
type Call struct {
	ID              string    `json:"id"`
	OwnerID         string    `json:"owner_id"`
	Mode            string    `json:"mode"`
	Destination     string    `json:"destination"`
	BusinessName    string    `json:"business_name"`
	Goal            Goal      `json:"goal"`
	Status          string    `json:"status"`
	ApprovalMode    string    `json:"approval_mode"`
	ContextRevision int64     `json:"context_revision"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
type Draft struct {
	Mode                string `json:"mode"`
	Destination         string `json:"destination"`
	BusinessName        string `json:"business_name"`
	Goal                Goal   `json:"goal"`
	ApprovalMode        string `json:"approval_mode"`
	ConsentAcknowledged bool   `json:"consent_acknowledged"`
}
type Turn struct {
	ID          string `json:"id"`
	CallID      string `json:"call_id"`
	Speaker     string `json:"speaker"`
	Text        string `json:"text"`
	IsFinal     bool   `json:"is_final"`
	StartedAtMS int64  `json:"started_at_ms"`
	EndedAtMS   *int64 `json:"ended_at_ms,omitempty"`
	ResponseID  string `json:"response_id,omitempty"`
}
type Response struct {
	ID              string `json:"id"`
	CallID          string `json:"call_id"`
	Text            string `json:"text"`
	ContextRevision int64  `json:"context_revision"`
	Status          string `json:"status"`
	NeedsApproval   bool   `json:"needs_approval"`
	Reason          string `json:"reason,omitempty"`
}
type Instruction struct {
	ID              string    `json:"id"`
	CallID          string    `json:"call_id"`
	Text            string    `json:"text"`
	ContextRevision int64     `json:"context_revision"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
}
type Event struct {
	ID              string          `json:"id"`
	CallID          string          `json:"call_id"`
	Seq             int64           `json:"seq"`
	Type            string          `json:"type"`
	OccurredAt      time.Time       `json:"occurred_at"`
	ContextRevision int64           `json:"context_revision"`
	Payload         json.RawMessage `json:"payload"`
}
type Snapshot struct {
	Call         Call          `json:"call"`
	LastSeq      int64         `json:"last_seq"`
	Turns        []Turn        `json:"turns"`
	Instructions []Instruction `json:"instructions"`
	Responses    []Response    `json:"responses"`
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var phonePattern = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)
var fictionalPhone = regexp.MustCompile(`^\+1[2-9][0-9]{2}55501[0-9]{2}$`)
var actionPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,99}$`)

func NewID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func bounded(s string, min, max int) bool {
	return utf8.ValidString(s) && len(utf16.Encode([]rune(s))) >= min && len(utf16.Encode([]rune(s))) <= max && (min == 0 || strings.TrimSpace(s) != "")
}
func oneOf(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}
func (g Goal) validate() error {
	t, err := time.Parse(time.RFC3339, g.PreferredAt)
	if err != nil {
		return ErrInvalid
	}
	loc, err := time.LoadLocation(g.Timezone)
	if err != nil || g.Timezone == "" || g.Timezone == "Local" {
		return ErrInvalid
	}
	_, offset := t.Zone()
	_, want := t.In(loc).Zone()
	if offset != want {
		return ErrInvalid
	}
	if !bounded(g.ContactName, 1, 120) || len(g.Constraints) > 12 || (g.ContactPhone != "" && !phonePattern.MatchString(g.ContactPhone)) {
		return ErrInvalid
	}
	for _, c := range g.Constraints {
		if !bounded(c, 1, 200) {
			return ErrInvalid
		}
	}
	if g.Kind == "restaurant" {
		if g.PartySize == nil || *g.PartySize < 1 || *g.PartySize > 20 || g.Service != "" {
			return ErrInvalid
		}
	} else if g.Kind == "appointment" {
		if g.PartySize != nil || !bounded(g.Service, 1, 160) {
			return ErrInvalid
		}
	} else {
		return ErrInvalid
	}
	return nil
}
