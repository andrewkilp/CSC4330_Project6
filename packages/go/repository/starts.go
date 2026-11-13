package repository

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
)

// StartPolicy is server configuration, never populated from an HTTP body.
type StartPolicy struct {
	EnableReal     bool
	AllowedNumbers []string
	AccountSID     string
}
type StartIntent struct {
	AttemptID string
	TokenHash []byte
}
type StartAttempt struct {
	Call      Call
	AttemptID string
	Fresh     bool
}
type TwilioCallback struct{ CallID, AttemptID, Token, AccountSID, CallSID, Status string }
type CallbackResult struct {
	Call    Call
	Events  []Event
	Changed bool
}

var twilioAccountSID = regexp.MustCompile(`^AC[0-9a-fA-F]{32}$`)
var twilioCallSID = regexp.MustCompile(`^CA[0-9a-fA-F]{32}$`)
var mockCallSID = regexp.MustCompile(`^MOCKCA[0-9a-fA-F]{32}$`)

// ReserveCallStart commits a permanent one-per-call intent. The successful caller
// alone receives Fresh=true. No provider I/O belongs inside this transaction.
// A pending intent cannot be retried, even after restart or with another key.
func (r *Repository) ReserveCallStart(ctx context.Context, owner, callID, key string, policy StartPolicy, intent StartIntent) (StartAttempt, error) {
	if !keyValid(owner, callID, "start", key) || !uuidPattern.MatchString(intent.AttemptID) || len(intent.TokenHash) != 32 {
		return StartAttempt{}, ErrInvalid
	}
	db, err := r.pool.Begin(ctx)
	if err != nil {
		return StartAttempt{}, safe(err)
	}
	defer rollback(db)
	var consent bool
	err = db.QueryRow(ctx, `SELECT consent_acknowledged FROM calls WHERE owner_id=$1 AND id=$2 FOR UPDATE`, owner, callID).Scan(&consent)
	if err != nil {
		return StartAttempt{}, safe(err)
	}
	var oldKey, attempt string
	var result []byte
	err = db.QueryRow(ctx, `SELECT start_key::text,attempt_id::text,result_json FROM call_starts WHERE call_id=$1`, callID).Scan(&oldKey, &attempt, &result)
	if err == nil {
		if oldKey != key || len(result) == 0 {
			return StartAttempt{}, ErrConflict
		}
		var call Call
		if json.Unmarshal(result, &call) != nil {
			return StartAttempt{}, ErrUnavailable
		}
		return StartAttempt{Call: call, AttemptID: attempt}, safe(db.Commit(ctx))
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return StartAttempt{}, safe(err)
	}
	t := &Tx{ctx: ctx, db: db, owner: owner, id: callID, active: true}
	defer func() { t.active = false }()
	t.call, err = t.loadCall()
	if err != nil {
		return StartAttempt{}, err
	}
	call := t.Call()
	if call.Status != "draft" {
		return StartAttempt{}, ErrConflict
	}
	if !consent {
		return StartAttempt{}, ErrInvalid
	}
	if call.Mode == "real" {
		allowed := false
		for _, number := range policy.AllowedNumbers {
			if number == call.Destination && phonePattern.MatchString(number) {
				allowed = true
			}
		}
		if !policy.EnableReal || !allowed || !twilioAccountSID.MatchString(policy.AccountSID) {
			return StartAttempt{}, ErrUnavailable
		}
	} else if call.Mode != "mock" || !fictionalPhone.MatchString(call.Destination) {
		return StartAttempt{}, ErrInvalid
	}
	account := ""
	if call.Mode == "real" {
		account = policy.AccountSID
	}
	_, err = db.Exec(ctx, `INSERT INTO call_starts(call_id,attempt_id,start_key,mode,destination,account_sid,callback_token_hash,state) VALUES($1,$2,$3,$4,$5,$6,$7,'reserved')`, callID, intent.AttemptID, key, call.Mode, call.Destination, account, intent.TokenHash)
	if err != nil {
		return StartAttempt{}, safe(err)
	}
	if err = t.SetCall(call.ContextRevision, "dialing", call.ContextRevision); err != nil {
		return StartAttempt{}, err
	}
	if _, err = t.AppendEvent("call.updated", t.Call()); err != nil {
		return StartAttempt{}, err
	}
	if err = db.Commit(ctx); err != nil {
		return StartAttempt{}, safe(err)
	}
	return StartAttempt{Call: t.Call(), AttemptID: intent.AttemptID, Fresh: true}, nil
}

