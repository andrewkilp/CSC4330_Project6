package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/telephony"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/telephony/media"
)

const (
	mediaCallID    = "00000000-0000-4000-8000-000000000001"
	mediaCallSID   = "CA22222222222222222222222222222222"
	mediaStreamSID = "MZ0123456789abcdef0123456789abcdef"
	mediaSecret    = "fixture-media-stream-secret-of-at-least-32-bytes"
)

// mediaStore is a fake of the repository surfaces the callback and media routes use.
type mediaStore struct{ status string }

func (mediaStore) Create(context.Context, string, string, repository.Draft) (repository.Call, error) {
	return repository.Call{}, repository.ErrUnavailable
}
func (mediaStore) GetCall(context.Context, string, string) (repository.Call, error) {
	return repository.Call{}, repository.ErrNotFound
}
func (s mediaStore) ApplyTwilioCallback(_ context.Context, cb repository.TwilioCallback) (repository.CallbackResult, error) {
	return repository.CallbackResult{Call: repository.Call{ID: cb.CallID, OwnerID: "00000000-0000-4000-8000-000000000300", Mode: "real", Status: s.status}, Events: []repository.Event{}}, nil
}
func (s mediaStore) ActiveProviderCallID(_ context.Context, callID string) (string, error) {
	if callID != mediaCallID {
		return "", repository.ErrNotFound
	}
	return mediaCallSID, nil
}

func mediaEnvironmentOn(t *testing.T) {
	t.Helper()
	callbackOnlyEnvironment(t)
	t.Setenv("MEDIA_STREAM_SECRET", mediaSecret)
	t.Setenv("DEEPGRAM_API_KEY", "fixture-stt-key")
}

