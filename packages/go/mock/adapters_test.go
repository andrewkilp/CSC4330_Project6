package mock

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers/providertest"
)

var callbacks = providers.Callbacks{
	Voice:  "https://switchboard.test/webhooks/twilio/voice",
	Status: "https://switchboard.test/webhooks/twilio/status",
	Media:  "wss://switchboard.test/webhooks/twilio/media",
}

func mustProviders(t *testing.T, name string, opts Options) (*Providers, providers.Dial, json.RawMessage) {
	t.Helper()
	p, err := NewProviders(name, DefaultSeed, opts)
	if err != nil {
		t.Fatalf("NewProviders(%q): %v", name, err)
	}
	call := p.Fixture.Events[0].Payload.(Call)
	goal, err := json.Marshal(call.Goal)
	if err != nil {
		t.Fatal(err)
	}
	return p, providers.Dial{CallID: call.ID, Destination: call.Destination, Callbacks: callbacks}, goal
}

// The shared contract suite runs unchanged against every scenario's mock adapters.
func TestMockAdaptersPassProviderContract(t *testing.T) {
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			providertest.Run(t, func(t *testing.T) providertest.Adapters {
				p, dial, goal := mustProviders(t, name, Options{})
				return providertest.Adapters{Telephony: p.Telephony, Transcriber: p.Transcriber,
					Responder: p.Responder, Synthesizer: p.Synthesizer, Dial: dial, Goal: goal, Voice: "mock-voice"}
			})
		})
	}
}

