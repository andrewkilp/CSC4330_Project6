package demo_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/demo"
)

type frame struct {
	Snapshot struct {
		Call struct {
			ID       string `json:"id"`
			Status   string `json:"status"`
			Revision uint64 `json:"context_revision"`
		} `json:"call"`
		Replies []demo.Reply `json:"responses"`
		Turns   []demo.Turn  `json:"turns"`
		Summary struct {
			Outcome  string   `json:"outcome"`
			Evidence []string `json:"evidence_turn_ids"`
		} `json:"summary"`
		Seq int `json:"last_seq"`
	} `json:"snapshot"`
	Events []demo.Event `json:"events"`
	Demo   struct {
		Phase  string `json:"phase"`
		Paused bool   `json:"paused"`
	} `json:"demo"`
}

func read(t *testing.T, b []byte, err error) frame {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	var f frame
	if err = json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}
func wait(t *testing.T, svc *demo.Service, id, phase string) frame {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		b, err := svc.Read(id, 0)
		f := read(t, b, err)
		if f.Demo.Phase == phase {
			return f
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("demo did not reach %s", phase)
	return frame{}
}
func TestSteerApproveAndConfirmBothScenarios(t *testing.T) {
	for _, tc := range []struct{ scenario, instruction, want string }{{"restaurant", "Accept 7:30; indoor seating please.", "indoor"}, {"appointment", "Thursday at 3; just a trim.", "trim"}} {
		t.Run(tc.scenario, func(t *testing.T) {
			svc := demo.New(time.Millisecond)
			b, err := svc.Start(tc.scenario)
			f := read(t, b, err)
			id := f.Snapshot.Call.ID
			f = wait(t, svc, id, "awaiting_approval")
			old := f.Snapshot.Replies[len(f.Snapshot.Replies)-1]
			if len(f.Snapshot.Turns) != 2 {
				t.Fatal("unapproved suggestion entered transcript")
			}
			b, err = svc.Act(id, demo.Action{Action: "instruction", Text: tc.instruction, Revision: 0})
			f = read(t, b, err)
			reply := f.Snapshot.Replies[len(f.Snapshot.Replies)-1]
			if reply.ID == old.ID || reply.Revision != 1 || !strings.Contains(reply.Text, tc.want) {
				t.Fatalf("instruction did not revise reply: %+v", reply)
			}
			if _, err = svc.Act(id, demo.Action{Action: "approve", Text: old.Text, ResponseID: old.ID, Revision: 0}); !errors.Is(err, demo.ErrConflict) {
				t.Fatal("stale approval accepted")
			}
			b, err = svc.Act(id, demo.Action{Action: "approve", Text: reply.Text, ResponseID: reply.ID, Revision: 1})
			read(t, b, err)
			f = wait(t, svc, id, "ended")
			if f.Snapshot.Summary.Outcome != "confirmed" || len(f.Snapshot.Summary.Evidence) != 1 {
				t.Fatalf("missing evidenced outcome: %+v", f.Snapshot.Summary)
			}
			if len(f.Snapshot.Turns) != 4 || !strings.Contains(f.Snapshot.Turns[3].Text, tc.want) {
				t.Fatal("business did not confirm the approved preference")
			}
			for i, event := range f.Events {
				if event.Seq != i+1 || event.CallID != id {
					t.Fatal("event sequence is not contiguous")
				}
			}
		})
	}
}
func TestPauseInstructionResumeAndTerminalEnd(t *testing.T) {
	svc := demo.New(3 * time.Millisecond)
	b, err := svc.Start("restaurant")
	f := read(t, b, err)
	id := f.Snapshot.Call.ID
	wait(t, svc, id, "awaiting_approval")
	b, err = svc.Act(id, demo.Action{Action: "pause"})
	f = read(t, b, err)
	if !f.Demo.Paused {
		t.Fatal("pause not applied")
	}
	b, err = svc.Act(id, demo.Action{Action: "instruction", Text: "Patio seating, please.", Revision: 0})
	f = read(t, b, err)
	if !f.Demo.Paused || f.Snapshot.Call.Revision != 1 {
		t.Fatal("instruction lost pause or revision")
	}
	b, err = svc.Act(id, demo.Action{Action: "resume"})
	f = read(t, b, err)
	reply := f.Snapshot.Replies[len(f.Snapshot.Replies)-1]
	b, err = svc.Act(id, demo.Action{Action: "approve", Text: reply.Text, ResponseID: reply.ID, Revision: 1})
	read(t, b, err)
	b, err = svc.Act(id, demo.Action{Action: "end"})
	f = read(t, b, err)
	seq := f.Snapshot.Seq
	time.Sleep(30 * time.Millisecond)
	b, err = svc.Read(id, 0)
	f = read(t, b, err)
	if f.Snapshot.Call.Status != "ended" || f.Snapshot.Seq != seq || f.Snapshot.Summary.Outcome != "cancelled" {
		t.Fatal("late timer changed ended session")
	}
	if _, err = svc.Act(id, demo.Action{Action: "resume"}); !errors.Is(err, demo.ErrConflict) {
		t.Fatal("terminal call reopened")
	}
}
func TestUnknownDirectionsNeverInventConfirmation(t *testing.T) {
	svc := demo.New(time.Millisecond)
	b, err := svc.Start("restaurant")
	f := read(t, b, err)
	id := f.Snapshot.Call.ID
	wait(t, svc, id, "awaiting_approval")
	b, err = svc.Act(id, demo.Action{Action: "instruction", Text: "Ask about something outside the scripted examples", Revision: 0})
	f = read(t, b, err)
	reply := f.Snapshot.Replies[len(f.Snapshot.Replies)-1]
	b, err = svc.Act(id, demo.Action{Action: "approve", Text: reply.Text, ResponseID: reply.ID, Revision: 1})
	read(t, b, err)
	f = wait(t, svc, id, "ended")
	if f.Snapshot.Summary.Outcome != "needs_follow_up" {
		t.Fatal("invented a confirmed booking")
	}
	if _, err = svc.Start("real"); !errors.Is(err, demo.ErrInput) {
		t.Fatal("unrecognized scenario accepted")
	}
	if _, err = svc.Read("missing", 0); !errors.Is(err, demo.ErrNotFound) {
		t.Fatal("missing session accepted")
	}
}
