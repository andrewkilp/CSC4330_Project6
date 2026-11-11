// Package providertest is the shared contract suite for provider adapters. Every
// adapter set (the mocks now; real adapters with recorded fixtures later) runs the same
// lifecycle, cancellation and failure checks, so a mock can never promise behavior a
// real adapter does not have. The suite places no real calls by itself: it only uses
// the adapters and Dial the factory supplies.
package providertest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
)

// Adapters is one fresh, independent set of providers for a subtest.
type Adapters struct {
	Telephony   providers.Telephony
	Transcriber providers.Transcriber
	Responder   providers.Responder
	Synthesizer providers.Synthesizer
	// Dial must be safe to place: a fictional number for mocks, an allowlisted consenting
	// recipient only in an explicitly labelled manual run.
	Dial providers.Dial
	// Goal is the call's goal JSON for response requests.
	Goal json.RawMessage
	// Voice is the server-side voice id.
	Voice string
}

// Factory returns a new adapter set. It is called once per subtest.
type Factory func(t *testing.T) Adapters

// Wait bounds every asynchronous expectation. Quiet is how long the suite watches for
// callbacks that must not happen.
var (
	Wait  = 5 * time.Second
	Quiet = 100 * time.Millisecond
)

// Run executes the full contract suite.
func Run(t *testing.T, factory Factory) {
	t.Run("lifecycle", func(t *testing.T) { lifecycle(t, factory(t)) })
	t.Run("responder cancel stops drafts", func(t *testing.T) { responderCancel(t, factory(t)) })
	t.Run("synthesizer cancel stops audio", func(t *testing.T) { synthesizerCancel(t, factory(t)) })
	t.Run("transcriber close and cancel stop updates", func(t *testing.T) { transcriberStop(t, factory) })
	t.Run("clear drops queued audio and its marks", func(t *testing.T) { clearDropsAudio(t, factory(t)) })
	t.Run("media cancel and close stop events", func(t *testing.T) { mediaStop(t, factory) })
	t.Run("callback errors stop delivery", func(t *testing.T) { callbackErrors(t, factory(t)) })
	t.Run("invalid requests are rejected before use", func(t *testing.T) { invalidRequests(t, factory(t)) })
}

// ---------------------------------------------------------------------------
// recorder collects callbacks, checks they are never concurrent, and lets a test wait.

type recorder[T any] struct {
	mu       sync.Mutex
	items    []T
	changed  chan struct{}
	inFlight atomic.Int32
	overlap  atomic.Bool
}

func newRecorder[T any]() *recorder[T] { return &recorder[T]{changed: make(chan struct{}, 1)} }

func (r *recorder[T]) add(v T) {
	if r.inFlight.Add(1) > 1 {
		r.overlap.Store(true)
	}
	defer r.inFlight.Add(-1)
	r.mu.Lock()
	r.items = append(r.items, v)
	r.mu.Unlock()
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

func (r *recorder[T]) snapshot() []T {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]T(nil), r.items...)
}

func (r *recorder[T]) len() int { return len(r.snapshot()) }

// waitFor returns the first recorded item (from index from) matching ok.
func (r *recorder[T]) waitFor(t *testing.T, what string, from int, ok func(T) bool) (T, int) {
	t.Helper()
	deadline := time.After(Wait)
	for {
		items := r.snapshot()
		for i := from; i < len(items); i++ {
			if ok(items[i]) {
				return items[i], i
			}
		}
		select {
		case <-r.changed:
		case <-deadline:
			var zero T
			t.Fatalf("timed out waiting for %s", what)
			return zero, -1
		}
	}
}

// quiet waits until no callback is running, then fails if the count changes.
func (r *recorder[T]) quiet(t *testing.T, what string) {
	t.Helper()
	for r.inFlight.Load() != 0 {
		time.Sleep(time.Millisecond)
	}
	before := r.len()
	time.Sleep(Quiet)
	if after := r.len(); after != before {
		t.Fatalf("%s: %d callbacks after it should have stopped", what, after-before)
	}
}

func (r *recorder[T]) checkSerial(t *testing.T, what string) {
	t.Helper()
	if r.overlap.Load() {
		t.Fatalf("%s: callbacks ran concurrently", what)
	}
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 4*Wait)
	t.Cleanup(cancel)
	return c
}