func wait[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

type replay struct {
	businessIDs     []string
	businessFinals  []string
	partials        map[string]int
	spoken          []string
	instructionsRun []string
}

// runScenario drives one whole scenario through the four adapters the way the server
// will: audio -> transcript -> generation -> (operator instruction) -> speech -> mark.
func runScenario(t *testing.T, name string) (replay, *Providers) {
	p, dial, goal := mustProviders(t, name, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	id, err := p.Telephony.Start(ctx, dial)
	if err != nil {
		t.Fatal(err)
	}
	media := make(chan providers.MediaEvent, 1<<16)
	stream, err := p.Telephony.Media(ctx, id, func(e providers.MediaEvent) error { media <- e; return nil })
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	out := replay{partials: map[string]int{}}
	finals := make(chan providers.TranscriptUpdate, 16)
	session, err := p.Transcriber.Open(ctx, providers.TranscriptOpen{CallID: dial.CallID, Format: providers.TelephonyFormat},
		func(u providers.TranscriptUpdate) error {
			mu.Lock()
			defer mu.Unlock()
			if u.IsFinal {
				finals <- u
			} else {
				out.partials[u.TurnID]++
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	marks, stopped := make(chan string, 16), make(chan struct{})
	go func() {
		for e := range media {
			switch e.Kind {
			case providers.MediaAudio:
				if err := session.Push(ctx, e.Audio); err != nil {
					t.Errorf("Push: %v", err)
				}
			case providers.MediaMark:
				marks <- e.Mark
			case providers.MediaStop:
				close(stopped)
				return
			}
		}
	}()

	pendingInstruction := map[string]string{}
	for _, in := range p.Fixture.Expected.OperatorInputs {
		if in.Kind == "instruction" {
			pendingInstruction[in.ResponseID] = in.Text
		}
	}
	responseID := map[string]string{}
	for _, r := range p.Script() {
		responseID[fmt.Sprint(r.BusinessTurns, r.Instructions)] = r.ResponseID
	}

	var transcript []providers.TranscriptTurn
	var instructions []string
	revision, generation := uint64(0), uint64(0)
	generate := func() (string, error) {
		var final string
		err := p.Responder.Generate(ctx, providers.ResponseRequest{CallID: dial.CallID, Goal: goal,
			Transcript: transcript, Instructions: instructions, ContextRevision: revision},
			func(d providers.ResponseDraft) error {
				if d.ContextRevision != revision {
					t.Errorf("draft for revision %d, want %d", d.ContextRevision, revision)
				}
				if d.Final {
					final = d.Text
				}
				return nil
			})
		return final, err
	}
	business := 0
	for {
		turn := wait(t, finals, "final business turn")
		business++
		out.businessIDs = append(out.businessIDs, turn.TurnID)
		out.businessFinals = append(out.businessFinals, turn.Text)
		transcript = append(transcript, providers.TranscriptTurn{Speaker: providers.SpeakerBusiness, Text: turn.Text})

		reply, err := generate()
		if errors.Is(err, ErrNoScriptedReply) {
			break // the business's closing line needs no reply
		}
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		// The operator's instruction supersedes the waiting suggestion.
		if text, ok := pendingInstruction[responseID[fmt.Sprint(business, instructions)]]; ok {
			instructions = append(instructions, text)
			out.instructionsRun = append(out.instructionsRun, text)
			revision++
			if reply, err = generate(); err != nil {
				t.Fatalf("Generate after instruction: %v", err)
			}
		}

		generation++
		token := providers.Token{ContextRevision: revision, Generation: generation}
		err = p.Synthesizer.Stream(ctx, providers.SpeechRequest{Text: reply, Voice: "mock-voice", Format: providers.TelephonyFormat, Token: token},
			func(s providers.SpeechChunk) error {
				if s.Token != token {
					return fmt.Errorf("audio from generation %+v", s.Token)
				}
				return stream.Send(ctx, s.Audio)
			})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		name := fmt.Sprintf("reply-%d", generation)
		if err := stream.Mark(ctx, name); err != nil {
			t.Fatal(err)
		}
		if got := wait(t, marks, name); got != name {
			t.Fatalf("mark %q, want %q", got, name)
		}
		out.spoken = append(out.spoken, reply)
		transcript = append(transcript, providers.TranscriptTurn{Speaker: providers.SpeakerAssistant, Text: reply})
	}
	if err := p.Telephony.End(ctx, id); err != nil {
		t.Fatal(err)
	}
	wait(t, stopped, "stop")
	_ = session.Close()
	mu.Lock()
	defer mu.Unlock()
	return out, p
}

func TestScenariosReplayThroughAllFourAdapters(t *testing.T) {
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			got, p := runScenario(t, name)
			var wantIDs, wantBusiness, wantSpoken, wantInstructions []string
			wantPartials := map[string]int{}
			for _, e := range p.Fixture.Events {
				switch v := e.Payload.(type) {
				case Turn:
					switch {
					case v.Speaker == "business" && v.IsFinal:
						wantIDs = append(wantIDs, v.ID)
						wantBusiness = append(wantBusiness, v.Text)
					case v.Speaker == "business":
						wantPartials[v.ID]++
					case v.IsFinal:
						wantSpoken = append(wantSpoken, v.Text)
					}
				case Instruction:
					if v.Status == "applied" {
						wantInstructions = append(wantInstructions, v.Text)
					}
				}
			}
			check := func(what string, got, want any) {
				t.Helper()
				g, _ := json.Marshal(got)
				w, _ := json.Marshal(want)
				if !bytes.Equal(g, w) {
					t.Errorf("%s:\n got %s\nwant %s", what, g, w)
				}
			}
			check("business turn ids", got.businessIDs, wantIDs)
			check("business finals", got.businessFinals, wantBusiness)
			check("partials per turn", got.partials, wantPartials)
			check("spoken replies", got.spoken, wantSpoken)
			check("instructions", got.instructionsRun, wantInstructions)
		})
	}
}

// pacer cancels exactly when the given callback number is about to run.
func pacer(cancelAt int, cancel context.CancelFunc) func(context.Context) error {
	var mu sync.Mutex
	n := 0
	return func(ctx context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		n++
		if n == cancelAt {
			cancel()
		}
		return ctx.Err()
	}
}

func TestCancelStopsDraftsAndAudioAtAnExactPoint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p, dial, goal := mustProviders(t, RestaurantTimeConflict, Options{Pace: pacer(3, cancel)})
	drafts := 0
	err := p.Responder.Generate(ctx, providers.ResponseRequest{CallID: dial.CallID, Goal: goal,
		Transcript: []providers.TranscriptTurn{{Speaker: providers.SpeakerBusiness, Text: "Bayou Lantern Bistro, how can I help you?"}}},
		func(providers.ResponseDraft) error { drafts++; return nil })
	if !errors.Is(err, context.Canceled) || drafts != 2 {
		t.Fatalf("Generate: err %v after %d drafts, want Canceled after 2", err, drafts)
	}

	ctx, cancel = context.WithCancel(context.Background())
	p, _, _ = mustProviders(t, RestaurantTimeConflict, Options{Pace: pacer(3, cancel)})
	chunks := 0
	err = p.Synthesizer.Stream(ctx, providers.SpeechRequest{Text: "8:45 works. Please book an indoor table for four at 8:45 under Jordan Lee.",
		Voice: "mock-voice", Format: providers.TelephonyFormat}, func(providers.SpeechChunk) error { chunks++; return nil })
	if !errors.Is(err, context.Canceled) || chunks != 2 {
		t.Fatalf("Stream: err %v after %d chunks, want Canceled after 2", err, chunks)
	}
}

// Clear and End stop outbound audio mid-reply: nothing further plays and the reply's
// mark is never reported.
func TestClearAndHangUpStopPlaybackMidReply(t *testing.T) {
	for _, stop := range []string{"clear", "end"} {
		t.Run(stop, func(t *testing.T) {
			p, dial, _ := mustProviders(t, RestaurantTimeConflict, Options{})
			ctx := context.Background()
			id, err := p.Telephony.Start(ctx, dial)
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var events []providers.MediaEvent
			ms, err := p.Telephony.Media(ctx, id, func(e providers.MediaEvent) error {
				mu.Lock()
				events = append(events, e)
				mu.Unlock()
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			stream := ms.(*ScenarioMediaStream)
			err = p.Synthesizer.Stream(ctx, providers.SpeechRequest{Text: p.Script()[0].Text, Voice: "mock-voice", Format: providers.TelephonyFormat},
				func(s providers.SpeechChunk) error { return stream.Send(ctx, s.Audio) })
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Mark(ctx, "reply"); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for stream.Played() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if stop == "clear" {
				if dropped, err := stream.Clear(ctx); err != nil || dropped == 0 {
					t.Fatalf("Clear dropped %d, err %v", dropped, err)
				}
			} else if err := p.Telephony.End(ctx, id); err != nil {
				t.Fatal(err)
			}
			time.Sleep(10 * time.Millisecond)
			played := stream.Played()
			time.Sleep(100 * time.Millisecond)
			if after := stream.Played(); after != played {
				t.Fatalf("%d more bytes played after %s", after-played, stop)
			}
			mu.Lock()
			defer mu.Unlock()
			for _, e := range events {
				if e.Kind == providers.MediaMark {
					t.Fatalf("mark %q reported for audio stopped by %s", e.Mark, stop)
				}
			}
			if stop == "end" && events[len(events)-1].Kind != providers.MediaStop {
				t.Fatalf("last event after End is %s, want stop", events[len(events)-1].Kind)
			}
		})
	}
}

func TestMockTelephonyOnlyDialsFictionalNumbers(t *testing.T) {
	p, dial, _ := mustProviders(t, AppointmentMissingDetail, Options{})
	for _, number := range []string{"+12255551234", "+12255550200", "+442071838750"} {
		d := dial
		d.Destination = number
		if _, err := p.Telephony.Start(context.Background(), d); !errors.Is(err, providers.ErrInvalid) {
			t.Errorf("Start(%s) = %v, want ErrInvalid", number, err)
		}
	}
}

func TestUnscriptedRequestsFailVisibly(t *testing.T) {
	p, dial, goal := mustProviders(t, AppointmentMissingDetail, Options{})
	called := false
	err := p.Responder.Generate(context.Background(), providers.ResponseRequest{CallID: dial.CallID, Goal: goal,
		Transcript: []providers.TranscriptTurn{{Speaker: providers.SpeakerBusiness, Text: "Hello?"}}, Instructions: []string{"Book Friday instead."}},
		func(providers.ResponseDraft) error { called = true; return nil })
	if !errors.Is(err, ErrNoScriptedReply) || called {
		t.Fatalf("unscripted request: err %v, called %v", err, called)
	}
}

func TestAdaptersAreDeterministic(t *testing.T) {
	run := func() []byte {
		p, dial, _ := mustProviders(t, RestaurantTimeConflict, Options{})
		id, err := p.Telephony.Start(context.Background(), dial)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		buf.WriteString(id)
		err = p.Synthesizer.Stream(context.Background(), providers.SpeechRequest{Text: p.Script()[1].Text, Voice: "v", Format: providers.TelephonyFormat},
			func(s providers.SpeechChunk) error { buf.Write(s.Audio.Data); return nil })
		if err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	if !bytes.Equal(run(), run()) {
		t.Fatal("same seed produced different provider ids or audio")
	}
}
