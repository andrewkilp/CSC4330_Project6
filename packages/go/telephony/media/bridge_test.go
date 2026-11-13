package media

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
)

const (
	callID  = "0b6c4f7e-3f1a-4c2b-9d3e-1a2b3c4d5e6f"
	stream  = "MZ" + "0123456789abcdef" + "0123456789abcdef"
	sid     = "CA" + "0123456789abcdef" + "0123456789abcdef"
	account = "AC" + "0123456789abcdef" + "0123456789abcdef"
)

var secret = []byte("0123456789abcdef0123456789abcdef-test-only")

// pipe is an in-memory WebSocket: the test plays Twilio.
type pipe struct {
	in  chan []byte // Twilio -> bridge
	out chan []byte // bridge -> Twilio
	// block makes bridge writes wait, to fill the outbound queue.
	block chan struct{}
}

func newPipe() *pipe { return &pipe{in: make(chan []byte, 1024), out: make(chan []byte, 1024)} }

func (p *pipe) Read(ctx context.Context) ([]byte, error) {
	select {
	case m, ok := <-p.in:
		if !ok {
			return nil, errors.New("closed")
		}
		return m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *pipe) Write(ctx context.Context, msg []byte) error {
	if p.block != nil {
		select {
		case <-p.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case p.out <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *pipe) send(t *testing.T, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	p.in <- data
}

func (p *pipe) next(t *testing.T) map[string]any {
	t.Helper()
	select {
	case data := <-p.out:
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no outbound message")
		return nil
	}
}

func startMsg(seq int, params map[string]string, mutate func(map[string]any)) map[string]any {
	start := map[string]any{"streamSid": stream, "callSid": sid, "accountSid": account, "tracks": []string{"inbound"},
		"customParameters": params, "mediaFormat": map[string]any{"encoding": "audio/x-mulaw", "sampleRate": 8000, "channels": 1}}
	m := map[string]any{"event": "start", "sequenceNumber": fmt.Sprint(seq), "streamSid": stream, "start": start}
	if mutate != nil {
		mutate(start)
	}
	return m
}

func validParams() map[string]string {
	return map[string]string{"call_id": callID, "token": SignStream(secret, callID, sid)}
}

func mediaMsg(seq int, audio []byte) map[string]any {
	return map[string]any{"event": "media", "sequenceNumber": fmt.Sprint(seq), "streamSid": stream,
		"media": map[string]any{"track": "inbound", "chunk": fmt.Sprint(seq), "timestamp": "0", "payload": base64.StdEncoding.EncodeToString(audio)}}
}

type fakeTranscriber struct {
	mu     sync.Mutex
	opened int
	closed bool
	pushed [][]byte
	gate   chan struct{} // when set, Push waits on it
}

func (f *fakeTranscriber) Open(ctx context.Context, open providers.TranscriptOpen, emit func(providers.TranscriptUpdate) error) (providers.TranscriptSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if open.CallID != callID || open.Format != providers.TelephonyFormat {
		return nil, providers.ErrInvalid
	}
	f.opened++
	return &fakeSession{f: f, emit: emit}, nil
}

type fakeSession struct {
	f    *fakeTranscriber
	emit func(providers.TranscriptUpdate) error
}

func (s *fakeSession) Push(ctx context.Context, c providers.AudioChunk) error {
	if s.f.gate != nil {
		select {
		case <-s.f.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.f.mu.Lock()
	s.f.pushed = append(s.f.pushed, append([]byte(nil), c.Data...))
	n := len(s.f.pushed)
	s.f.mu.Unlock()
	return s.emit(providers.TranscriptUpdate{TurnID: "t1", Text: fmt.Sprintf("frame %d", n), IsFinal: n == 2})
}

func (s *fakeSession) Close() error {
	s.f.mu.Lock()
	s.f.closed = true
	s.f.mu.Unlock()
	return nil
}

func (f *fakeTranscriber) snapshot() (int, bool, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opened, f.closed, len(f.pushed)
}

type recorder struct {
	mu      sync.Mutex
	started chan providers.MediaStream
	updates []providers.TranscriptUpdate
	events  []providers.MediaEvent
}

func newRecorder() *recorder { return &recorder{started: make(chan providers.MediaStream, 1)} }

func (r *recorder) config(tr providers.Transcriber, lookup func(context.Context, string) (string, error)) Config {
	if lookup == nil {
		lookup = func(context.Context, string) (string, error) { return sid, nil }
	}
	return Config{
		Authorizer:  TokenAuthorizer{Secret: secret, ProviderCallID: lookup},
		Transcriber: tr,
		OnStart: func(id string, out providers.MediaStream) {
			if id == callID {
				r.started <- out
			}
		},
		OnTranscript: func(id string, u providers.TranscriptUpdate) error {
			r.mu.Lock()
			r.updates = append(r.updates, u)
			r.mu.Unlock()
			return nil
		},
		OnEvent: func(id string, e providers.MediaEvent) {
			r.mu.Lock()
			r.events = append(r.events, e)
			r.mu.Unlock()
		},
		StartTimeout: 2 * time.Second,
	}
}

func (r *recorder) eventKinds() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		out = append(out, string(e.Kind)+":"+e.Mark)
	}
	return out
}

func serve(cfg Config, p *pipe) (chan error, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, p, cfg) }()
	return done, cancel
}

func wait(t *testing.T, done chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("bridge did not return")
		return nil
	}
}

