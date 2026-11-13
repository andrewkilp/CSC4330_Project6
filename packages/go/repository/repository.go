package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ pool *pgxpool.Pool }

// New uses the caller's pool; pool configuration and lifecycle remain server-owned.
func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func safe(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	for _, known := range []error{ErrInvalid, ErrNotFound, ErrConflict} {
		if errors.Is(err, known) {
			return known
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		switch p.Code {
		case "23505", "40001", "40P01":
			return ErrConflict
		case "23502", "23503", "23514", "22001", "22P02":
			return ErrInvalid
		}
	}
	return ErrUnavailable
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
func canonical(v any, max int) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil || len(b) > max {
		return nil, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var value map[string]any
	if err = d.Decode(&value); err != nil || value == nil {
		return nil, ErrInvalid
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return nil, ErrInvalid
	}
	b, err = json.Marshal(value)
	if err != nil {
		return nil, ErrInvalid
	}
	return b, nil
}
func read[T any](ctx context.Context, tx pgx.Tx, sql string, args ...any) (T, error) {
	var value T
	var b []byte
	err := tx.QueryRow(ctx, sql, args...).Scan(&b)
	if err != nil {
		return value, safe(err)
	}
	if json.Unmarshal(b, &value) != nil {
		return value, ErrUnavailable
	}
	return value, nil
}
func rows[T any](ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]T, error) {
	r, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, safe(err)
	}
	defer r.Close()
	out := []T{}
	for r.Next() {
		var b []byte
		var v T
		if r.Scan(&b) != nil || json.Unmarshal(b, &v) != nil {
			return nil, ErrUnavailable
		}
		out = append(out, v)
	}
	return out, safe(r.Err())
}
func keyValid(owner, call, action, key string) bool {
	return uuidPattern.MatchString(owner) && uuidPattern.MatchString(call) && actionPattern.MatchString(action) && uuidPattern.MatchString(key)
}

