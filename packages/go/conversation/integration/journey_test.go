package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/mock"
)

// TestJourneys runs both scenarios end to end on the mock provider stack. Each run's
// event transcript passes mock.Validate (contract shape, ordering, state progressions,
// evidence and the scenario's operator inputs), then the journey milestones are
// checked against the scenario script. Set SWITCHBOARD_JOURNEY_DIR to keep the
// transcripts; `go test -v` prints them.
func TestJourneys(t *testing.T) {
	for _, name := range mock.Names() {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			got, err := Run(ctx, name, mock.DefaultSeed)
			if err != nil {
				t.Fatal(err)
			}
			script, _ := mock.Generate(name, mock.DefaultSeed)
			var wantBusiness, wantSpoken, wantInstructions []string
			for _, e := range script.Events {
				switch v := e.Payload.(type) {
				case mock.Turn:
					if v.IsFinal && v.Speaker == "business" {
						wantBusiness = append(wantBusiness, v.Text)
					} else if v.IsFinal {
						wantSpoken = append(wantSpoken, v.Text)
					}
				case mock.Instruction:
					if v.Status == "applied" {
						wantInstructions = append(wantInstructions, v.Text)
					}
				}
			}
			if !slices.Equal(got.Business, wantBusiness) {
				t.Errorf("business turns:\n got %q\nwant %q", got.Business, wantBusiness)
			}
			if !slices.Equal(got.Spoken, wantSpoken) {
				t.Errorf("spoken replies:\n got %q\nwant %q", got.Spoken, wantSpoken)
			}
			if !slices.Equal(got.Instructions, wantInstructions) {
				t.Errorf("instructions:\n got %q\nwant %q", got.Instructions, wantInstructions)
			}
			if !got.Paused || !got.HungUp {
				t.Errorf("paused=%v hung up=%v", got.Paused, got.HungUp)
			}
			checkMilestones(t, got.Transcript, script.Expected.Outcome)
			writeTranscript(t, name, got.Transcript)
		})
	}
}

// checkMilestones asserts the journey covered every documented step, in order.
func checkMilestones(t *testing.T, f mock.Fixture, outcome string) {
	t.Helper()
	var statuses []string
	instructionAt, changedAt, lastSpoken := -1, -1, -1
	cancelledByPause := false
	for i, e := range f.Events {
		switch v := e.Payload.(type) {
		case mock.Call:
			statuses = append(statuses, v.Status)
		case mock.Instruction:
			if v.Status == "applied" && instructionAt < 0 {
				instructionAt = i
			}
		case mock.Response:
			if instructionAt >= 0 && changedAt < 0 && v.Status == "suggested" && v.ContextRevision > 0 {
				changedAt = i
			}
			if v.Status == "cancelled" && len(statuses) > 0 && i+1 < len(f.Events) {
				if next, ok := f.Events[i+1].Payload.(mock.Call); ok && next.Status == "paused" {
					cancelledByPause = true
				}
			}
		case mock.Turn:
			if v.Speaker == "assistant" {
				lastSpoken = i
			}
		}
	}
	want := []string{"draft", "dialing", "connected", "paused", "connected", "ended"}
	if !slices.Equal(statuses, want) {
		t.Errorf("call statuses %v, want %v", statuses, want)
	}
	if instructionAt < 0 || changedAt < instructionAt {
		t.Error("no changed suggestion after the private instruction")
	}
	if !cancelledByPause {
		t.Error("pause did not cancel the waiting suggestion before the paused event")
	}
	last := f.Events[len(f.Events)-1]
	if s, ok := last.Payload.(mock.Summary); !ok || s.Outcome != outcome || lastSpoken < 0 {
		t.Errorf("journey must end with the %s summary after spoken replies; last event %s", outcome, last.Type)
	}
}

func writeTranscript(t *testing.T, name string, f mock.Fixture) {
	t.Helper()
	for _, e := range f.Events {
		payload, _ := json.Marshal(e.Payload)
		line := string(payload)
		if len(line) > 140 {
			line = line[:140] + "…"
		}
		t.Logf("%3d %-20s rev=%d %s", e.Seq, e.Type, e.ContextRevision, line)
	}
	dir := os.Getenv("SWITCHBOARD_JOURNEY_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	data, err := mock.Encode(f)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, strings.ReplaceAll(name, "/", "_")+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("transcript: %s (%d events)", path, len(f.Events))
}

func TestJourneysRejectUnknownScenarios(t *testing.T) {
	if _, err := Run(context.Background(), "bulk-campaign", mock.DefaultSeed); err == nil {
		t.Fatal("an unknown scenario ran")
	}
}