func TestVerifiedStreamCarriesAudioMarksAndStop(t *testing.T) {
	p, tr, rec := newPipe(), &fakeTranscriber{}, newRecorder()
	done, cancel := serve(rec.config(tr, nil), p)
	defer cancel()
	p.send(t, map[string]any{"event": "connected", "protocol": "Call", "version": "1.0.0"})
	p.send(t, startMsg(1, validParams(), nil))
	out := <-rec.started
	frame := make([]byte, 160)
	for i := range frame {
		frame[i] = 0xff
	}
	p.send(t, mediaMsg(2, frame))
	p.send(t, mediaMsg(3, frame[:80]))

	speech := make([]byte, 320)
	if err := out.Send(context.Background(), providers.AudioChunk{Format: providers.TelephonyFormat, Seq: 1, Data: speech}); err != nil {
		t.Fatal(err)
	}
	if err := out.Mark(context.Background(), "resp-1"); err != nil {
		t.Fatal(err)
	}
	m := p.next(t)
	payload, _ := base64.StdEncoding.DecodeString(m["media"].(map[string]any)["payload"].(string))
	if m["event"] != "media" || m["streamSid"] != stream || len(payload) != 320 {
		t.Fatalf("outbound media = %v", m)
	}
	if m := p.next(t); m["event"] != "mark" || m["mark"].(map[string]any)["name"] != "resp-1" {
		t.Fatalf("outbound mark = %v", m)
	}
	p.send(t, map[string]any{"event": "mark", "sequenceNumber": "4", "streamSid": stream, "mark": map[string]any{"name": "resp-1"}})
	p.send(t, map[string]any{"event": "mark", "sequenceNumber": "5", "streamSid": stream, "mark": map[string]any{"name": "never-sent"}})
	p.send(t, map[string]any{"event": "stop", "sequenceNumber": "6", "streamSid": stream, "stop": map[string]any{"callSid": sid}})
	if err := wait(t, done); err != nil {
		t.Fatalf("clean stop returned %v", err)
	}
	opened, closed, pushed := tr.snapshot()
	if opened != 1 || !closed || pushed != 2 {
		t.Fatalf("transcriber opened=%d closed=%v pushed=%d", opened, closed, pushed)
	}
	if got := strings.Join(rec.eventKinds(), ","); got != "mark:resp-1,stop:" {
		t.Fatalf("events = %s", got)
	}
	rec.mu.Lock()
	if len(rec.updates) != 2 || !rec.updates[1].IsFinal {
		t.Fatalf("updates = %+v", rec.updates)
	}
	rec.mu.Unlock()
	if err := out.Send(context.Background(), providers.AudioChunk{Format: providers.TelephonyFormat, Seq: 2, Data: speech}); !errors.Is(err, providers.ErrClosed) {
		t.Fatalf("send after stop = %v", err)
	}
}

