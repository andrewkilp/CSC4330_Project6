package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func draft() Draft {
	n := 4
	return Draft{Mode: "mock", Destination: "+12255550100", BusinessName: "Fictional Bistro (mock)", ApprovalMode: "always", ConsentAcknowledged: true, Goal: Goal{Kind: "restaurant", PreferredAt: "2026-11-20T19:00:00-06:00", Timezone: "America/Chicago", PartySize: &n, Constraints: []string{}, ContactName: "Fictional Guest"}}
}

func TestGoalValidation(t *testing.T) {
	g := draft().Goal
	if err := g.validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Goal)
	}{
		{"offset mismatch", func(g *Goal) { g.PreferredAt = "2026-11-20T19:00:00-05:00" }},
		{"timezone", func(g *Goal) { g.Timezone = "Not/AZone" }},
		{"restaurant service", func(g *Goal) { g.Service = "haircut" }},
		{"emoji units", func(g *Goal) { g.ContactName = strings.Repeat("😀", 61) }},
		{"invalid contact", func(g *Goal) { g.ContactPhone = "555" }},
		{"constraint", func(g *Goal) { g.Constraints = []string{""} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := draft().Goal
			tc.change(&g)
			if !errors.Is(g.validate(), ErrInvalid) {
				t.Fatal("invalid goal accepted")
			}
		})
	}
}

func TestCanonicalRequestHashIsStable(t *testing.T) {
	a, err := canonical(json.RawMessage(` {"b": 2, "a":1} `), 16384)
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonical(json.RawMessage(`{"a":1,"b":2}`), 16384)
	if err != nil || string(a) != string(b) {
		t.Fatal("object order/whitespace changed hash input")
	}
	for _, bad := range []json.RawMessage{nil, []byte("null"), []byte("[]"), []byte("{}{}")} {
		if _, err := canonical(bad, 16384); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestExpiredTransactionRejectsUse(t *testing.T) {
	tx := &Tx{}
	if !errors.Is(tx.PutTurn(Turn{}), ErrConflict) {
		t.Fatal("expired transaction used")
	}
	if !errors.Is(tx.SetCall(0, "connected", 0), ErrConflict) {
		t.Fatal("expired transaction used")
	}
	if _, err := tx.AppendEvent("usage.updated", map[string]any{}); !errors.Is(err, ErrConflict) {
		t.Fatal("expired transaction used")
	}
}

func TestSafeErrorsDoNotExposeDriverOrCallbackDetails(t *testing.T) {
	for _, err := range []error{errors.New("private connection string"), fmt.Errorf("private callback value: %w", ErrConflict)} {
		if strings.Contains(safe(err).Error(), "private") {
			t.Fatal("private error detail escaped")
		}
	}
}