// ConfirmCallStart associates an acknowledged provider SID without restarting a
// call. A callback can win this race; the REST SID must then match that binding.
func (r *Repository) ConfirmCallStart(ctx context.Context, owner, callID, attemptID, sid string) (Call, error) {
	if !uuidPattern.MatchString(owner) || !uuidPattern.MatchString(callID) || !uuidPattern.MatchString(attemptID) {
		return Call{}, ErrInvalid
	}
	db, err := r.pool.Begin(ctx)
	if err != nil {
		return Call{}, safe(err)
	}
	defer rollback(db)
	var mode string
	var existing *string
	var result []byte
	err = db.QueryRow(ctx, `SELECT s.mode,s.provider_sid,s.result_json FROM call_starts s JOIN calls c ON c.id=s.call_id WHERE c.owner_id=$1 AND c.id=$2 AND s.attempt_id=$3 FOR UPDATE OF c,s`, owner, callID, attemptID).Scan(&mode, &existing, &result)
	if err != nil {
		return Call{}, safe(err)
	}
	if (mode == "real" && !twilioCallSID.MatchString(sid)) || (mode == "mock" && !mockCallSID.MatchString(sid)) {
		return Call{}, ErrInvalid
	}
	if existing != nil && *existing != sid {
		return Call{}, ErrConflict
	}
	if len(result) > 0 {
		var c Call
		if json.Unmarshal(result, &c) != nil {
			return Call{}, ErrUnavailable
		}
		return c, safe(db.Commit(ctx))
	}
	t := &Tx{ctx: ctx, db: db, owner: owner, id: callID, active: true}
	defer func() { t.active = false }()
	call, err := t.loadCall()
	if err != nil {
		return Call{}, err
	}
	body, err := canonical(call, 32*1024)
	if err != nil {
		return Call{}, err
	}
	_, err = db.Exec(ctx, `UPDATE call_starts SET provider_sid=$3,state='confirmed',result_json=$4,updated_at=clock_timestamp() WHERE call_id=$1 AND attempt_id=$2`, callID, attemptID, sid, body)
	if err != nil {
		return Call{}, safe(err)
	}
	return call, safe(db.Commit(ctx))
}

// MarkCallStartAmbiguous never rearms an intent. A later authenticated callback
// may still associate the real call, but another POST must never dial again.
func (r *Repository) MarkCallStartAmbiguous(ctx context.Context, owner, callID, attemptID string) error {
	if !uuidPattern.MatchString(owner) || !uuidPattern.MatchString(callID) || !uuidPattern.MatchString(attemptID) {
		return ErrInvalid
	}
	db, err := r.pool.Begin(ctx)
	if err != nil {
		return safe(err)
	}
	defer rollback(db)
	var id string
	err = db.QueryRow(ctx, `SELECT c.id::text FROM calls c JOIN call_starts s ON s.call_id=c.id WHERE c.owner_id=$1 AND c.id=$2 AND s.attempt_id=$3 FOR UPDATE OF c,s`, owner, callID, attemptID).Scan(&id)
	if err != nil {
		return safe(err)
	}
	_, err = db.Exec(ctx, `UPDATE call_starts SET state='ambiguous',updated_at=clock_timestamp() WHERE call_id=$1 AND provider_sid IS NULL`, callID)
	if err != nil {
		return safe(err)
	}
	return safe(db.Commit(ctx))
}

