package mock

import (
	"fmt"
	"slices"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/conversation"
)

const (
	// RestaurantTimeConflict: the requested time is full and the business asks a
	// question the goal cannot answer.
	RestaurantTimeConflict = "restaurant-time-conflict"
	// AppointmentMissingDetail: the business needs a callback number the goal lacks.
	AppointmentMissingDetail = "appointment-missing-detail"

	// DefaultSeed is the seed used for the committed fixtures in docs/fixtures.
	DefaultSeed uint64 = 4330
)

type scenario struct {
	description string
	build       func(source) *builder
}

var scenarios = map[string]scenario{
	RestaurantTimeConflict: {
		description: "Dinner for four at 7:00 PM is full. The operator chooses 8:45 PM, and the business asks an allergy question the goal cannot answer.",
		build:       restaurantTimeConflict,
	},
	AppointmentMissingDetail: {
		description: "A haircut slot is open, but the salon needs a callback number the goal does not contain. The operator declines to share one, so the call needs follow-up.",
		build:       appointmentMissingDetail,
	},
}

// Names lists every scenario in stable order.
func Names() []string {
	names := make([]string, 0, len(scenarios))
	for name := range scenarios {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Generate expands one scenario. The same name and seed always produce identical
// ordered events; the seed changes identifiers and timing jitter only.
func Generate(name string, seed uint64) (Fixture, error) {
	s, ok := scenarios[name]
	if !ok {
		return Fixture{}, fmt.Errorf("unknown mock scenario %q (known: %v)", name, Names())
	}
	b := s.build(source{seed: seed, scenario: name})
	if b.err != nil {
		return Fixture{}, b.err
	}
	f := Fixture{Scenario: name, Description: s.description, Seed: seed, Fictional: true, Expected: b.expected, Events: b.events}
	if err := Validate(f); err != nil {
		return Fixture{}, fmt.Errorf("mock scenario %q produced an invalid fixture: %w", name, err)
	}
	return f, nil
}

func intPtr(v int) *int { return &v }

func restaurantTimeConflict(src source) *builder {
	const allergyQuestion = "Will anyone in the party need a high chair or have allergies we should know about?"
	b := newBuilder(src, time.Date(2026, 11, 12, 23, 0, 0, 0, time.UTC), Call{
		Destination:  "+12255550142",
		BusinessName: "Bayou Lantern Bistro (mock)",
		ApprovalMode: "uncertain",
		Goal: Goal{
			Kind:        "restaurant",
			PreferredAt: "2026-11-14T19:00:00-06:00",
			Timezone:    "America/Chicago",
			PartySize:   intPtr(4),
			Constraints: []string{"Indoor seating"},
			ContactName: "Jordan Lee",
		},
	})
	b.transition(conversation.Dialing)
	b.transition(conversation.Connected)

	b.business([]string{"Bayou Lantern"}, "Bayou Lantern Bistro, how can I help you?")
	b.suggest("Hi, this is an AI assistant calling for Jordan Lee. I'd like to book an indoor table for four this Saturday at 7 PM.", false, "")
	b.approve("")

	b.business(
		[]string{"Let me check", "Let me check. Seven is fully booked"},
		"Let me check. Seven is fully booked for four. I can do 6:15 or 8:45.",
	)
	b.suggest("Could you hold on one moment while I check which time works?", true,
		"The business offered 6:15 PM or 8:45 PM; the authorized goal only covers 7:00 PM.")
	b.instruct("8:45 is fine. Do not accept 6:15.",
		"Choosing an alternative time is outside the authorized goal.")
	b.suggest("8:45 works. Please book an indoor table for four at 8:45 under Jordan Lee.", false, "")
	b.approve("")

	confirmation := b.business(
		[]string{"Okay, you're booked", "Okay, you're booked for four at 8:45 Saturday"},
		"Okay, you're booked for four at 8:45 Saturday under Jordan Lee. Your confirmation is BLB-2048. "+allergyQuestion,
	)
	b.suggest("I don't have that information right now. Jordan will let you know before Saturday.", true,
		"The business asked about allergies and a high chair; the goal has no such details and the assistant must not invent them.")
	b.approve("The reply defers a question the goal cannot answer.")

	b.business(nil, "No problem. See you Saturday.")
	b.expected.UnansweredQuestions = []string{allergyQuestion}
	b.end(Summary{
		Outcome: "confirmed",
		ConfirmedDetails: ConfirmedDetails{
			BusinessName:          b.call.BusinessName,
			ScheduledAt:           "2026-11-14T20:45:00-06:00",
			Timezone:              "America/Chicago",
			PartySize:             intPtr(4),
			ConfirmationReference: "BLB-2048",
		},
		EvidenceTurnIDs:     []string{confirmation},
		UnresolvedQuestions: []string{allergyQuestion},
		NextSteps:           []string{"Tell the restaurant about any allergies or a high chair before Saturday."},
	})
	return b
}

func appointmentMissingDetail(src source) *builder {
	const callbackQuestion = "What callback number should the salon use to hold the appointment?"
	b := newBuilder(src, time.Date(2026, 11, 13, 15, 30, 0, 0, time.UTC), Call{
		Destination:  "+12255550187",
		BusinessName: "Cypress Lane Salon (mock)",
		ApprovalMode: "uncertain",
		Goal: Goal{
			Kind:        "appointment",
			PreferredAt: "2026-11-17T10:00:00-06:00",
			Timezone:    "America/Chicago",
			Service:     "Haircut",
			Constraints: []string{"Weekday mornings only"},
			ContactName: "Sam Rivera",
		},
	})
	b.transition(conversation.Dialing)
	b.transition(conversation.Connected)

	b.business([]string{"Cypress Lane"}, "Cypress Lane Salon, this is the front desk.")
	b.suggest("Hi, this is an AI assistant calling for Sam Rivera. I'd like to book a haircut on Tuesday at 10 AM.", false, "")
	b.approve("")

	b.business(
		[]string{"Tuesday at ten", "Tuesday at ten is open. I just need"},
		"Tuesday at ten is open. I just need a callback number to hold it.",
	)
	b.suggest("One moment while I check on that.", true,
		"The business asked for a callback number; the goal has no contact phone and the assistant must not invent one.")
	b.instruct("Don't share a phone number. Ask if they can hold it under the name only.",
		"Only the operator can decide whether to share contact details.")
	b.suggest("Sam would prefer not to share a number on this call. Could you hold Tuesday at 10 under the name Sam Rivera?", false, "")
	b.approve("")

	refusal := b.business(
		[]string{"Sorry, we can't", "Sorry, we can't hold an appointment"},
		"Sorry, we can't hold an appointment without a phone number. Can Sam call us back to finish booking?",
	)
	b.suggest("Yes, Sam will call back to finish booking. Thank you.", true,
		"Agreeing to a follow-up call is a commitment outside the authorized goal.")
	b.approve("The reply commits Sam to a follow-up call.")

	b.business(nil, "Okay, thanks. Bye.")
	b.expected.UnansweredQuestions = []string{callbackQuestion}
	b.end(Summary{
		Outcome: "needs_follow_up",
		ConfirmedDetails: ConfirmedDetails{
			BusinessName: b.call.BusinessName,
			Timezone:     "America/Chicago",
			Service:      "Haircut",
		},
		EvidenceTurnIDs:     []string{refusal},
		UnresolvedQuestions: []string{callbackQuestion},
		NextSteps:           []string{"Sam calls the salon to finish booking Tuesday at 10:00 AM."},
	})
	return b
}