func TestStreamOwnershipAndProtocolRejection(t *testing.T) {
	otherCall := "1c7d5a8f-4a2b-4d3c-8e4f-2b3c4d5e6f70"
	cases := []struct {
		name   string
		msgs   []any
		lookup func(context.Context, string) (string, error)
		want   error
	}{
		{"no credential", []any{startMsg(1, map[string]string{"call_id": callID}, nil)}, nil, ErrUnauthorized},
		{"forged token", []any{startMsg(1, map[string]string{"call_id": callID, "token": SignStream([]byte("wrong-secret-wrong-secret-wrong-secret"), callID, sid)}, nil)}, nil, ErrUnauthorized},
		{"token for another call", []any{startMsg(1, map[string]string{"call_id": callID, "token": SignStream(secret, otherCall, sid)}, nil)}, nil, ErrUnauthorized},
		{"sid not stored for this call", []any{startMsg(1, validParams(), nil)},
			func(context.Context, string) (string, error) {
				return "CA" + "ffffffffffffffff" + "ffffffffffffffff", nil
			}, ErrUnauthorized},
		{"call not active", []any{startMsg(1, validParams(), nil)},
			func(context.Context, string) (string, error) { return "", errors.New("ended") }, ErrUnauthorized},
		{"media before start", []any{mediaMsg(1, []byte{1})}, nil, ErrProtocol},
		{"pcm audio", []any{startMsg(1, validParams(), func(s map[string]any) {
			s["mediaFormat"] = map[string]any{"encoding": "audio/l16", "sampleRate": 16000, "channels": 1}
		})}, nil, providers.ErrUnsupportedFormat},
		{"stereo", []any{startMsg(1, validParams(), func(s map[string]any) {
			s["mediaFormat"].(map[string]any)["channels"] = 2
		})}, nil, providers.ErrUnsupportedFormat},
		{"outbound-only track", []any{startMsg(1, validParams(), func(s map[string]any) { s["tracks"] = []string{"outbound"} })}, nil, ErrProtocol},
		{"malformed sid", []any{startMsg(1, validParams(), func(s map[string]any) { s["callSid"] = "CA123" })}, nil, ErrProtocol},
		{"stream id switch", []any{startMsg(1, validParams(), nil), func() map[string]any {
			m := mediaMsg(2, []byte{1})
			m["streamSid"] = "MZ" + "ffffffffffffffff" + "ffffffffffffffff"
			return m
		}()}, nil, ErrProtocol},
		{"sequence replay", []any{startMsg(1, validParams(), nil), mediaMsg(2, []byte{1}), mediaMsg(2, []byte{1})}, nil, ErrProtocol},
		{"bad base64", []any{startMsg(1, validParams(), nil), map[string]any{"event": "media", "sequenceNumber": "2", "streamSid": stream,
			"media": map[string]any{"payload": "%%%"}}}, nil, ErrProtocol},
		{"unknown event", []any{startMsg(1, validParams(), nil), map[string]any{"event": "surprise", "sequenceNumber": "2", "streamSid": stream}}, nil, ErrProtocol},
		{"not json", []any{"{"}, nil, ErrProtocol},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, tr, rec := newPipe(), &fakeTranscriber{}, newRecorder()
			done, cancel := serve(rec.config(tr, tc.lookup), p)
			defer cancel()
			for _, m := range tc.msgs {
				if s, ok := m.(string); ok {
					p.in <- []byte(s)
					continue
				}
				p.send(t, m)
			}
			err := wait(t, done)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if strings.Contains(fmt.Sprint(err), "surprise") || strings.Contains(fmt.Sprint(err), sid) {
				t.Fatal("error echoes provider input")
			}
			if opened, _, pushed := tr.snapshot(); tc.want == ErrUnauthorized && (opened != 0 || pushed != 0) {
				t.Fatal("unauthorized stream reached recognition")
			}
		})
	}
}

func TestStartTimeout(t *testing.T) {
	p, tr, rec := newPipe(), &fakeTranscriber{}, newRecorder()
	cfg := rec.config(tr, nil)
	cfg.StartTimeout = 50 * time.Millisecond
	done, cancel := serve(cfg, p)
	defer cancel()
	if err := wait(t, done); !errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v", err)
	}
}