// ApplyTwilioCallback is an internal correlation boundary. HTTP callers MUST
// verify Twilio's signature over the canonical external URL/form before using it.
// Token/account/mode/attempt/SID checks here are additional durable owner fences.
func (r *Repository) ApplyTwilioCallback(ctx context.Context, cb TwilioCallback) (CallbackResult, error) {
	if !uuidPattern.MatchString(cb.CallID) || !uuidPattern.MatchString(cb.AttemptID) || len(cb.Token) < 32 || len(cb.Token) > 128 || !twilioAccountSID.MatchString(cb.AccountSID) || !twilioCallSID.MatchString(cb.CallSID) {
		return CallbackResult{}, ErrNotFound
	}
	if cb.Status != "" && !oneOf(cb.Status, "queued", "initiated", "ringing", "in-progress", "completed", "busy", "no-answer", "failed", "canceled") {
		return CallbackResult{}, ErrInvalid
	}
	db, err := r.pool.Begin(ctx)
	if err != nil {
		return CallbackResult{}, safe(err)
	}
	defer rollback(db)
	var owner, mode, account string
	var hash, result []byte
	var sid *string
	err = db.QueryRow(ctx, `SELECT c.owner_id::text,s.mode,s.account_sid,s.callback_token_hash,s.provider_sid,s.result_json FROM call_starts s JOIN calls c ON c.id=s.call_id WHERE c.id=$1 AND s.attempt_id=$2 FOR UPDATE OF c,s`, cb.CallID, cb.AttemptID).Scan(&owner, &mode, &account, &hash, &sid, &result)
	if err != nil {
		return CallbackResult{}, safe(err)
	}
	want := sha256.Sum256([]byte(cb.Token))
	if mode != "real" || account != cb.AccountSID || subtle.ConstantTimeCompare(hash, want[:]) != 1 {
		return CallbackResult{}, ErrNotFound
	}
	if sid != nil && *sid != cb.CallSID {
		return CallbackResult{}, ErrConflict
	}
	t := &Tx{ctx: ctx, db: db, owner: owner, id: cb.CallID, active: true}
	defer func() { t.active = false }()
	t.call, err = t.loadCall()
	if err != nil {
		return CallbackResult{}, err
	}
	out := CallbackResult{Call: t.Call(), Events: []Event{}}
	next := twilioStatus(t.call.Status, cb.Status)
	if next != t.call.Status {
		if err = t.SetCall(t.call.ContextRevision, next, t.call.ContextRevision); err != nil {
			return CallbackResult{}, err
		}
		event, err := t.AppendEvent("call.updated", t.Call())
		if err != nil {
			return CallbackResult{}, err
		}
		out.Call = t.Call()
		out.Changed = true
		out.Events = append(out.Events, event)
	}
	if sid == nil {
		body, err := canonical(out.Call, 32*1024)
		if err != nil {
			return CallbackResult{}, err
		}
		_, err = db.Exec(ctx, `UPDATE call_starts SET provider_sid=$3,state='confirmed',result_json=$4,updated_at=clock_timestamp() WHERE call_id=$1 AND attempt_id=$2`, cb.CallID, cb.AttemptID, cb.CallSID, body)
		if err != nil {
			return CallbackResult{}, safe(err)
		}
	}
	if err = db.Commit(ctx); err != nil {
		return CallbackResult{}, safe(err)
	}
	return out, nil
}

func twilioStatus(current, status string) string {
	if status == "" || oneOf(current, "ended", "failed", "cancelled") {
		return current
	}
	next := current
	switch status {
	case "queued", "initiated", "ringing":
		next = "dialing"
	case "in-progress":
		next = "connected"
	case "completed":
		next = "ended"
	case "busy", "no-answer", "failed":
		next = "failed"
	case "canceled":
		next = "cancelled"
	}
	if (current == "connected" || current == "paused") && next == "dialing" {
		return current
	}
	if current == "paused" && next == "connected" {
		return current
	}
	return next
}

