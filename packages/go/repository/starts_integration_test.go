package repository

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const startTestAccount = "AC11111111111111111111111111111111"
const startTestSID = "CA22222222222222222222222222222222"
const startTestToken = "fictional-test-token-with-at-least-thirty-two-bytes"

func startDB(t *testing.T) (*Repository, context.Context) {
	return databaseWithMigrations(t, []string{"00001_calls.sql", "00002_repository_transactions.sql", "00003_call_start_intents.sql"})
}
func startIntent() StartIntent {
	hash := sha256.Sum256([]byte(startTestToken))
	return StartIntent{AttemptID: NewID(), TokenHash: hash[:]}
}
func startPolicy() StartPolicy {
	return StartPolicy{EnableReal: true, AllowedNumbers: []string{"+12255550100"}, AccountSID: startTestAccount}
}
func realDraft() Draft { d := draft(); d.Mode = "real"; return d }

func TestIntegrationStartIntentSurvivesRestartAndAlternateKeys(t *testing.T) {
	r, ctx := startDB(t)
	owner, key := NewID(), NewID()
	c, err := r.Create(ctx, owner, NewID(), realDraft())
	if err != nil {
		t.Fatal(err)
	}
	intent := startIntent()
	first, err := r.ReserveCallStart(ctx, owner, c.ID, key, startPolicy(), intent)
	if err != nil || !first.Fresh || first.Call.Status != "dialing" {
		t.Fatalf("reserve=%+v %v", first, err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, r.pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	restarted := New(pool)
	for _, retryKey := range []string{key, NewID()} {
		if _, err := restarted.ReserveCallStart(ctx, owner, c.ID, retryKey, startPolicy(), startIntent()); !errors.Is(err, ErrConflict) {
			t.Fatalf("pending retry rearmed intent: %v", err)
		}
	}
	if err := restarted.MarkCallStartAmbiguous(ctx, owner, c.ID, intent.AttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.ReserveCallStart(ctx, owner, c.ID, key, startPolicy(), startIntent()); !errors.Is(err, ErrConflict) {
		t.Fatalf("ambiguous retry rearmed intent: %v", err)
	}
	confirmed, err := restarted.ConfirmCallStart(ctx, owner, c.ID, intent.AttemptID, startTestSID)
	if err != nil {
		t.Fatal(err)
	}
	again, err := restarted.ReserveCallStart(ctx, owner, c.ID, key, StartPolicy{}, startIntent())
	if err != nil || again.Fresh || again.Call.ID != confirmed.ID {
		t.Fatalf("confirmed replay=%+v %v", again, err)
	}
	if _, err := restarted.ReserveCallStart(ctx, owner, c.ID, NewID(), startPolicy(), startIntent()); !errors.Is(err, ErrConflict) {
		t.Fatal("alternate key redial allowed")
	}
}

func TestIntegrationCallbackBeforeRESTResponseAndWrongCorrelation(t *testing.T) {
	r, ctx := startDB(t)
	owner := NewID()
	c, err := r.Create(ctx, owner, NewID(), realDraft())
	if err != nil {
		t.Fatal(err)
	}
	intent := startIntent()
	if _, err = r.ReserveCallStart(ctx, owner, c.ID, NewID(), startPolicy(), intent); err != nil {
		t.Fatal(err)
	}
	cb := TwilioCallback{CallID: c.ID, AttemptID: intent.AttemptID, Token: startTestToken, AccountSID: startTestAccount, CallSID: startTestSID}
	voice, err := r.ApplyTwilioCallback(ctx, cb)
	if err != nil || voice.Changed || len(voice.Events) != 0 || voice.Call.Status != "dialing" {
		t.Fatalf("voice=%+v %v", voice, err)
	}
	cb.Status = "in-progress"
	out, err := r.ApplyTwilioCallback(ctx, cb)
	if err != nil || !out.Changed || len(out.Events) != 1 || out.Call.Status != "connected" {
		t.Fatalf("answered=%+v %v", out, err)
	}
	for _, status := range []string{"in-progress", "ringing", "queued", "initiated"} {
		cb.Status = status
		again, err := r.ApplyTwilioCallback(ctx, cb)
		if err != nil || again.Changed || again.Call.Status != "connected" {
			t.Fatalf("duplicate/regression=%+v %v", again, err)
		}
	}
	if _, err = r.ConfirmCallStart(ctx, owner, c.ID, intent.AttemptID, startTestSID); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ConfirmCallStart(ctx, owner, c.ID, intent.AttemptID, "CA"+strings.Repeat("3", 32)); !errors.Is(err, ErrConflict) {
		t.Fatal("REST SID mismatch not rejected")
	}
	for _, mutate := range []func(*TwilioCallback){func(c *TwilioCallback) { c.Token = "wrong-token-with-at-least-thirty-two-bytes" }, func(c *TwilioCallback) { c.AttemptID = NewID() }, func(c *TwilioCallback) { c.AccountSID = "AC" + strings.Repeat("9", 32) }, func(c *TwilioCallback) { c.CallID = NewID() }} {
		bad := cb
		mutate(&bad)
		if _, err = r.ApplyTwilioCallback(ctx, bad); !errors.Is(err, ErrNotFound) {
			t.Fatalf("bad correlation accepted=%v", err)
		}
	}
	cb.Status = "completed"
	terminal, err := r.ApplyTwilioCallback(ctx, cb)
	if err != nil || terminal.Call.Status != "ended" {
		t.Fatal(err)
	}
	cb.Status = "in-progress"
	late, err := r.ApplyTwilioCallback(ctx, cb)
	if err != nil || late.Changed || late.Call.Status != "ended" {
		t.Fatal("terminal call reopened")
	}
	events, err := r.EventsAfter(ctx, owner, c.ID, 0, 100)
	if err != nil || len(events) != 4 {
		t.Fatalf("durable callback events=%d %v", len(events), err)
	}
}

func TestIntegrationStartChecksStoredConsentOwnerAndPolicy(t *testing.T) {
	r, ctx := startDB(t)
	owner := NewID()
	c, err := r.Create(ctx, owner, NewID(), realDraft())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReserveCallStart(ctx, NewID(), c.ID, NewID(), startPolicy(), startIntent()); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	for _, policy := range []StartPolicy{{}, {EnableReal: true, AllowedNumbers: []string{"+12255550101"}, AccountSID: startTestAccount}} {
		if _, err = r.ReserveCallStart(ctx, owner, c.ID, NewID(), policy, startIntent()); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
	if _, err = r.pool.Exec(ctx, `UPDATE calls SET consent_acknowledged=false WHERE id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReserveCallStart(ctx, owner, c.ID, NewID(), startPolicy(), startIntent()); !errors.Is(err, ErrInvalid) {
		t.Fatal("stored consent ignored")
	}
	var n int
	if err = r.pool.QueryRow(ctx, `SELECT count(*) FROM call_starts`).Scan(&n); err != nil || n != 0 {
		t.Fatal("blocked request left intent")
	}
	mockCall := create(t, r, ctx, owner)
	intent := startIntent()
	if _, err = r.ReserveCallStart(ctx, owner, mockCall.ID, NewID(), StartPolicy{}, intent); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ApplyTwilioCallback(ctx, TwilioCallback{CallID: mockCall.ID, AttemptID: intent.AttemptID, Token: startTestToken, AccountSID: startTestAccount, CallSID: startTestSID}); !errors.Is(err, ErrNotFound) {
		t.Fatal("mock accepted real callback")
	}
}