func voiceTwiML(t *testing.T, h http.Handler) (connect bool, params map[string]string) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, signedAPICallbackRequest(t, "voice"))
	if w.Code != 200 {
		t.Fatalf("voice callback = %d", w.Code)
	}
	var out struct {
		Connect *struct {
			Stream struct {
				Parameters []struct {
					Name  string `xml:"name,attr"`
					Value string `xml:"value,attr"`
				} `xml:"Parameter"`
			} `xml:"Stream"`
		} `xml:"Connect"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Connect == nil {
		return false, nil
	}
	params = map[string]string{}
	for _, p := range out.Connect.Stream.Parameters {
		params[p.Name] = p.Value
	}
	return true, params
}

func TestMediaStaysUnmountedWithoutStreamSecret(t *testing.T) {
	callbackOnlyEnvironment(t)
	t.Setenv("MEDIA_STREAM_SECRET", "")
	h, err := configuredRouter("development", mediaStore{status: "connected"}, "")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/webhooks/twilio/media", nil))
	if w.Code != 404 {
		t.Fatalf("media route mounted without a secret: %d", w.Code)
	}
	if connect, _ := voiceTwiML(t, h); connect {
		t.Fatal("voice TwiML pointed Twilio at an unmounted stream")
	}
}

func TestMediaConfigurationFailsClosed(t *testing.T) {
	cases := map[string]struct {
		env   map[string]string
		store any
	}{
		"short secret":       {map[string]string{"MEDIA_STREAM_SECRET": "too-short"}, mediaStore{}},
		"callbacks disabled": {map[string]string{"TWILIO_WEBHOOKS_ENABLED": "false"}, mediaStore{}},
		"no storage":         {nil, nil},
		"no transcriber key": {map[string]string{"DEEPGRAM_API_KEY": ""}, mediaStore{}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mediaEnvironmentOn(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			var err error
			if tc.store == nil {
				_, err = configuredRouter("development", nil, "")
			} else {
				_, err = configuredRouter("development", tc.store.(mediaStore), "")
			}
			if err == nil {
				t.Fatal("partial media configuration started")
			}
			if strings.Contains(err.Error(), mediaSecret) || strings.Contains(err.Error(), "fixture-stt-key") {
				t.Fatal("startup error leaked a secret")
			}
		})
	}
}

func TestConfiguredMediaMountsBridgeAndSignsVoiceTwiML(t *testing.T) {
	mediaEnvironmentOn(t)
	h, err := configuredRouter("development", mediaStore{status: "connected"}, "")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/webhooks/twilio/media", nil))
	if w.Code == 404 {
		t.Fatal("media route not mounted")
	}
	connect, params := voiceTwiML(t, h)
	if !connect || params["call_id"] != mediaCallID || params["token"] != media.SignStream([]byte(mediaSecret), mediaCallID, mediaCallSID) {
		t.Fatalf("voice TwiML stream parameters = %v", params)
	}
	// Terminal calls still hang up even with media configured.
	ended, err := configuredRouter("development", mediaStore{status: "ended"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if connect, _ := voiceTwiML(t, ended); connect {
		t.Fatal("ended call was connected to media")
	}
}

// ---------------------------------------------------------------------------
// The mounted bridge feeds the live-call registry and sink.

type finalTranscriber struct{}

func (finalTranscriber) Open(_ context.Context, _ providers.TranscriptOpen, emit func(providers.TranscriptUpdate) error) (providers.TranscriptSession, error) {
	return finalSession{emit}, nil
}

type finalSession struct {
	emit func(providers.TranscriptUpdate) error
}

func (s finalSession) Push(context.Context, providers.AudioChunk) error {
	return s.emit(providers.TranscriptUpdate{TurnID: "turn-1", Text: "Bayou Lantern, how can I help?", IsFinal: true})
}
func (finalSession) Close() error { return nil }

type recordingSink struct {
	mu            sync.Mutex
	transcripts   []string
	events        []providers.MediaEventKind
	interruptions []telephony.Interruption
	changed       chan struct{}
}

func (s *recordingSink) Transcript(callID string, u providers.TranscriptUpdate) error {
	s.mu.Lock()
	s.transcripts = append(s.transcripts, callID+":"+u.Text)
	s.mu.Unlock()
	s.changed <- struct{}{}
	return nil
}
func (s *recordingSink) Interrupted(callID string, i telephony.Interruption) {
	s.mu.Lock()
	s.interruptions = append(s.interruptions, i)
	s.mu.Unlock()
}
func (s *recordingSink) Event(callID string, e providers.MediaEvent) {
	s.mu.Lock()
	s.events = append(s.events, e.Kind)
	s.mu.Unlock()
	s.changed <- struct{}{}
}

func twilioStart(token string) map[string]any {
	return map[string]any{"event": "start", "sequenceNumber": "1", "streamSid": mediaStreamSID, "start": map[string]any{
		"streamSid": mediaStreamSID, "callSid": mediaCallSID, "accountSid": "AC11111111111111111111111111111111", "tracks": []string{"inbound"},
		"customParameters": map[string]string{"call_id": mediaCallID, "token": token},
		"mediaFormat":      map[string]any{"encoding": "audio/x-mulaw", "sampleRate": 8000, "channels": 1}}}
}

func mediaClient(t *testing.T, live *liveMedia) *websocket.Conn {
	t.Helper()
	mux := http.NewServeMux()
	registerMedia(mux, live, []byte(mediaSecret), mediaStore{}, finalTranscriber{})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http")+"/webhooks/twilio/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func write(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	data, _ := json.Marshal(v)
	if err := c.Write(context.Background(), websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestMountedBridgeTracksLiveStreamAndForwardsToSink(t *testing.T) {
	live := newLiveMedia(telephony.NewCalls())
	sink := &recordingSink{changed: make(chan struct{}, 16)}
	live.SetSink(sink)
	c := mediaClient(t, live)
	write(t, c, map[string]any{"event": "connected", "protocol": "Call", "version": "1.0.0"})
	write(t, c, twilioStart(media.SignStream([]byte(mediaSecret), mediaCallID, mediaCallSID)))
	waitUntil(t, "attached stream", func() bool { _, ok := live.Stream(mediaCallID); return ok })

	write(t, c, map[string]any{"event": "media", "sequenceNumber": "2", "streamSid": mediaStreamSID,
		"media": map[string]any{"track": "inbound", "payload": base64.StdEncoding.EncodeToString(make([]byte, 160))}})
	waitUntil(t, "forwarded transcript", func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		return len(sink.transcripts) == 1
	})
	if sink.transcripts[0] != mediaCallID+":Bayou Lantern, how can I help?" {
		t.Fatalf("transcript = %v", sink.transcripts)
	}

	write(t, c, map[string]any{"event": "stop", "sequenceNumber": "3", "streamSid": mediaStreamSID})
	waitUntil(t, "stop event", func() bool {
		sink.mu.Lock()
		defer sink.mu.Unlock()
		return len(sink.events) > 0 && sink.events[len(sink.events)-1] == providers.MediaStop
	})
	if _, ok := live.Stream(mediaCallID); ok {
		t.Fatal("stopped stream still registered for controls")
	}
}

func TestMountedBridgeRejectsForgedStreamsBeforeRegistering(t *testing.T) {
	for name, token := range map[string]string{
		"forged":     "forged-token",
		"other call": media.SignStream([]byte(mediaSecret), mediaCallID, "CA33333333333333333333333333333333"),
		"other key":  media.SignStream([]byte(strings.Repeat("x", 40)), mediaCallID, mediaCallSID),
	} {
		t.Run(name, func(t *testing.T) {
			live := newLiveMedia(telephony.NewCalls())
			c := mediaClient(t, live)
			write(t, c, twilioStart(token))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _, err := c.Read(ctx)
			if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
				t.Fatalf("close = %v, want policy violation", err)
			}
			if _, ok := live.Stream(mediaCallID); ok {
				t.Fatal("forged stream was registered")
			}
		})
	}
}

func TestLiveMediaReplacesAndClosesPreviousStream(t *testing.T) {
	live := newLiveMedia(telephony.NewCalls())
	first, second := &closeCounter{}, &closeCounter{}
	live.attach(mediaCallID, first)
	live.attach(mediaCallID, second)
	if got, _ := live.Stream(mediaCallID); got != second || first.closed.Load() != 1 || second.closed.Load() != 0 {
		t.Fatal("a reconnecting stream did not replace and close the previous one")
	}
	if err := live.transcript(mediaCallID, providers.TranscriptUpdate{Text: "dropped"}); err != nil {
		t.Fatal("no sink must discard, not fail")
	}
	live.event(mediaCallID, providers.MediaEvent{Kind: providers.MediaStop})
	if _, ok := live.Stream(mediaCallID); ok {
		t.Fatal("stop did not unregister")
	}
}

type closeCounter struct{ closed atomic.Int32 }

func (*closeCounter) Send(context.Context, providers.AudioChunk) error { return errors.New("unused") }
func (*closeCounter) Mark(context.Context, string) error               { return errors.New("unused") }
func (*closeCounter) Clear(context.Context) (int, error)               { return 0, nil }
func (c *closeCounter) Close() error                                   { c.closed.Add(1); return nil }

// Remote speech on a live call clears the assistant's queued audio before the sink
// sees the transcript, and the sink learns what was heard.
func TestTranscriptsInterruptPlayingSpeech(t *testing.T) {
	calls := telephony.NewCalls()
	live := newLiveMedia(calls)
	sink := &recordingSink{changed: make(chan struct{}, 16)}
	live.SetSink(sink)
	stream := &clearCounter{}
	if err := calls.AttachMedia(mediaCallID, stream); err != nil {
		t.Fatal(err)
	}
	p, _ := media.NewPlayback("00000000-0000-4000-8000-0000000000aa")
	first, _ := p.Queue(1600)
	_, _ = p.Queue(1600)
	if err := calls.BeginSpeech(mediaCallID, p); err != nil {
		t.Fatal(err)
	}
	if _, _, err := calls.Acknowledge(mediaCallID, first); err != nil {
		t.Fatal(err)
	}
	if err := live.transcript(mediaCallID, providers.TranscriptUpdate{TurnID: "b", Text: "Wait"}); err != nil {
		t.Fatal(err)
	}
	if stream.clears.Load() != 1 || len(sink.interruptions) != 1 || sink.interruptions[0].PlayedBytes != 1600 {
		t.Fatalf("clears=%d interruptions=%+v", stream.clears.Load(), sink.interruptions)
	}
	if len(sink.transcripts) != 1 {
		t.Fatal("the interrupting transcript was not forwarded")
	}
	// Further speech with nothing playing does not clear again.
	_ = live.transcript(mediaCallID, providers.TranscriptUpdate{TurnID: "b", Text: "Wait, one"})
	if stream.clears.Load() != 1 || len(sink.interruptions) != 1 {
		t.Fatal("interrupted twice")
	}
}

type clearCounter struct{ clears atomic.Int32 }

func (*clearCounter) Send(context.Context, providers.AudioChunk) error { return nil }
func (*clearCounter) Mark(context.Context, string) error               { return nil }
func (c *clearCounter) Clear(context.Context) (int, error)             { c.clears.Add(1); return 1600, nil }
func (*clearCounter) Close() error                                     { return nil }

type failureLog struct {
	mu    sync.Mutex
	codes []string
}

func (f *failureLog) RecordProviderFailure(_ context.Context, callID string, pf repository.ProviderFailure) ([]repository.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !pf.FailCall {
		return nil, errors.New("a lost stream must fail the call")
	}
	f.codes = append(f.codes, callID+":"+pf.Code)
	return []repository.Event{{CallID: callID, Type: "provider.error"}, {CallID: callID, Type: "call.updated"}}, nil
}

func (f *failureLog) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.codes)
}

// A Twilio socket that drops without a stop must not leave a media session or a
// silent call behind (#36).
func TestDroppedMediaSocketEndsTheCallVisibly(t *testing.T) {
	calls := telephony.NewCalls()
	live := newLiveMedia(calls)
	failures := &failureLog{}
	var published atomic.Int32
	live.failures, live.publish = failures, func(e ...repository.Event) { published.Add(int32(len(e))) }
	provider := &hangupCounter{}
	if err := calls.Started(mediaCallID, provider, mediaCallSID); err != nil {
		t.Fatal(err)
	}
	c := mediaClient(t, live)
	write(t, c, twilioStart(media.SignStream([]byte(mediaSecret), mediaCallID, mediaCallSID)))
	waitUntil(t, "attached stream", func() bool { _, ok := live.Stream(mediaCallID); return ok })
	c.CloseNow() // abrupt: no stop message
	waitUntil(t, "failure recorded", func() bool { return failures.count() == 1 })
	if failures.codes[0] != mediaCallID+":media_disconnected" || published.Load() != 2 {
		t.Fatalf("failures=%v published=%d", failures.codes, published.Load())
	}
	if _, ok := live.Stream(mediaCallID); ok || !calls.Ended(mediaCallID) || provider.ends.Load() != 1 {
		t.Fatalf("leaked: stream registered=%v ended=%v hangups=%d", ok, calls.Ended(mediaCallID), provider.ends.Load())
	}
}

func TestCleanStopAndShutdownRecordNoFailure(t *testing.T) {
	calls := telephony.NewCalls()
	live := newLiveMedia(calls)
	failures := &failureLog{}
	live.failures = failures
	live.closed(mediaCallID, nil)
	live.closed(mediaCallID, context.Canceled)
	if failures.count() != 0 || calls.Ended(mediaCallID) {
		t.Fatal("a clean stop or shutdown was treated as a lost stream")
	}
}

type hangupCounter struct{ ends atomic.Int32 }

func (*hangupCounter) Start(context.Context, providers.Dial) (string, error) { return "", nil }
func (h *hangupCounter) End(context.Context, string) error                   { h.ends.Add(1); return nil }
func (*hangupCounter) Media(context.Context, string, func(providers.MediaEvent) error) (providers.MediaStream, error) {
	return nil, providers.ErrUnavailable
}
