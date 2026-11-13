package repository

import (
	"encoding/json"
	"time"
)

func (t *Tx) SetCall(expectedRevision int64, status string, revision int64) error {
	if err := t.check(); err != nil {
		return err
	}
	if !oneOf(status, "draft", "dialing", "connected", "paused", "ended", "failed", "cancelled") || revision < 0 {
		return ErrInvalid
	}
	if expectedRevision != t.call.ContextRevision || revision < expectedRevision {
		return ErrConflict
	}
	_, err := t.db.Exec(t.ctx, `UPDATE calls SET status=$3,context_revision=$4,updated_at=clock_timestamp() WHERE owner_id=$1 AND id=$2`, t.owner, t.id, status, revision)
	if err != nil {
		return safe(err)
	}
	t.call, err = t.loadCall()
	return err
}

// PutGoal inserts an immutable goal snapshot. Repeating identical data at the
// same revision is harmless; replacing a committed goal revision is a conflict.
func (t *Tx) PutGoal(revision int64, g Goal) error {
	if err := t.check(); err != nil {
		return err
	}
	if revision < 0 {
		return ErrInvalid
	}
	if err := g.validate(); err != nil {
		return err
	}
	if g.Constraints == nil {
		g.Constraints = []string{}
	}
	at, _ := time.Parse(time.RFC3339, g.PreferredAt)
	cmd, err := t.db.Exec(t.ctx, `INSERT INTO call_goals(call_id,context_revision,kind,preferred_at,timezone,party_size,service,constraints,contact_name,contact_phone)
SELECT c.id,$3,$4,$5,$6,$7,$8,$9,$10,$11 FROM calls c WHERE c.owner_id=$1 AND c.id=$2
ON CONFLICT(call_id,context_revision) DO UPDATE SET kind=call_goals.kind
WHERE (call_goals.kind,call_goals.preferred_at,call_goals.timezone,call_goals.party_size,call_goals.service,call_goals.constraints,call_goals.contact_name,call_goals.contact_phone)
IS NOT DISTINCT FROM (EXCLUDED.kind,EXCLUDED.preferred_at,EXCLUDED.timezone,EXCLUDED.party_size,EXCLUDED.service,EXCLUDED.constraints,EXCLUDED.contact_name,EXCLUDED.contact_phone)`, t.owner, t.id, revision, g.Kind, at, g.Timezone, g.PartySize, nullString(g.Service), g.Constraints, g.ContactName, nullString(g.ContactPhone))
	if err != nil {
		return safe(err)
	}
	if cmd.RowsAffected() != 1 {
		return ErrConflict
	}
	if revision <= t.call.ContextRevision {
		t.call, err = t.loadCall()
		return err
	}
	return nil
}
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (t *Tx) PutTurn(v Turn) error {
	if err := t.check(); err != nil {
		return err
	}
	if v.CallID != t.id || !uuidPattern.MatchString(v.ID) || !oneOf(v.Speaker, "assistant", "business") || !bounded(v.Text, 0, 10000) || (v.IsFinal && !bounded(v.Text, 1, 10000)) || v.StartedAtMS < 0 || (v.EndedAtMS != nil && *v.EndedAtMS < v.StartedAtMS) || v.IsFinal != (v.EndedAtMS != nil) || (v.ResponseID != "" && !uuidPattern.MatchString(v.ResponseID)) || (v.Speaker == "business" && v.ResponseID != "") {
		return ErrInvalid
	}
	cmd, err := t.db.Exec(t.ctx, `INSERT INTO call_turns(id,call_id,speaker,text,is_final,started_at_ms,ended_at_ms,response_id)
SELECT $3,c.id,$4,$5,$6,$7,$8,$9 FROM calls c WHERE c.owner_id=$1 AND c.id=$2
ON CONFLICT(id) DO UPDATE SET text=EXCLUDED.text,is_final=EXCLUDED.is_final,ended_at_ms=EXCLUDED.ended_at_ms,response_id=EXCLUDED.response_id
WHERE call_turns.call_id=EXCLUDED.call_id AND call_turns.speaker=EXCLUDED.speaker AND call_turns.started_at_ms=EXCLUDED.started_at_ms
AND (NOT call_turns.is_final OR (call_turns.text,call_turns.is_final,call_turns.ended_at_ms,call_turns.response_id) IS NOT DISTINCT FROM (EXCLUDED.text,EXCLUDED.is_final,EXCLUDED.ended_at_ms,EXCLUDED.response_id))`, t.owner, t.id, v.ID, v.Speaker, v.Text, v.IsFinal, v.StartedAtMS, v.EndedAtMS, nullString(v.ResponseID))
	if err != nil {
		return safe(err)
	}
	if cmd.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (t *Tx) PutResponse(v Response) error {
	if err := t.check(); err != nil {
		return err
	}
	if v.CallID != t.id || !uuidPattern.MatchString(v.ID) || v.ContextRevision < 0 || v.ContextRevision > t.call.ContextRevision || !oneOf(v.Status, "generating", "suggested", "approved", "speaking", "spoken", "cancelled", "failed") || !bounded(v.Text, 0, 1500) || (!oneOf(v.Status, "generating", "cancelled", "failed") && !bounded(v.Text, 1, 1500)) || !bounded(v.Reason, 0, 1000) {
		return ErrInvalid
	}
	cmd, err := t.db.Exec(t.ctx, `INSERT INTO call_responses(id,call_id,text,context_revision,status,needs_approval,reason)
SELECT $3,c.id,$4,$5,$6,$7,$8 FROM calls c WHERE c.owner_id=$1 AND c.id=$2
ON CONFLICT(id) DO UPDATE SET text=EXCLUDED.text,status=EXCLUDED.status,needs_approval=EXCLUDED.needs_approval,reason=EXCLUDED.reason
WHERE call_responses.call_id=EXCLUDED.call_id AND call_responses.context_revision=EXCLUDED.context_revision`, t.owner, t.id, v.ID, v.Text, v.ContextRevision, v.Status, v.NeedsApproval, nullString(v.Reason))
	if err != nil {
		return safe(err)
	}
	if cmd.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (t *Tx) PutInstruction(v Instruction) error {
	if err := t.check(); err != nil {
		return err
	}
	if v.CallID != t.id || !uuidPattern.MatchString(v.ID) || v.ContextRevision < 0 || v.ContextRevision > t.call.ContextRevision || !oneOf(v.Status, "pending", "applied", "failed") || !bounded(v.Text, 1, 1000) {
		return ErrInvalid
	}
	cmd, err := t.db.Exec(t.ctx, `INSERT INTO call_instructions(id,call_id,text,context_revision,status)
SELECT $3,c.id,$4,$5,$6 FROM calls c WHERE c.owner_id=$1 AND c.id=$2
ON CONFLICT(id) DO UPDATE SET context_revision=EXCLUDED.context_revision,status=EXCLUDED.status
WHERE call_instructions.call_id=EXCLUDED.call_id AND call_instructions.text=EXCLUDED.text AND call_instructions.context_revision<=EXCLUDED.context_revision`, t.owner, t.id, v.ID, v.Text, v.ContextRevision, v.Status)
	if err != nil {
		return safe(err)
	}
	if cmd.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (t *Tx) Turn(id string) (Turn, error) {
	if err := t.check(); err != nil {
		return Turn{}, err
	}
	return read[Turn](t.ctx, t.db, `SELECT to_jsonb(v) FROM call_turns v JOIN calls c ON c.id=v.call_id WHERE c.owner_id=$1 AND c.id=$2 AND v.id=$3`, t.owner, t.id, id)
}
func (t *Tx) Response(id string) (Response, error) {
	if err := t.check(); err != nil {
		return Response{}, err
	}
	return read[Response](t.ctx, t.db, `SELECT to_jsonb(v) FROM call_responses v JOIN calls c ON c.id=v.call_id WHERE c.owner_id=$1 AND c.id=$2 AND v.id=$3`, t.owner, t.id, id)
}
func (t *Tx) Instruction(id string) (Instruction, error) {
	if err := t.check(); err != nil {
		return Instruction{}, err
	}
	return read[Instruction](t.ctx, t.db, `SELECT to_jsonb(v) FROM call_instructions v JOIN calls c ON c.id=v.call_id WHERE c.owner_id=$1 AND c.id=$2 AND v.id=$3`, t.owner, t.id, id)
}

// Child collections are available inside the locked mutation so instruction and
// approval services can inspect/invalidate current entities at the same checkpoint.
func (t *Tx) Turns() ([]Turn, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	return limited[Turn](t, `SELECT to_jsonb(v) FROM call_turns v JOIN calls c ON c.id=v.call_id WHERE c.owner_id=$1 AND c.id=$2 ORDER BY v.started_at_ms,v.id LIMIT 4097`)
}
func (t *Tx) Responses() ([]Response, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	return limited[Response](t, `SELECT to_jsonb(v) FROM call_responses v JOIN calls c ON c.id=v.call_id WHERE c.owner_id=$1 AND c.id=$2 ORDER BY v.context_revision,v.id LIMIT 4097`)
}
func (t *Tx) Instructions() ([]Instruction, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	return limited[Instruction](t, `SELECT to_jsonb(v) FROM call_instructions v JOIN calls c ON c.id=v.call_id WHERE c.owner_id=$1 AND c.id=$2 ORDER BY v.context_revision,v.created_at,v.id LIMIT 4097`)
}
func limited[T any](t *Tx, sql string) ([]T, error) {
	v, err := rows[T](t.ctx, t.db, sql, t.owner, t.id)
	if err != nil {
		return nil, err
	}
	if len(v) > 4096 {
		return nil, ErrConflict
	}
	return v, nil
}

func (t *Tx) DeleteTurn(id string) error        { return t.delete("turn", id) }
func (t *Tx) DeleteResponse(id string) error    { return t.delete("response", id) }
func (t *Tx) DeleteInstruction(id string) error { return t.delete("instruction", id) }
func (t *Tx) delete(kind, id string) error {
	if err := t.check(); err != nil {
		return err
	}
	if !uuidPattern.MatchString(id) {
		return ErrInvalid
	}
	var sql string
	switch kind {
	case "turn":
		sql = `DELETE FROM call_turns v USING calls c WHERE c.id=v.call_id AND c.owner_id=$1 AND c.id=$2 AND v.id=$3`
	case "response":
		sql = `DELETE FROM call_responses v USING calls c WHERE c.id=v.call_id AND c.owner_id=$1 AND c.id=$2 AND v.id=$3`
	case "instruction":
		sql = `DELETE FROM call_instructions v USING calls c WHERE c.id=v.call_id AND c.owner_id=$1 AND c.id=$2 AND v.id=$3`
	default:
		return ErrInvalid
	}
	cmd, err := t.db.Exec(t.ctx, sql, t.owner, t.id, id)
	if err != nil {
		return safe(err)
	}
	if cmd.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

func (t *Tx) DeleteGoal(revision int64) error {
	if err := t.check(); err != nil {
		return err
	}
	if revision < 0 {
		return ErrInvalid
	}
	// Never remove the goal selected for the current call revision.
	cmd, err := t.db.Exec(t.ctx, `DELETE FROM call_goals g USING calls c WHERE c.id=g.call_id AND c.owner_id=$1 AND c.id=$2 AND g.context_revision=$3 AND g.context_revision < (SELECT max(x.context_revision) FROM call_goals x WHERE x.call_id=c.id AND x.context_revision<=c.context_revision)`, t.owner, t.id, revision)
	if err != nil {
		return safe(err)
	}
	if cmd.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (t *Tx) AppendEvent(kind string, payload any) (Event, error) {
	if err := t.check(); err != nil {
		return Event{}, err
	}
	if !oneOf(kind, "call.updated", "turn.updated", "instruction.updated", "response.updated", "summary.ready", "provider.error", "usage.updated") {
		return Event{}, ErrInvalid
	}
	b, err := canonical(payload, 64*1024)
	if err != nil {
		return Event{}, err
	}
	var seq int64
	err = t.db.QueryRow(t.ctx, `UPDATE calls SET last_event_seq=last_event_seq+1 WHERE owner_id=$1 AND id=$2 RETURNING last_event_seq`, t.owner, t.id).Scan(&seq)
	if err != nil {
		return Event{}, safe(err)
	}
	e := Event{ID: NewID(), CallID: t.id, Seq: seq, Type: kind, ContextRevision: t.call.ContextRevision, Payload: b}
	err = t.db.QueryRow(t.ctx, `INSERT INTO call_events(id,call_id,seq,type,context_revision,payload,occurred_at) VALUES($1,$2,$3,$4,$5,$6,clock_timestamp()) RETURNING occurred_at`, e.ID, e.CallID, e.Seq, e.Type, e.ContextRevision, e.Payload).Scan(&e.OccurredAt)
	if err != nil {
		return Event{}, safe(err)
	}
	e.OccurredAt = e.OccurredAt.UTC()
	return e, nil
}

// DecodeResult is a convenience for callers that need to decode an idempotent
// Update result into the corresponding contract type without sharing raw slices.
func DecodeResult(data json.RawMessage, out any) error {
	if json.Unmarshal(data, out) != nil {
		return ErrInvalid
	}
	return nil
}