func (r *Repository) Create(ctx context.Context, owner, key string, draft Draft) (Call, error) {
	if !uuidPattern.MatchString(owner) || !uuidPattern.MatchString(key) || !oneOf(draft.Mode, "mock", "real") || !phonePattern.MatchString(draft.Destination) || (draft.Mode == "mock" && !fictionalPhone.MatchString(draft.Destination)) || !bounded(draft.BusinessName, 1, 120) || !oneOf(draft.ApprovalMode, "always", "uncertain") || !draft.ConsentAcknowledged {
		return Call{}, ErrInvalid
	}
	if err := draft.Goal.validate(); err != nil {
		return Call{}, err
	}
	body, err := canonical(draft, 16*1024)
	if err != nil {
		return Call{}, err
	}
	hash := sha256.Sum256(body)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Call{}, safe(err)
	}
	defer rollback(tx)
	_, err = tx.Exec(ctx, `INSERT INTO session_owners(id) VALUES($1) ON CONFLICT DO NOTHING`, owner)
	if err != nil {
		return Call{}, safe(err)
	}
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, owner+":create:"+key)
	if err != nil {
		return Call{}, safe(err)
	}
	var oldHash, oldResult []byte
	err = tx.QueryRow(ctx, `SELECT request_hash,result_json FROM call_idempotency_keys WHERE owner_id=$1 AND action='create' AND scope_call_id IS NULL AND key=$2`, owner, key).Scan(&oldHash, &oldResult)
	if err == nil {
		if !bytes.Equal(oldHash, hash[:]) {
			return Call{}, ErrConflict
		}
		var c Call
		if json.Unmarshal(oldResult, &c) != nil {
			return Call{}, ErrUnavailable
		}
		return c, safe(tx.Commit(ctx))
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Call{}, safe(err)
	}
	id := NewID()
	_, err = tx.Exec(ctx, `INSERT INTO calls(id,owner_id,mode,destination,business_name,approval_mode,consent_acknowledged) VALUES($1,$2,$3,$4,$5,$6,true)`, id, owner, draft.Mode, draft.Destination, draft.BusinessName, draft.ApprovalMode)
	if err != nil {
		return Call{}, safe(err)
	}
	t := &Tx{ctx: ctx, db: tx, owner: owner, id: id, active: true}
	defer func() { t.active = false }()
	if err = t.PutGoal(0, draft.Goal); err != nil {
		return Call{}, err
	}
	c, err := t.loadCall()
	if err != nil {
		return Call{}, err
	}
	t.call = c
	if _, err = t.AppendEvent("call.updated", c); err != nil {
		return Call{}, err
	}
	result, err := canonical(c, 32*1024)
	if err != nil {
		return Call{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO call_idempotency_keys(owner_id,action,scope_call_id,key,request_hash,result_call_id,result_json) VALUES($1,'create',NULL,$2,$3,$4,$5)`, owner, key, hash[:], id, result)
	if err != nil {
		return Call{}, safe(err)
	}
	return c, safe(tx.Commit(ctx))
}

// Update locks exactly one owned call. fn must only perform database work through
// Tx: never network I/O, callbacks, audio or irreversible side effects. A retry
// with identical input returns the original JSON result without invoking fn.
func (r *Repository) Update(ctx context.Context, owner, callID, action, key string, request any, fn func(*Tx) (any, error)) (json.RawMessage, error) {
	if !keyValid(owner, callID, action, key) || fn == nil {
		return nil, ErrInvalid
	}
	body, err := canonical(request, 16*1024)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(body)
	db, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, safe(err)
	}
	defer rollback(db)
	var locked string
	err = db.QueryRow(ctx, `SELECT id::text FROM calls WHERE owner_id=$1 AND id=$2 FOR UPDATE`, owner, callID).Scan(&locked)
	if err != nil {
		return nil, safe(err)
	}
	var oldHash, result []byte
	err = db.QueryRow(ctx, `SELECT request_hash,result_json FROM call_idempotency_keys WHERE owner_id=$1 AND action=$2 AND scope_call_id=$3 AND key=$4`, owner, action, callID, key).Scan(&oldHash, &result)
	if err == nil {
		if !bytes.Equal(oldHash, hash[:]) {
			return nil, ErrConflict
		}
		return json.RawMessage(result), safe(db.Commit(ctx))
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, safe(err)
	}
	t := &Tx{ctx: ctx, db: db, owner: owner, id: callID, active: true}
	defer func() { t.active = false }()
	t.call, err = t.loadCall()
	if err != nil {
		return nil, err
	}
	v, err := fn(t)
	if err != nil {
		return nil, safe(err)
	}
	result, err = canonical(v, 32*1024)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(ctx, `INSERT INTO call_idempotency_keys(owner_id,action,scope_call_id,key,request_hash,result_call_id,result_json) VALUES($1,$2,$3,$4,$5,$3,$6)`, owner, action, callID, key, hash[:], result)
	if err != nil {
		return nil, safe(err)
	}
	if err = db.Commit(ctx); err != nil {
		return nil, safe(err)
	}
	return json.RawMessage(result), nil
}

// Snapshot uses one repeatable-read checkpoint. Lists are bounded to 4096 rows;
// a larger call fails explicitly instead of silently returning a partial snapshot.
func (r *Repository) Snapshot(ctx context.Context, owner, callID string) (Snapshot, error) {
	if !uuidPattern.MatchString(owner) || !uuidPattern.MatchString(callID) {
		return Snapshot{}, ErrInvalid
	}
	db, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Snapshot{}, safe(err)
	}
	defer rollback(db)
	t := &Tx{ctx: ctx, db: db, owner: owner, id: callID, active: true}
	defer func() { t.active = false }()
	var out Snapshot
	if out.Call, err = t.loadCall(); err != nil {
		return Snapshot{}, err
	}
	if err = db.QueryRow(ctx, `SELECT last_event_seq FROM calls WHERE owner_id=$1 AND id=$2`, owner, callID).Scan(&out.LastSeq); err != nil {
		return Snapshot{}, safe(err)
	}
	if out.Turns, err = t.Turns(); err != nil {
		return Snapshot{}, err
	}
	if out.Responses, err = t.Responses(); err != nil {
		return Snapshot{}, err
	}
	if out.Instructions, err = t.Instructions(); err != nil {
		return Snapshot{}, err
	}
	return out, safe(db.Commit(ctx))
}

func (r *Repository) GetCall(ctx context.Context, owner, callID string) (Call, error) {
	if !uuidPattern.MatchString(owner) || !uuidPattern.MatchString(callID) {
		return Call{}, ErrInvalid
	}
	db, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Call{}, safe(err)
	}
	defer rollback(db)
	t := &Tx{ctx: ctx, db: db, owner: owner, id: callID, active: true}
	defer func() { t.active = false }()
	c, err := t.loadCall()
	if err != nil {
		return Call{}, err
	}
	return c, safe(db.Commit(ctx))
}

// DeleteCall is separate from Update because erasure also cascades through its
// idempotency records. It never deletes a live call or performs retention jobs.
func (r *Repository) DeleteCall(ctx context.Context, owner, callID string) error {
	if !uuidPattern.MatchString(owner) || !uuidPattern.MatchString(callID) {
		return ErrInvalid
	}
	db, err := r.pool.Begin(ctx)
	if err != nil {
		return safe(err)
	}
	defer rollback(db)
	var status string
	if err = db.QueryRow(ctx, `SELECT status FROM calls WHERE owner_id=$1 AND id=$2 FOR UPDATE`, owner, callID).Scan(&status); err != nil {
		return safe(err)
	}
	if !oneOf(status, "ended", "failed", "cancelled") {
		return ErrConflict
	}
	_, err = db.Exec(ctx, `DELETE FROM calls WHERE owner_id=$1 AND id=$2`, owner, callID)
	if err != nil {
		return safe(err)
	}
	return safe(db.Commit(ctx))
}

// EventsAfter returns at most limit events in durable sequence order. It does not
// implement cursor expiry/fanout: #23 owns retention and SSE policy.
func (r *Repository) EventsAfter(ctx context.Context, owner, callID string, after int64, limit int) ([]Event, error) {
	if !uuidPattern.MatchString(owner) || !uuidPattern.MatchString(callID) || after < 0 || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	db, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, safe(err)
	}
	defer rollback(db)
	var id string
	if err = db.QueryRow(ctx, `SELECT id::text FROM calls WHERE owner_id=$1 AND id=$2`, owner, callID).Scan(&id); err != nil {
		return nil, safe(err)
	}
	items, err := rows[Event](ctx, db, `SELECT to_jsonb(e) FROM call_events e JOIN calls c ON c.id=e.call_id WHERE c.owner_id=$1 AND c.id=$2 AND e.seq>$3 ORDER BY e.seq LIMIT $4`, owner, callID, after, limit)
	if err != nil {
		return nil, err
	}
	return items, safe(db.Commit(ctx))
}

type Tx struct {
	ctx       context.Context
	db        pgx.Tx
	owner, id string
	call      Call
	active    bool
}

func (t *Tx) check() error {
	if !t.active {
		return ErrConflict
	}
	return nil
}
func (t *Tx) Call() Call {
	c := t.call
	c.Goal.Constraints = append([]string{}, c.Goal.Constraints...)
	if c.Goal.PartySize != nil {
		n := *c.Goal.PartySize
		c.Goal.PartySize = &n
	}
	return c
}
func (t *Tx) loadCall() (Call, error) {
	c, err := read[Call](t.ctx, t.db, `SELECT to_jsonb(c) FROM calls c WHERE c.owner_id=$1 AND c.id=$2`, t.owner, t.id)
	if err != nil {
		return Call{}, err
	}
	g, err := read[Goal](t.ctx, t.db, `SELECT to_jsonb(g) FROM call_goals g JOIN calls c ON c.id=g.call_id WHERE c.owner_id=$1 AND c.id=$2 AND g.context_revision<=c.context_revision ORDER BY g.context_revision DESC LIMIT 1`, t.owner, t.id)
	if err != nil {
		return Call{}, err
	}
	at, err := time.Parse(time.RFC3339, g.PreferredAt)
	if err != nil {
		return Call{}, ErrUnavailable
	}
	loc, err := time.LoadLocation(g.Timezone)
	if err != nil {
		return Call{}, ErrUnavailable
	}
	g.PreferredAt = at.In(loc).Format(time.RFC3339Nano)
	c.Goal = g
	c.CreatedAt = c.CreatedAt.UTC()
	c.UpdatedAt = c.UpdatedAt.UTC()
	return c, nil
}