func isMark(name string) func(providers.MediaEvent) bool {
	return func(e providers.MediaEvent) bool { return e.Kind == providers.MediaMark && e.Mark == name }
}

func isStop(e providers.MediaEvent) bool { return e.Kind == providers.MediaStop }

func silence(seq uint64) providers.AudioChunk {
	f := providers.TelephonyFormat
	data := make([]byte, f.BytesPerFrame())
	for i := range data {
		data[i] = 0xFF // mu-law silence
	}
	return providers.AudioChunk{Format: f, Seq: seq, Data: data}
}

// call starts the call, attaches media and returns the provider id, stream and events.
func call(t *testing.T, c context.Context, a Adapters) (string, providers.MediaStream, *recorder[providers.MediaEvent]) {
	t.Helper()
	id, err := a.Telephony.Start(c, a.Dial)
	if err != nil || strings.TrimSpace(id) == "" {
		t.Fatalf("Start: id %q, err %v", id, err)
	}
	events := newRecorder[providers.MediaEvent]()
	stream, err := a.Telephony.Media(c, id, func(e providers.MediaEvent) error { events.add(e); return nil })
	if err != nil {
		t.Fatalf("Media: %v", err)
	}
	return id, stream, events
}

// transcribeFirstTurn pumps inbound audio into a transcriber until its first final turn.
func transcribeFirstTurn(t *testing.T, c context.Context, a Adapters, events *recorder[providers.MediaEvent]) (providers.TranscriptSession, *recorder[providers.TranscriptUpdate]) {
	t.Helper()
	updates := newRecorder[providers.TranscriptUpdate]()
	session, err := a.Transcriber.Open(c, providers.TranscriptOpen{CallID: a.Dial.CallID, Format: providers.TelephonyFormat},
		func(u providers.TranscriptUpdate) error { updates.add(u); return nil })
	if err != nil {
		t.Fatalf("Transcriber.Open: %v", err)
	}
	next := 0
	for {
		e, i := events.waitFor(t, "inbound audio", next, func(e providers.MediaEvent) bool { return e.Kind == providers.MediaAudio })
		next = i + 1
		if err := e.Audio.Validate(); err != nil || e.Audio.Format != providers.TelephonyFormat {
			t.Fatalf("inbound audio has invalid metadata: %+v (%v)", e.Audio.Format, err)
		}
		if err := session.Push(c, e.Audio); err != nil {
			t.Fatalf("Push: %v", err)
		}
		for _, u := range updates.snapshot() {
			if u.IsFinal {
				return session, updates
			}
		}
	}
}

// ---------------------------------------------------------------------------

