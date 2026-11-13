package mock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/claude"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/conversation"
)

func TestConversationFixtureIntegration(t *testing.T) {
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			loop, f, err := NewConversation(name, DefaultSeed)
			if err != nil {
				t.Fatal(err)
			}
			responder, err := NewResponder(name, DefaultSeed)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, event := range f.Events {
				switch p := event.Payload.(type) {
				case Turn:
					if p.Speaker != "business" {
						continue
					}
					// The final goodbye has no scripted next reply. All earlier partial/final
					// updates exercise the actual loop, not fixture response-state replay.
					if p.IsFinal {
						known := false
						for _, r := range responder.replies {
							if r.business == p.Text {
								known = true
								break
							}
						}
						if !known {
							continue
						}
					}
					if err = loop.ObserveBusinessTurn(context.Background(), conversation.BusinessTurn{ID: p.ID, CallID: p.CallID, Text: p.Text, IsFinal: p.IsFinal}); err != nil {
						t.Fatal(err)
					}
				case Instruction:
					if p.Status == "applied" {
						if err = loop.Instruct(context.Background(), p.Text, p.ContextRevision-1); err != nil {
							t.Fatal(err)
						}
					}
				case Response:
					if p.Status != "suggested" {
						continue
					}
					snap := loop.Snapshot()
					events := loop.Events()
					reply := events[len(events)-1].Payload
					if snap.Response.Status != conversation.Suggested || reply.Text != p.Text || reply.ContextRevision != p.ContextRevision || !reply.NeedsApproval {
						t.Fatalf("fixture suggestion mismatch: %+v", reply)
					}
					count++
				}
			}
			if count != 4 {
				t.Fatalf("covered %d suggestions, want 4", count)
			}
			cancelled := 0
			for _, event := range loop.Events() {
				switch event.Payload.Status {
				case conversation.ResponseCancelled:
					cancelled++
				case conversation.Approved, conversation.Speaking, conversation.Spoken:
					t.Fatal("loop crossed approval boundary")
				}
			}
			if cancelled < 1 {
				t.Fatal("instructions did not cancel pending suggestion")
			}
		})
	}
}

func TestOfflineRunnerStopsAtApproval(t *testing.T) {
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if err := RunConversation(context.Background(), name, DefaultSeed, &out); err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(&out)
			var last conversation.ResponseEvent
			count := 0
			for decoder.More() {
				var event conversation.ResponseEvent
				if err := decoder.Decode(&event); err != nil {
					t.Fatal(err)
				}
				last = event
				count++
				if event.Payload.Status != conversation.Generating && event.Payload.Status != conversation.Suggested {
					t.Fatal("unexpected state")
				}
			}
			if count != 4 || last.Payload.Status != conversation.Suggested || !last.Payload.NeedsApproval {
				t.Fatalf("count %d, last %+v", count, last)
			}
		})
	}
}

func TestMockCancellationAndUnscriptedInput(t *testing.T) {
	r, err := NewResponder(RestaurantTimeConflict, DefaultSeed)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := Generate(RestaurantTimeConflict, DefaultSeed)
	goal, _ := json.Marshal(f.Events[0].Payload.(Call).Goal)
	in := claude.Input{Goal: goal, Transcript: []claude.Turn{{Speaker: "business", Text: r.replies[0].business}}}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err = r.Generate(ctx, in, func(update claude.Update) error { calls++; cancel(); return nil })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("%v callbacks=%d", err, calls)
	}
	in.Transcript[0].Text = "Unknown business question"
	if err = r.Generate(context.Background(), in, func(claude.Update) error { t.Fatal("unscripted reply invented"); return nil }); err != ErrUnscripted {
		t.Fatal(err)
	}
}
