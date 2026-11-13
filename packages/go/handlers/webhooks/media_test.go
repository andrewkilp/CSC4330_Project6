package webhooks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/telephony/media"
)

const (
	callID             = "0b6c4f7e-3f1a-4c2b-9d3e-1a2b3c4d5e6f"
	mediaTestStreamSID = "MZ" + "0123456789abcdef" + "0123456789abcdef"
	sid                = "CA" + "0123456789abcdef" + "0123456789abcdef"
)

var secret = []byte("0123456789abcdef0123456789abcdef-test-only")

type transcriber struct {
	mu     sync.Mutex
	frames int
	closed bool
}

func (tr *transcriber) Open(ctx context.Context, o providers.TranscriptOpen, emit func(providers.TranscriptUpdate) error) (providers.TranscriptSession, error) {
	return session{tr, emit}, nil
}

type session struct {
	tr   *transcriber
	emit func(providers.TranscriptUpdate) error
}

func (s session) Push(ctx context.Context, c providers.AudioChunk) error {
	s.tr.mu.Lock()
	s.tr.frames++
	s.tr.mu.Unlock()
	return s.emit(providers.TranscriptUpdate{TurnID: "t", Text: "hello", IsFinal: true})
}

func (s session) Close() error {
	s.tr.mu.Lock()
	s.tr.closed = true
	s.tr.mu.Unlock()
	return nil
}

func server(t *testing.T, tr *transcriber, events chan providers.MediaEvent) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	RegisterMedia(mux, media.Config{
		Authorizer:  media.TokenAuthorizer{Secret: secret, ProviderCallID: func(context.Context, string) (string, error) { return sid, nil }},
		Transcriber: tr,
		OnStart: func(id string, out providers.MediaStream) {
			go func() {
				_ = out.Send(context.Background(), providers.AudioChunk{Format: providers.TelephonyFormat, Seq: 1, Data: []byte{0x7f, 0x7f}})
				_ = out.Mark(context.Background(), "greeting")
			}()
		},
		OnEvent: func(id string, e providers.MediaEvent) { events <- e },
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func dial(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http")+"/webhooks/twilio/media", nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func send(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	data, _ := json.Marshal(v)
	if err := c.Write(context.Background(), websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func start(token string) map[string]any {
	return map[string]any{"event": "start", "sequenceNumber": "1", "streamSid": mediaTestStreamSID, "start": map[string]any{
		"streamSid": mediaTestStreamSID, "callSid": sid, "accountSid": "AC" + "0123456789abcdef" + "0123456789abcdef", "tracks": []string{"inbound"},
		"customParameters": map[string]string{"call_id": callID, "token": token},
		"mediaFormat":      map[string]any{"encoding": "audio/x-mulaw", "sampleRate": 8000, "channels": 1}}}
}

// A simulated Twilio client drives a full stream over a real WebSocket.
func TestSimulatedTwilioStream(t *testing.T) {
	tr, events := &transcriber{}, make(chan providers.MediaEvent, 4)
	srv := server(t, tr, events)
	c := dial(t, srv)
	defer c.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	send(t, c, map[string]any{"event": "connected", "protocol": "Call", "version": "1.0.0"})
	send(t, c, start(media.SignStream(secret, callID, sid)))
	send(t, c, map[string]any{"event": "media", "sequenceNumber": "2", "streamSid": mediaTestStreamSID,
		"media": map[string]any{"track": "inbound", "payload": base64.StdEncoding.EncodeToString(make([]byte, 160))}})
	var got []string
	for len(got) < 2 {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		if m["streamSid"] != mediaTestStreamSID {
			t.Fatalf("outbound for another stream: %s", data)
		}
		got = append(got, m["event"].(string))
	}
	if strings.Join(got, ",") != "media,mark" {
		t.Fatalf("outbound = %v", got)
	}
	send(t, c, map[string]any{"event": "mark", "sequenceNumber": "3", "streamSid": mediaTestStreamSID, "mark": map[string]any{"name": "greeting"}})
	send(t, c, map[string]any{"event": "stop", "sequenceNumber": "4", "streamSid": mediaTestStreamSID})
	if e := <-events; e.Kind != providers.MediaMark || e.Mark != "greeting" {
		t.Fatalf("event = %+v", e)
	}
	if e := <-events; e.Kind != providers.MediaStop {
		t.Fatalf("event = %+v", e)
	}
	_, _, err := c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("close = %v", err)
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.frames != 1 || !tr.closed {
		t.Fatalf("frames=%d closed=%v", tr.frames, tr.closed)
	}
}

func TestUnauthorizedStreamIsClosedWithPolicyViolation(t *testing.T) {
	tr := &transcriber{}
	srv := server(t, tr, make(chan providers.MediaEvent, 1))
	c := dial(t, srv)
	defer c.CloseNow()
	send(t, c, start("forged"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("close = %v", err)
	}
	if tr.frames != 0 {
		t.Fatal("unauthorized audio reached recognition")
	}
}

func TestBinaryFramesAreRejected(t *testing.T) {
	srv := server(t, &transcriber{}, make(chan providers.MediaEvent, 1))
	c := dial(t, srv)
	defer c.CloseNow()
	_ = c.Write(context.Background(), websocket.MessageBinary, []byte{1, 2, 3})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusUnsupportedData {
		t.Fatalf("close = %v", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("server did not close")
	}
}