func lifecycle(t *testing.T, a Adapters) {
	c := ctx(t)
	id, stream, events := call(t, c, a)
	session, updates := transcribeFirstTurn(t, c, a, events)

	// Partials share the final's turn id, and the final carries an end time.
	all := updates.snapshot()
	final := all[len(all)-1]
	if strings.TrimSpace(final.Text) == "" || final.TurnID == "" || final.EndedAtMS < final.StartedAtMS {
		t.Fatalf("invalid final update %+v", final)
	}
	for _, u := range all[:len(all)-1] {
		if u.IsFinal || u.TurnID != final.TurnID || u.StartedAtMS != final.StartedAtMS {
			t.Fatalf("partial %+v does not belong to final turn %s", u, final.TurnID)
		}
	}

	// Generate a reply from the finalized turn.
	drafts := newRecorder[providers.ResponseDraft]()
	req := providers.ResponseRequest{CallID: a.Dial.CallID, Goal: a.Goal, ContextRevision: 0,
		Transcript: []providers.TranscriptTurn{{Speaker: providers.SpeakerBusiness, Text: final.Text}}}
	if err := a.Responder.Generate(c, req, func(d providers.ResponseDraft) error { drafts.add(d); return nil }); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	ds := drafts.snapshot()
	if len(ds) == 0 || !ds[len(ds)-1].Final {
		t.Fatalf("Generate returned without a final draft (%d drafts)", len(ds))
	}
	for i, d := range ds {
		if d.ContextRevision != req.ContextRevision || (d.Final && i != len(ds)-1) ||
			(i > 0 && !strings.HasPrefix(d.Text, ds[i-1].Text)) {
			t.Fatalf("draft %d is not cumulative, fenced and final-last: %+v", i, d)
		}
	}
	reply := ds[len(ds)-1].Text
	if strings.TrimSpace(reply) == "" || len([]rune(reply)) > providers.MaxResponseText {
		t.Fatalf("final reply length %d is out of bounds", len(reply))
	}
	drafts.checkSerial(t, "Generate")

	// Synthesize the approved reply and play it into the call.
	token := providers.Token{ContextRevision: 0, Generation: 1}
	chunks := newRecorder[providers.SpeechChunk]()
	speech := providers.SpeechRequest{Text: reply, Voice: a.Voice, Format: providers.TelephonyFormat, Token: token}
	if err := a.Synthesizer.Stream(c, speech, func(s providers.SpeechChunk) error { chunks.add(s); return nil }); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	cs := chunks.snapshot()
	if len(cs) == 0 || !cs[len(cs)-1].Last {
		t.Fatalf("Stream returned without a last chunk (%d chunks)", len(cs))
	}
	for i, s := range cs {
		if s.Token != token || s.Audio.Format != providers.TelephonyFormat || s.Audio.Seq != uint64(i+1) ||
			s.Audio.Validate() != nil || (s.Last && i != len(cs)-1) {
			t.Fatalf("speech chunk %d has wrong token, format, sequence or last flag", i)
		}
		if err := stream.Send(c, s.Audio); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	chunks.checkSerial(t, "Stream")
	if err := stream.Mark(c, "reply-1"); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	events.waitFor(t, "playback mark reply-1", 0, isMark("reply-1"))

	// End is terminal: stop is the last media event and the call rejects more audio.
	if err := a.Telephony.End(c, id); err != nil {
		t.Fatalf("End: %v", err)
	}
	if err := a.Telephony.End(c, id); err != nil {
		t.Fatalf("second End should be a no-op, got %v", err)
	}
	_, stopAt := events.waitFor(t, "stop event", 0, isStop)
	events.quiet(t, "media after stop")
	if n := events.len(); n != stopAt+1 {
		t.Fatalf("%d media events after stop", n-stopAt-1)
	}
	events.checkSerial(t, "Media")
	if err := stream.Send(c, silence(1)); !errors.Is(err, providers.ErrClosed) {
		t.Fatalf("Send after End = %v, want ErrClosed", err)
	}
	if _, err := a.Telephony.Media(c, id, func(providers.MediaEvent) error { return nil }); !errors.Is(err, providers.ErrClosed) {
		t.Fatalf("Media after End = %v, want ErrClosed", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("transcriber Close: %v", err)
	}
	if err := session.Push(c, silence(1)); !errors.Is(err, providers.ErrClosed) {
		t.Fatalf("Push after Close = %v, want ErrClosed", err)
	}
	updates.checkSerial(t, "Transcriber")
}

func firstRequest(t *testing.T, c context.Context, a Adapters) providers.ResponseRequest {
	t.Helper()
	_, _, events := call(t, c, a)
	_, updates := transcribeFirstTurn(t, c, a, events)
	all := updates.snapshot()
	return providers.ResponseRequest{CallID: a.Dial.CallID, Goal: a.Goal,
		Transcript: []providers.TranscriptTurn{{Speaker: providers.SpeakerBusiness, Text: all[len(all)-1].Text}}}
}

func responderCancel(t *testing.T, a Adapters) {
	c := ctx(t)
	req := firstRequest(t, c, a)
	gen, cancel := context.WithCancel(c)
	defer cancel()
	drafts := newRecorder[providers.ResponseDraft]()
	err := a.Responder.Generate(gen, req, func(d providers.ResponseDraft) error {
		drafts.add(d)
		if drafts.len() == 1 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate after cancel = %v, want context.Canceled", err)
	}
	drafts.quiet(t, "drafts after cancel")
	for _, d := range drafts.snapshot() {
		if d.Final {
			t.Fatal("a cancelled generation produced a final draft")
		}
	}
	if n := drafts.len(); n != 1 {
		t.Fatalf("%d drafts delivered after cancel", n-1)
	}
	// An already-cancelled context never calls back.
	if err := a.Responder.Generate(gen, req, func(providers.ResponseDraft) error {
		t.Fatal("callback after cancelled context")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate with cancelled context = %v", err)
	}
}

func synthesizerCancel(t *testing.T, a Adapters) {
	c := ctx(t)
	speak, cancel := context.WithCancel(c)
	defer cancel()
	chunks := newRecorder[providers.SpeechChunk]()
	req := providers.SpeechRequest{Text: "Could you hold on one moment while I check which time works for everyone?",
		Voice: a.Voice, Format: providers.TelephonyFormat, Token: providers.Token{ContextRevision: 3, Generation: 7}}
	err := a.Synthesizer.Stream(speak, req, func(s providers.SpeechChunk) error {
		chunks.add(s)
		if chunks.len() == 1 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream after cancel = %v, want context.Canceled", err)
	}
	chunks.quiet(t, "audio after cancel")
	if cs := chunks.snapshot(); len(cs) != 1 || cs[0].Last {
		t.Fatalf("cancel did not stop audio emission: %d chunks, last=%v", len(cs), len(cs) > 0 && cs[len(cs)-1].Last)
	}
	if err := a.Synthesizer.Stream(speak, req, func(providers.SpeechChunk) error {
		t.Fatal("audio after cancelled context")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream with cancelled context = %v", err)
	}
}

func transcriberStop(t *testing.T, factory Factory) {
	for _, mode := range []string{"close", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			a := factory(t)
			c := ctx(t)
			_, _, events := call(t, c, a)
			listen, cancel := context.WithCancel(c)
			defer cancel()
			updates := newRecorder[providers.TranscriptUpdate]()
			session, err := a.Transcriber.Open(listen, providers.TranscriptOpen{CallID: a.Dial.CallID, Format: providers.TelephonyFormat},
				func(u providers.TranscriptUpdate) error { updates.add(u); return nil })
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			next, stopped := 0, false
			for updates.len() == 0 || !stopped {
				e, i := events.waitFor(t, "inbound audio", next, func(e providers.MediaEvent) bool { return e.Kind == providers.MediaAudio })
				next = i + 1
				err := session.Push(listen, e.Audio)
				if stopped {
					if !errors.Is(err, providers.ErrClosed) && !errors.Is(err, context.Canceled) {
						t.Fatalf("Push after %s = %v", mode, err)
					}
					break
				}
				if err != nil {
					t.Fatalf("Push: %v", err)
				}
				if updates.len() > 0 {
					if mode == "close" {
						if err := session.Close(); err != nil {
							t.Fatalf("Close: %v", err)
						}
					} else {
						cancel()
					}
					stopped = true
				}
			}
			updates.quiet(t, "transcript after "+mode)
			for _, u := range updates.snapshot() {
				if u.IsFinal {
					t.Fatalf("a %s session finalized pending text", mode)
				}
			}
		})
	}
}

func clearDropsAudio(t *testing.T, a Adapters) {
	c := ctx(t)
	_, stream, events := call(t, c, a)
	// Fifty seconds of audio cannot finish playing before Clear runs.
	for i := 1; i <= 2500; i++ {
		if err := stream.Send(c, silence(uint64(i))); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}
	if err := stream.Mark(c, "obsolete"); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	dropped, err := stream.Clear(c)
	if err != nil || dropped == 0 {
		t.Fatalf("Clear dropped %d bytes, err %v", dropped, err)
	}
	// Playback continues with new audio; the cleared mark is never reported.
	if err := stream.Send(c, silence(2501)); err != nil {
		t.Fatalf("Send after Clear: %v", err)
	}
	if err := stream.Mark(c, "fresh"); err != nil {
		t.Fatalf("Mark after Clear: %v", err)
	}
	events.waitFor(t, "mark after clear", 0, isMark("fresh"))
	for _, e := range events.snapshot() {
		if isMark("obsolete")(e) {
			t.Fatal("a cleared mark was reported as played")
		}
	}
	if err := stream.Mark(c, ""); !errors.Is(err, providers.ErrInvalid) {
		t.Fatalf("empty mark = %v, want ErrInvalid", err)
	}
}

func mediaStop(t *testing.T, factory Factory) {
	for _, mode := range []string{"close", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			a := factory(t)
			c := ctx(t)
			id, err := a.Telephony.Start(c, a.Dial)
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			listen, cancel := context.WithCancel(c)
			defer cancel()
			events := newRecorder[providers.MediaEvent]()
			stream, err := a.Telephony.Media(listen, id, func(e providers.MediaEvent) error { events.add(e); return nil })
			if err != nil {
				t.Fatalf("Media: %v", err)
			}
			events.waitFor(t, "inbound audio", 0, func(e providers.MediaEvent) bool { return e.Kind == providers.MediaAudio })
			if mode == "close" {
				if err := stream.Close(); err != nil {
					t.Fatalf("Close: %v", err)
				}
			} else {
				cancel()
			}
			events.quiet(t, "media after "+mode)
			if err := stream.Send(c, silence(1)); !errors.Is(err, providers.ErrClosed) {
				t.Fatalf("Send after %s = %v, want ErrClosed", mode, err)
			}
			// Detaching does not end the call; a new stream can attach and the call can end.
			if err := a.Telephony.End(c, id); err != nil {
				t.Fatalf("End after %s: %v", mode, err)
			}
		})
	}
}

func callbackErrors(t *testing.T, a Adapters) {
	c := ctx(t)
	stop := errors.New("consumer stopped")
	req := firstRequest(t, c, a)
	calls := 0
	if err := a.Responder.Generate(c, req, func(providers.ResponseDraft) error { calls++; return stop }); !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("Generate callback error: err %v after %d calls", err, calls)
	}
	calls = 0
	speech := providers.SpeechRequest{Text: "One moment please, I am checking.", Voice: a.Voice, Format: providers.TelephonyFormat, Token: providers.Token{Generation: 1}}
	if err := a.Synthesizer.Stream(c, speech, func(providers.SpeechChunk) error { calls++; return stop }); !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("Stream callback error: err %v after %d calls", err, calls)
	}

	// A failing media consumer detaches its stream; the call itself stays up.
	id, err := a.Telephony.Start(c, a.Dial)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	var mediaCalls atomic.Int32
	stream, err := a.Telephony.Media(c, id, func(providers.MediaEvent) error { mediaCalls.Add(1); return stop })
	if err != nil {
		t.Fatalf("Media: %v", err)
	}
	deadline := time.Now().Add(Wait)
	for mediaCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(Quiet)
	if n := mediaCalls.Load(); n != 1 {
		t.Fatalf("media callback ran %d times after returning an error", n)
	}
	if err := stream.Send(c, silence(1)); !errors.Is(err, providers.ErrClosed) {
		t.Fatalf("Send after callback error = %v, want ErrClosed", err)
	}
	if err := a.Telephony.End(c, id); err != nil {
		t.Fatalf("End: %v", err)
	}
}