// ActiveProviderCallID returns the provider call SID bound to a real call that is still
// dialing, connected or paused. The media bridge (#21) calls it only after verifying
// the stream's HMAC token, to check that the stream belongs to that exact provider
// call. It is not owner-scoped because provider media carries no user session; it
// returns nothing but the SID, and ErrNotFound for any other call or state.
func (r *Repository) ActiveProviderCallID(ctx context.Context, callID string) (string, error) {
	if !uuidPattern.MatchString(callID) {
		return "", ErrNotFound
	}
	var sid string
	err := r.pool.QueryRow(ctx, `SELECT s.provider_sid FROM call_starts s JOIN calls c ON c.id=s.call_id WHERE c.id=$1 AND s.mode='real' AND s.state='confirmed' AND s.provider_sid IS NOT NULL AND c.status IN ('dialing','connected','paused')`, callID).Scan(&sid)
	if err != nil {
		return "", safe(err)
	}
	return sid, nil
}

// ---------------------------------------------------------------------------
// Provider failure recovery (#36). These are internal boundaries for verified
// provider paths and the dial reconciler; they never take user input directly.

// ProviderFailure is a user-safe provider.error. Code is a stable snake_case value
// (for example dial_timeout), never a vendor message.
type ProviderFailure struct {
	Provider  string `json:"provider"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
	// FailCall also moves a call that is not yet terminal to failed.
	FailCall bool `json:"-"`
}

var failureCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// RecordProviderFailure appends a provider.error event for the call and, with
// FailCall, moves a non-terminal call to failed after it, in one transaction. It
// returns the committed events for publishing.
func (r *Repository) RecordProviderFailure(ctx context.Context, callID string, f ProviderFailure) ([]Event, error) {
	if !uuidPattern.MatchString(callID) || !oneOf(f.Provider, "telephony", "stt", "llm", "tts") || !failureCode.MatchString(f.Code) {
		return nil, ErrInvalid
	}
	db, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, safe(err)
	}
	defer rollback(db)
	var owner string
	if err = db.QueryRow(ctx, `SELECT owner_id::text FROM calls WHERE id=$1 FOR UPDATE`, callID).Scan(&owner); err != nil {
		return nil, safe(err)
	}
	t := &Tx{ctx: ctx, db: db, owner: owner, id: callID, active: true}
	defer func() { t.active = false }()
	if t.call, err = t.loadCall(); err != nil {
		return nil, err
	}
	e, err := t.AppendEvent("provider.error", f)
	if err != nil {
		return nil, err
	}
	events := []Event{e}
	if f.FailCall && !oneOf(t.call.Status, "ended", "failed", "cancelled") {
		if err = t.SetCall(t.call.ContextRevision, "failed", t.call.ContextRevision); err != nil {
			return nil, err
		}
		if e, err = t.AppendEvent("call.updated", t.Call()); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	if err = db.Commit(ctx); err != nil {
		return nil, safe(err)
	}
	return events, nil
}

// StaleCall is a call that has been dialing too long.
type StaleCall struct {
	CallID, Mode string
	// ProviderSID is empty when the provider never acknowledged the start.
	ProviderSID string
}

// StaleDialing lists calls that have been dialing for longer than olderThan, oldest
// first, at most limit (1..100).
func (r *Repository) StaleDialing(ctx context.Context, olderThan time.Duration, limit int) ([]StaleCall, error) {
	if olderThan <= 0 || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	rows, err := r.pool.Query(ctx, `SELECT c.id::text, c.mode, COALESCE(s.provider_sid, '') FROM calls c LEFT JOIN call_starts s ON s.call_id=c.id
		WHERE c.status='dialing' AND c.updated_at < clock_timestamp() - make_interval(secs => $1) ORDER BY c.updated_at, c.id LIMIT $2`, olderThan.Seconds(), limit)
	if err != nil {
		return nil, safe(err)
	}
	defer rows.Close()
	var out []StaleCall
	for rows.Next() {
		var s StaleCall
		if err := rows.Scan(&s.CallID, &s.Mode, &s.ProviderSID); err != nil {
			return nil, safe(err)
		}
		out = append(out, s)
	}
	return out, safe(rows.Err())
}

// TwilioStatus maps a Twilio CallStatus onto the current call status the way
// ApplyTwilioCallback does: terminal states never change and nothing regresses.
func TwilioStatus(current, status string) string { return twilioStatus(current, status) }
