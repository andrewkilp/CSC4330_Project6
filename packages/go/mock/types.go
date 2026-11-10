// Package mock builds deterministic, network-free call scenarios for tests, previews and
// later mock provider adapters. Every fixture uses fictional people, businesses and
// phone numbers, and its business name is visibly labelled as mock data.
package mock

// The types below mirror the planned wire shapes in docs/contracts.md. They are a
// fixture projection, not the canonical domain types owned by T01.

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
	ID              string `json:"id"`
	OwnerID         string `json:"owner_id"`
	Mode            string `json:"mode"`
	Destination     string `json:"destination"`
	BusinessName    string `json:"business_name"`
	Goal            Goal   `json:"goal"`
	Status          string `json:"status"`
	ApprovalMode    string `json:"approval_mode"`
	ContextRevision uint64 `json:"context_revision"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
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

type Instruction struct {
	ID              string `json:"id"`
	CallID          string `json:"call_id"`
	Text            string `json:"text"`
	ContextRevision uint64 `json:"context_revision"`
	Status          string `json:"status"`
	CreatedAt       string `json:"created_at"`
}

type Response struct {
	ID              string `json:"id"`
	CallID          string `json:"call_id"`
	Text            string `json:"text"`
	ContextRevision uint64 `json:"context_revision"`
	Status          string `json:"status"`
	NeedsApproval   bool   `json:"needs_approval"`
	Reason          string `json:"reason,omitempty"`
}

type ConfirmedDetails struct {
	BusinessName          string `json:"business_name"`
	ScheduledAt           string `json:"scheduled_at,omitempty"`
	Timezone              string `json:"timezone,omitempty"`
	PartySize             *int   `json:"party_size,omitempty"`
	Service               string `json:"service,omitempty"`
	ConfirmationReference string `json:"confirmation_reference,omitempty"`
}

type Summary struct {
	CallID              string           `json:"call_id"`
	Outcome             string           `json:"outcome"`
	ConfirmedDetails    ConfirmedDetails `json:"confirmed_details"`
	EvidenceTurnIDs     []string         `json:"evidence_turn_ids"`
	UnresolvedQuestions []string         `json:"unresolved_questions"`
	NextSteps           []string         `json:"next_steps"`
}

type Usage struct {
	ElapsedSeconds   int64   `json:"elapsed_seconds"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
	EstimateOnly     bool    `json:"estimate_only"`
}

// Event is the durable envelope from docs/contracts.md. Payload holds one of the
// payload types above.
type Event struct {
	ID              string `json:"id"`
	CallID          string `json:"call_id"`
	Seq             uint64 `json:"seq"`
	Type            string `json:"type"`
	OccurredAt      string `json:"occurred_at"`
	ContextRevision uint64 `json:"context_revision"`
	Payload         any    `json:"payload"`
}

// OperatorInput is an expected private action from the person running the call.
// Kind is "instruction" (a new context revision) or "approval" (approve one response).
type OperatorInput struct {
	Kind       string `json:"kind"`
	ResponseID string `json:"response_id"`
	Text       string `json:"text,omitempty"`
	Reason     string `json:"reason"`
}

// Expected describes what a runner or reviewer should observe from the scenario.
type Expected struct {
	OperatorInputs      []OperatorInput `json:"operator_inputs"`
	UnansweredQuestions []string        `json:"unanswered_questions"`
	Outcome             string          `json:"outcome"`
}

// Fixture is one fully expanded scenario for one seed.
type Fixture struct {
	Scenario    string   `json:"scenario"`
	Description string   `json:"description"`
	Seed        uint64   `json:"seed"`
	Fictional   bool     `json:"fictional"`
	Expected    Expected `json:"expected"`
	Events      []Event  `json:"events"`
}
