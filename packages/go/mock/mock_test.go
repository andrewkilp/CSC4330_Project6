package mock

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustGenerate(t *testing.T, name string, seed uint64) Fixture {
	t.Helper()
	f, err := Generate(name, seed)
	if err != nil {
		t.Fatalf("Generate(%q, %d): %v", name, seed, err)
	}
	return f
}

func mustEncode(t *testing.T, f Fixture) []byte {
	t.Helper()
	out, err := Encode(f)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNamesAreStableAndCoverBothDomains(t *testing.T) {
	got := strings.Join(Names(), ",")
	if got != AppointmentMissingDetail+","+RestaurantTimeConflict {
		t.Fatalf("Names() = %s", got)
	}
}

func TestSameSeedProducesIdenticalOrderedEvents(t *testing.T) {
	for _, name := range Names() {
		for _, seed := range []uint64{0, 1, DefaultSeed, 1 << 63} {
			a := mustEncode(t, mustGenerate(t, name, seed))
			b := mustEncode(t, mustGenerate(t, name, seed))
			if !bytes.Equal(a, b) {
				t.Fatalf("%s seed %d: two runs differ", name, seed)
			}
		}
	}
}

func TestDifferentSeedKeepsScriptButChangesIdentity(t *testing.T) {
	for _, name := range Names() {
		a, b := mustGenerate(t, name, 1), mustGenerate(t, name, 2)
		if len(a.Events) != len(b.Events) {
			t.Fatalf("%s: event count changed with seed", name)
		}
		if a.Events[0].CallID == b.Events[0].CallID {
			t.Fatalf("%s: seed did not change identifiers", name)
		}
		for i := range a.Events {
			if a.Events[i].Type != b.Events[i].Type || a.Events[i].Seq != b.Events[i].Seq {
				t.Fatalf("%s: event %d order changed with seed", name, i+1)
			}
		}
	}
}

func TestEveryScenarioNeedsOperatorInputAndLeavesAQuestionOpen(t *testing.T) {
	want := map[string]string{RestaurantTimeConflict: "confirmed", AppointmentMissingDetail: "needs_follow_up"}
	for _, name := range Names() {
		f := mustGenerate(t, name, DefaultSeed)
		if f.Expected.Outcome != want[name] {
			t.Errorf("%s outcome = %q, want %q", name, f.Expected.Outcome, want[name])
		}
		if len(f.Expected.UnansweredQuestions) == 0 {
			t.Errorf("%s has no unanswered question", name)
		}
		var instructions, partials, waiting int
		for _, e := range f.Events {
			switch p := e.Payload.(type) {
			case Instruction:
				if p.Status == "applied" {
					instructions++
				}
			case Turn:
				if !p.IsFinal {
					partials++
				}
			case Response:
				if p.NeedsApproval && p.Status == "suggested" {
					waiting++
				}
			}
		}
		if instructions == 0 || partials == 0 || waiting < 2 {
			t.Errorf("%s: applied instructions=%d partial turns=%d responses awaiting operator=%d", name, instructions, partials, waiting)
		}
	}
}

func TestInstructionCancelsWaitingSuggestionBeforeNextRevision(t *testing.T) {
	f := mustGenerate(t, RestaurantTimeConflict, DefaultSeed)
	target := f.Expected.OperatorInputs[0].ResponseID
	var cancelledAt, appliedAt int
	for i, e := range f.Events {
		if r, ok := e.Payload.(Response); ok && r.ID == target && r.Status == "cancelled" {
			cancelledAt = i
		}
		if n, ok := e.Payload.(Instruction); ok && n.Status == "applied" {
			appliedAt = i
			if n.ContextRevision != 1 {
				t.Fatalf("applied instruction revision = %d, want 1", n.ContextRevision)
			}
		}
	}
	if cancelledAt == 0 || cancelledAt > appliedAt {
		t.Fatalf("suggestion cancelled at %d, instruction applied at %d", cancelledAt, appliedAt)
	}
}

func TestOnlyFictionalLabelledData(t *testing.T) {
	for _, name := range Names() {
		f := mustGenerate(t, name, DefaultSeed)
		c := f.Events[0].Payload.(Call)
		if !f.Fictional || c.Mode != "mock" || !strings.HasSuffix(c.BusinessName, "(mock)") || !fictionalPhone.MatchString(c.Destination) {
			t.Errorf("%s: call is not fictional mock data: %+v", name, c)
		}
	}
}

func TestCommittedFixturesMatchGenerator(t *testing.T) {
	for _, name := range Names() {
		path := filepath.Join("..", "..", "..", "docs", "fixtures", name+".json")
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if want := mustEncode(t, mustGenerate(t, name, DefaultSeed)); !bytes.Equal(got, want) {
			t.Fatalf("%s is stale; run scripts/scenarios/generate.sh", path)
		}
		decoded, err := Decode(got)
		if err != nil {
			t.Fatal(err)
		}
		if err := Validate(decoded); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
}

func TestUnknownScenario(t *testing.T) {
	if _, err := Generate("pizza-delivery", 1); err == nil || !strings.Contains(err.Error(), "unknown mock scenario") {
		t.Fatalf("err = %v", err)
	}
}

// Each case corrupts a valid fixture the way a hand edit might, and expects a
// ValidationError naming the field.
func TestValidateRejectsMalformedFixtures(t *testing.T) {
	cases := []struct {
		name   string
		field  string
		mutate func(f *Fixture)
	}{
		{"sequence gap", "seq", func(f *Fixture) { f.Events[4].Seq = 9 }},
		{"duplicate event", "seq", func(f *Fixture) { f.Events = append(f.Events[:5], f.Events[4:]...) }},
		{"real looking destination", "payload.destination", func(f *Fixture) {
			c := f.Events[0].Payload.(Call)
			c.Destination = "+12255551234"
			f.Events[0].Payload = c
		}},
		{"unlabelled business", "payload.business_name", func(f *Fixture) {
			c := f.Events[0].Payload.(Call)
			c.BusinessName = "Bayou Lantern Bistro"
			f.Events[0].Payload = c
		}},
		{"real mode", "payload.mode", func(f *Fixture) {
			c := f.Events[0].Payload.(Call)
			c.Mode = "real"
			f.Events[0].Payload = c
		}},
		{"not fictional", "fictional", func(f *Fixture) { f.Fictional = false }},
		{"revision goes backward", "context_revision", func(f *Fixture) { f.Events[len(f.Events)-2].ContextRevision = 0 }},
		{"time goes backward", "occurred_at", func(f *Fixture) { f.Events[5].OccurredAt = "2026-11-12T22:00:00.000Z" }},
		{"final turn edited later", "payload.is_final", func(f *Fixture) {
			f.Events = append(f.Events[:5], append([]Event{f.Events[4]}, f.Events[5:]...)...)
			renumber(f)
		}},
		{"response skips approval", "payload.status", func(f *Fixture) {
			f.Events = append(f.Events[:7], f.Events[8:]...)
			renumber(f)
		}},
		{"confirmed without evidence", "payload.evidence_turn_ids", func(f *Fixture) {
			last := len(f.Events) - 1
			s := f.Events[last].Payload.(Summary)
			s.EvidenceTurnIDs = nil
			f.Events[last].Payload = s
		}},
		{"evidence is assistant speech", "payload.evidence_turn_ids", func(f *Fixture) {
			last := len(f.Events) - 1
			s := f.Events[last].Payload.(Summary)
			s.EvidenceTurnIDs = []string{f.Events[10].Payload.(Turn).ID}
			f.Events[last].Payload = s
		}},
		{"missing unanswered question", "expected.unanswered_questions", func(f *Fixture) { f.Expected.UnansweredQuestions = nil }},
		{"no operator instruction", "expected.operator_inputs", func(f *Fixture) { f.Expected.OperatorInputs = f.Expected.OperatorInputs[1:] }},
		{"outcome mismatch", "expected.outcome", func(f *Fixture) { f.Expected.Outcome = "declined" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := mustGenerate(t, RestaurantTimeConflict, DefaultSeed)
			f.Events = append([]Event(nil), f.Events...)
			tc.mutate(&f)
			err := Validate(f)
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Field != tc.field {
				t.Fatalf("Validate() = %v, want ValidationError on %s", err, tc.field)
			}
		})
	}
}

func TestDecodeRejectsUnknownFieldsAndTypes(t *testing.T) {
	good := mustEncode(t, mustGenerate(t, AppointmentMissingDetail, DefaultSeed))
	for name, data := range map[string][]byte{
		"unknown field": bytes.Replace(good, []byte(`"is_final"`), []byte(`"surprise": 1, "is_final"`), 1),
		"unknown type":  bytes.Replace(good, []byte(`"usage.updated"`), []byte(`"usage.changed"`), 1),
		"trailing data": append(append([]byte(nil), good...), []byte("{}")...),
		"not json":      []byte("events:\n"),
	} {
		if _, err := Decode(data); err == nil {
			t.Errorf("%s: Decode accepted malformed input", name)
		}
	}
}

func renumber(f *Fixture) {
	for i := range f.Events {
		f.Events[i].Seq = uint64(i + 1)
	}
}

// withPause inserts call.updated events with the given statuses right after the call
// first connects.
func withPause(t *testing.T, statuses ...string) Fixture {
	t.Helper()
	f := mustGenerate(t, RestaurantTimeConflict, DefaultSeed)
	at := -1
	for i, e := range f.Events {
		if c, ok := e.Payload.(Call); ok && c.Status == "connected" {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatal("scenario never connects")
	}
	var inserted []Event
	for _, status := range statuses {
		e := f.Events[at]
		c := e.Payload.(Call)
		c.Status = status
		e.Payload = c
		inserted = append(inserted, e)
	}
	events := append([]Event(nil), f.Events[:at+1]...)
	events = append(events, inserted...)
	f.Events = append(events, f.Events[at+1:]...)
	renumber(&f)
	return f
}

func TestPausedIsAConnectedSubstate(t *testing.T) {
	if err := Validate(withPause(t, "paused", "connected")); err != nil {
		t.Fatalf("pause then resume: %v", err)
	}
	cases := []struct {
		name, field string
		statuses    []string
	}{
		// The first business turn is transcribed while paused, but the next response
		// event starts a generation, which a paused call cannot do.
		{"generate while paused", "payload.status", []string{"paused"}},
		{"paused cannot redial", "payload.status", []string{"paused", "dialing"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ve *ValidationError
			if err := Validate(withPause(t, tc.statuses...)); !errors.As(err, &ve) || ve.Field != tc.field {
				t.Fatalf("Validate() = %v, want ValidationError on %s", err, tc.field)
			}
		})
	}
}

// The validator follows the #1 contract for streamed and retired responses, which the
// live conversation loop produces, while still rejecting real lifecycle violations.
func TestValidateAcceptsContractResponseUpdates(t *testing.T) {
	f := mustGenerate(t, RestaurantTimeConflict, DefaultSeed)
	at := -1
	for i, e := range f.Events {
		if r, ok := e.Payload.(Response); ok && r.Status == "generating" {
			at = i
			break
		}
	}
	gen := f.Events[at]
	draft := gen.Payload.(Response)
	draft.Text = "Hi, this is"
	draft.NeedsApproval, draft.Reason = true, "Operator approval required before speech."
	withDraft := func(mutate func(*Response)) Fixture {
		g := f
		r := draft
		mutate(&r)
		e := gen
		e.Payload = r
		g.Events = append(append(append([]Event(nil), f.Events[:at+1]...), e), f.Events[at+1:]...)
		renumber(&g)
		return g
	}
	// A streamed draft with growing text and a provisional approval requirement.
	if err := Validate(withDraft(func(*Response) {})); err != nil {
		t.Fatalf("streamed draft rejected: %v", err)
	}
	// Still rejected: going backward, and changing approved text.
	for name, mutate := range map[string]func(*Response){
		"suggested before generating": func(r *Response) { r.Status = "approved" },
		"blank suggestion":            func(r *Response) { r.Status, r.Text = "suggested", " " },
	} {
		var ve *ValidationError
		if err := Validate(withDraft(mutate)); !errors.As(err, &ve) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Interrupted speech (#26) records the heard prefix of a cancelled response.
func TestValidateAcceptsInterruptedSpeechPrefix(t *testing.T) {
	base := mustGenerate(t, RestaurantTimeConflict, DefaultSeed)
	// Find the first spoken response and its assistant turn.
	spokenAt, turnAt := -1, -1
	for i, e := range base.Events {
		if r, ok := e.Payload.(Response); ok && r.Status == "spoken" && spokenAt < 0 {
			spokenAt = i
		}
		if tr, ok := e.Payload.(Turn); ok && tr.Speaker == "assistant" && turnAt < 0 {
			turnAt = i
		}
	}
	full := base.Events[spokenAt].Payload.(Response).Text
	build := func(prefix string) Fixture {
		f := base
		f.Events = append([]Event(nil), base.Events...)
		r := f.Events[spokenAt].Payload.(Response)
		r.Status, r.Text = "cancelled", ""
		f.Events[spokenAt].Payload = r
		tr := f.Events[turnAt].Payload.(Turn)
		tr.Text = prefix
		f.Events[turnAt].Payload = tr
		// The scripted approval input targets this response, which is now cancelled.
		var inputs []OperatorInput
		for _, in := range f.Expected.OperatorInputs {
			if in.ResponseID != r.ID {
				inputs = append(inputs, in)
			}
		}
		f.Expected.OperatorInputs = inputs
		return f
	}
	words := strings.Fields(full)
	if err := Validate(build(strings.Join(words[:3], " "))); err != nil {
		t.Fatalf("heard prefix rejected: %v", err)
	}
	for name, text := range map[string]string{
		"half a word":  full[:len(strings.Join(words[:3], " "))+2],
		"not a prefix": "Something never said",
		"empty":        "",
	} {
		var ve *ValidationError
		if err := Validate(build(text)); !errors.As(err, &ve) || ve.Field != "payload.response_id" && ve.Field != "payload.text" {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
}