func invalidRequests(t *testing.T, a Adapters) {
	c := ctx(t)
	never := func(string) func() { return func() { t.Fatal("callback for an invalid request") } }

	badDials := map[string]func(*providers.Dial){
		"call id":     func(d *providers.Dial) { d.CallID = "call-1" },
		"destination": func(d *providers.Dial) { d.Destination = "225-555-0142" },
		"voice url":   func(d *providers.Dial) { d.Callbacks.Voice = "http://example.test/voice" },
		"media url":   func(d *providers.Dial) { d.Callbacks.Media = "https://example.test/media" },
	}
	for name, mutate := range badDials {
		d := a.Dial
		mutate(&d)
		if _, err := a.Telephony.Start(c, d); !errors.Is(err, providers.ErrInvalid) {
			t.Errorf("Start with bad %s = %v, want ErrInvalid", name, err)
		}
	}
	if err := a.Telephony.End(c, "unknown"); !errors.Is(err, providers.ErrNotFound) {
		t.Errorf("End unknown = %v, want ErrNotFound", err)
	}
	if _, err := a.Telephony.Media(c, "unknown", func(providers.MediaEvent) error { never("media")(); return nil }); !errors.Is(err, providers.ErrNotFound) {
		t.Errorf("Media unknown = %v, want ErrNotFound", err)
	}
	if _, err := a.Telephony.Media(c, "unknown", nil); err == nil {
		t.Error("Media with nil callback succeeded")
	}

	mp3 := providers.AudioFormat{Encoding: "audio/mpeg", SampleRate: 44100, Channels: 2}
	if _, err := a.Transcriber.Open(c, providers.TranscriptOpen{CallID: a.Dial.CallID, Format: mp3},
		func(providers.TranscriptUpdate) error { never("transcript")(); return nil }); !errors.Is(err, providers.ErrUnsupportedFormat) {
		t.Errorf("Open with mp3 = %v, want ErrUnsupportedFormat", err)
	}
	if _, err := a.Transcriber.Open(c, providers.TranscriptOpen{CallID: "x", Format: providers.TelephonyFormat},
		func(providers.TranscriptUpdate) error { return nil }); !errors.Is(err, providers.ErrInvalid) {
		t.Errorf("Open with bad call id = %v, want ErrInvalid", err)
	}
	session, err := a.Transcriber.Open(c, providers.TranscriptOpen{CallID: a.Dial.CallID, Format: providers.TelephonyFormat},
		func(providers.TranscriptUpdate) error { return nil })
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	wrong := silence(1)
	wrong.Format.SampleRate = 16000
	if err := session.Push(c, wrong); !errors.Is(err, providers.ErrUnsupportedFormat) {
		t.Errorf("Push with a different format = %v, want ErrUnsupportedFormat", err)
	}
	if err := session.Push(c, providers.AudioChunk{Format: providers.TelephonyFormat, Seq: 1}); !errors.Is(err, providers.ErrInvalid) {
		t.Errorf("Push empty chunk = %v, want ErrInvalid", err)
	}
	_ = session.Close()

	turn := []providers.TranscriptTurn{{Speaker: providers.SpeakerBusiness, Text: "Hello?"}}
	badResponses := map[string]providers.ResponseRequest{
		"empty transcript":  {CallID: a.Dial.CallID, Goal: a.Goal},
		"bad goal":          {CallID: a.Dial.CallID, Goal: json.RawMessage("{"), Transcript: turn},
		"bad speaker":       {CallID: a.Dial.CallID, Goal: a.Goal, Transcript: []providers.TranscriptTurn{{Speaker: "operator", Text: "hi"}}},
		"blank instruction": {CallID: a.Dial.CallID, Goal: a.Goal, Transcript: turn, Instructions: []string{" "}},
		"long instruction": {CallID: a.Dial.CallID, Goal: a.Goal, Transcript: turn,
			Instructions: []string{strings.Repeat("x", providers.MaxInstructionText+1)}},
	}
	for name, req := range badResponses {
		if err := a.Responder.Generate(c, req, func(providers.ResponseDraft) error { never("draft")(); return nil }); !errors.Is(err, providers.ErrInvalid) {
			t.Errorf("Generate with %s = %v, want ErrInvalid", name, err)
		}
	}

	ok := providers.SpeechRequest{Text: "Thank you.", Voice: a.Voice, Format: providers.TelephonyFormat}
	badSpeech := map[string]func(*providers.SpeechRequest) error{
		"empty text": func(r *providers.SpeechRequest) error { r.Text = " "; return providers.ErrInvalid },
		"long text": func(r *providers.SpeechRequest) error {
			r.Text = strings.Repeat("x", providers.MaxSpeechText+1)
			return providers.ErrInvalid
		},
		"no voice": func(r *providers.SpeechRequest) error { r.Voice = ""; return providers.ErrInvalid },
		"mp3":      func(r *providers.SpeechRequest) error { r.Format = mp3; return providers.ErrUnsupportedFormat },
	}
	for name, mutate := range badSpeech {
		r := ok
		want := mutate(&r)
		if err := a.Synthesizer.Stream(c, r, func(providers.SpeechChunk) error { never("speech")(); return nil }); !errors.Is(err, want) {
			t.Errorf("Stream with %s = %v, want %v", name, err, want)
		}
	}
}