func TestInboundBacklogIsBounded(t *testing.T) {
	p, rec := newPipe(), newRecorder()
	tr := &fakeTranscriber{gate: make(chan struct{})} // recognition stalls
	cfg := rec.config(tr, nil)
	cfg.InboundFrames = 4
	done, cancel := serve(cfg, p)
	defer cancel()
	p.send(t, startMsg(1, validParams(), nil))
	<-rec.started
	// One frame is held by the stalled Push, four fill the queue, the next overflows.
	for i := range 6 {
		p.send(t, mediaMsg(2+i, []byte{0xff}))
	}
	if err := wait(t, done); !errors.Is(err, ErrBacklog) {
		t.Fatalf("err = %v", err)
	}
	if _, closed, _ := tr.snapshot(); !closed {
		t.Fatal("recognition not closed after backlog")
	}
}

func TestOutboundQueueBoundAndClearDropsPendingMarks(t *testing.T) {
	p, tr, rec := newPipe(), &fakeTranscriber{}, newRecorder()
	p.block = make(chan struct{}) // the provider socket is not accepting writes
	cfg := rec.config(tr, nil)
	cfg.OutboundMessages = 3
	done, cancel := serve(cfg, p)
	defer cancel()
	p.send(t, startMsg(1, validParams(), nil))
	out := <-rec.started
	chunk := providers.AudioChunk{Format: providers.TelephonyFormat, Seq: 1, Data: make([]byte, 160)}
	// The writer takes the first message and blocks on it; three more fill the queue.
	sent := 0
	var err error
	for range 10 {
		if err = out.Send(context.Background(), chunk); err != nil {
			break
		}
		sent++
	}
	if !errors.Is(err, providers.ErrInvalid) || sent > 4 {
		t.Fatalf("queue accepted %d messages, last err %v", sent, err)
	}
	if err := out.Send(context.Background(), providers.AudioChunk{Format: providers.AudioFormat{Encoding: providers.EncodingPCM16, SampleRate: 16000, Channels: 1}, Seq: 1, Data: []byte{1, 2}}); !errors.Is(err, providers.ErrUnsupportedFormat) {
		t.Fatalf("pcm send = %v", err)
	}
	dropped, err := out.Clear(context.Background())
	if err != nil || dropped == 0 {
		t.Fatalf("clear dropped %d, %v", dropped, err)
	}
	if err := out.Mark(context.Background(), "resp-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := out.Clear(context.Background()); err != nil { // cancels resp-2 before it is acknowledged
		t.Fatal(err)
	}
	close(p.block)
	var events []string
	for {
		m := p.next(t)
		events = append(events, m["event"].(string))
		if m["event"] == "clear" {
			break
		}
	}
	// The provider may still echo the cleared mark; it must not be reported.
	p.send(t, map[string]any{"event": "mark", "sequenceNumber": "2", "streamSid": stream, "mark": map[string]any{"name": "resp-2"}})
	p.send(t, map[string]any{"event": "stop", "sequenceNumber": "3", "streamSid": stream})
	if err := wait(t, done); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rec.eventKinds(), ","); got != "stop:" {
		t.Fatalf("events = %s (outbound %v)", got, events)
	}
}

func TestCancellationClosesEverything(t *testing.T) {
	p, tr, rec := newPipe(), &fakeTranscriber{}, newRecorder()
	done, cancel := serve(rec.config(tr, nil), p)
	p.send(t, startMsg(1, validParams(), nil))
	out := <-rec.started
	cancel()
	if err := wait(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if _, closed, _ := tr.snapshot(); !closed {
		t.Fatal("recognition left open")
	}
	if err := out.Mark(context.Background(), "late"); !errors.Is(err, providers.ErrClosed) {
		t.Fatalf("mark after cancel = %v", err)
	}
}

func TestAuthorizerConfiguration(t *testing.T) {
	short := TokenAuthorizer{Secret: []byte("short"), ProviderCallID: func(context.Context, string) (string, error) { return sid, nil }}
	if _, err := short.Authorize(context.Background(), Start{CallSID: sid, CustomParameters: validParams()}); err == nil {
		t.Fatal("short secret accepted")
	}
	if SignStream(secret, callID, sid) == SignStream(secret, callID, "CA"+"ffffffffffffffff"+"ffffffffffffffff") {
		t.Fatal("token does not bind the provider call")
	}
}
