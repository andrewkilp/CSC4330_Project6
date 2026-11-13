package transcription

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
	"github.com/coder/websocket"
)

const callID = "00000000-0000-4000-8000-000000000001"

func fixture(t *testing.T) [][]byte {
	t.Helper()
	f, err := os.Open("testdata/results.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out [][]byte
	s := bufio.NewScanner(f)
	for s.Scan() {
		out = append(out, append([]byte(nil), s.Bytes()...))
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for local fixture server")
		var zero T
		return zero
	}
}

func server(t *testing.T, handle func(*websocket.Conn)) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token test-only" {
			t.Error("missing auth header")
		}
		q := r.URL.Query()
		if q.Get("encoding") != "mulaw" || q.Get("sample_rate") != "8000" || q.Get("interim_results") != "true" || q.Get("endpointing") != "300" || q.Get("model") != "nova-3" || q.Get("mip_opt_out") != "true" {
			t.Error("wrong codec/recognition policy")
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer c.CloseNow()
		handle(c)
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{APIKey: "test-only"})
	if err != nil {
		t.Fatal(err)
	}
	c.endpoint = "ws" + strings.TrimPrefix(srv.URL, "http")
	return c
}

func open(t *testing.T, c *Client, ctx context.Context, emit func(providers.TranscriptUpdate) error) providers.TranscriptSession {
	t.Helper()
	s, err := c.Open(ctx, providers.TranscriptOpen{CallID: callID, Format: providers.TelephonyFormat}, emit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestFixtureMappingAndActualBinaryAudio(t *testing.T) {
	msgs := fixture(t)
	audio := make(chan []byte, 1)
	c := server(t, func(c *websocket.Conn) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		kind, b, err := c.Read(ctx)
		if err != nil {
			return
		}
		if kind != websocket.MessageBinary {
			t.Error("audio was not binary")
		}
		audio <- b
		for _, msg := range msgs {
			if c.Write(ctx, websocket.MessageText, msg) != nil {
				return
			}
		}
		_, _, _ = c.Read(ctx)
	})
	updates := make(chan providers.TranscriptUpdate, 8)
	s := open(t, c, context.Background(), func(u providers.TranscriptUpdate) error { updates <- u; return nil })
	data := []byte{0xff, 0xff, 0xff}
	if err := s.Push(context.Background(), providers.AudioChunk{Seq: 1, Format: providers.TelephonyFormat, Data: data}); err != nil {
		t.Fatal(err)
	}
	if string(receive(t, audio)) != string(data) {
		t.Fatal("audio changed codec bytes")
	}
	var got []providers.TranscriptUpdate
	for range 5 {
		got = append(got, receive(t, updates))
	}
	for i, u := range got {
		if !providers.ValidUUID(u.TurnID) {
			t.Fatal("non-UUID turn")
		}
		if i < 4 && u.TurnID != got[0].TurnID {
			t.Fatal("partials changed turn id")
		}
	}
	if got[1].IsFinal || got[2].IsFinal || !got[3].IsFinal || got[3].Text != "Table for four. At seven?" || got[3].StartedAtMS != 200 || got[3].EndedAtMS != 1600 {
		t.Fatalf("bad utterance aggregation: %+v", got)
	}
	if !got[4].IsFinal || got[4].TurnID == got[3].TurnID || got[4].Text != "Thank you." {
		t.Fatalf("bad next turn: %+v", got[4])
	}
}

func TestCloseInsideCallbackAndCancelStopLaterCallbacks(t *testing.T) {
	for _, cancelMode := range []bool{false, true} {
		t.Run(map[bool]string{true: "cancel", false: "close"}[cancelMode], func(t *testing.T) {
			proceed := make(chan struct{})
			done := make(chan struct{})
			msgs := fixture(t)
			c := server(t, func(c *websocket.Conn) {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				<-proceed
				for _, msg := range msgs[2:] {
					if c.Write(ctx, websocket.MessageText, msg) != nil {
						return
					}
				}
				_, _, _ = c.Read(ctx)
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var n atomic.Int32
			var s providers.TranscriptSession
			s = open(t, c, ctx, func(u providers.TranscriptUpdate) error {
				if n.Add(1) != 1 {
					t.Error("callback after stop")
				}
				if u.IsFinal {
					t.Error("pending partial finalized")
				}
				if cancelMode {
					cancel()
				} else {
					s.Close()
				}
				close(done)
				return nil
			})
			close(proceed)
			receive(t, done)
			if err := s.Push(context.Background(), providers.AudioChunk{Seq: 1, Format: providers.TelephonyFormat, Data: []byte{255}}); !errors.Is(err, providers.ErrClosed) {
				t.Fatalf("push after stop = %v", err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConcurrentExternalCloseRespectsLogicalCallbackAdmission(t *testing.T) {
	// Deliberately stop between admission and the user function's first
	// instruction. This is the exact reentrant-Close race documented in #6.
	c := server(t, func(c *websocket.Conn) { _, _, _ = c.Read(context.Background()) })
	var count atomic.Int32
	s := open(t, c, context.Background(), func(providers.TranscriptUpdate) error { count.Add(1); return nil }).(*session)
	if !s.admit() {
		t.Fatal("initial delivery not admitted")
	}
	closed := make(chan struct{})
	go func() { _ = s.Close(); close(closed) }()
	receive(t, closed)
	if s.admit() {
		t.Fatal("delivery admitted after external Close returned")
	}
	// The already-admitted callback may enter after Close and finish. Consumers
	// recheck context/token before persistence; Close cannot wait on self-Close.
	if err := s.emit(providers.TranscriptUpdate{Text: "in flight"}); err != nil {
		t.Fatal(err)
	}
	s.finishDelivery()
	if count.Load() != 1 || s.admit() {
		t.Fatal("callback admission not fenced")
	}
}

func TestCloseAndCancelBeforeAdmissionDropCallback(t *testing.T) {
	for _, mode := range []string{"close", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			c := server(t, func(c *websocket.Conn) { _, _, _ = c.Read(context.Background()) })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := open(t, c, ctx, func(providers.TranscriptUpdate) error { t.Fatal("callback after admission fence"); return nil }).(*session)
			if mode == "close" {
				s.Close()
			} else {
				cancel()
			}
			result := make(chan bool, 1)
			go func() { result <- s.admit() }()
			if receive(t, result) {
				t.Fatal("callback admitted after stop")
			}
		})
	}
}

func TestMalformedAndDisconnect(t *testing.T) {
	for _, msg := range []string{`{`, `{"type":"Error","description":"private-key-must-not-leak"}`, `{"type":"Results"}`, "disconnect"} {
		t.Run(msg, func(t *testing.T) {
			closed := make(chan struct{})
			c := server(t, func(c *websocket.Conn) {
				if msg != "disconnect" {
					_ = c.Write(context.Background(), websocket.MessageText, []byte(msg))
					_, _, _ = c.Read(context.Background())
				}
				close(closed)
			})
			s := open(t, c, context.Background(), func(providers.TranscriptUpdate) error { t.Error("unexpected callback"); return nil })
			receive(t, closed)
			deadline := time.Now().Add(time.Second)
			for s.(*session).status() == nil && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			err := s.Push(context.Background(), providers.AudioChunk{Seq: 1, Format: providers.TelephonyFormat, Data: []byte{255}})
			if !errors.Is(err, providers.ErrUnavailable) || strings.Contains(err.Error(), "private") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestCallbackErrorClosesAndDiscardsPendingText(t *testing.T) {
	proceed := make(chan struct{})
	done := make(chan struct{})
	msgs := fixture(t)
	c := server(t, func(c *websocket.Conn) {
		<-proceed
		for _, msg := range msgs[2:] {
			if c.Write(context.Background(), websocket.MessageText, msg) != nil {
				return
			}
		}
		_, _, _ = c.Read(context.Background())
	})
	var count atomic.Int32
	s := open(t, c, context.Background(), func(u providers.TranscriptUpdate) error {
		count.Add(1)
		close(done)
		return errors.New("private callback failure")
	})
	close(proceed)
	receive(t, done)
	deadline := time.Now().Add(time.Second)
	for s.(*session).status() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := s.Push(context.Background(), providers.AudioChunk{Seq: 1, Format: providers.TelephonyFormat, Data: []byte{255}}); !errors.Is(err, providers.ErrClosed) {
		t.Fatal(err)
	}
	if count.Load() != 1 {
		t.Fatal("callback continued after error")
	}
}

func TestHandshakeDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	c, _ := New(Config{APIKey: "test-only"})
	c.endpoint = "ws" + strings.TrimPrefix(srv.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := c.Open(ctx, providers.TranscriptOpen{CallID: callID, Format: providers.TelephonyFormat}, func(providers.TranscriptUpdate) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestIdleKeepAliveAndCancellation(t *testing.T) {
	keep := make(chan []byte, 1)
	c := server(t, func(c *websocket.Conn) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		kind, b, err := c.Read(ctx)
		if err != nil {
			return
		}
		if kind != websocket.MessageText {
			t.Error("keepalive is not text")
		}
		keep <- b
		_, _, _ = c.Read(ctx)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := open(t, c, ctx, func(providers.TranscriptUpdate) error { t.Error("silence produced a transcript"); return nil })
	select {
	case b := <-keep:
		if string(b) != `{"type":"KeepAlive"}` {
			t.Fatal(string(b))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no keepalive")
	}
	cancel()
	if err := s.Push(context.Background(), providers.AudioChunk{Seq: 1, Format: providers.TelephonyFormat, Data: []byte{255}}); !errors.Is(err, providers.ErrClosed) {
		t.Fatal(err)
	}
}

func TestRejectsOversizedVendorMessage(t *testing.T) {
	done := make(chan struct{})
	c := server(t, func(c *websocket.Conn) {
		_ = c.Write(context.Background(), websocket.MessageText, []byte(strings.Repeat("x", 64*1024+1)))
		_, _, _ = c.Read(context.Background())
		close(done)
	})
	s := open(t, c, context.Background(), func(providers.TranscriptUpdate) error { t.Error("unexpected callback"); return nil })
	receive(t, done)
	deadline := time.Now().Add(time.Second)
	for s.(*session).status() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !errors.Is(s.(*session).status(), providers.ErrUnavailable) {
		t.Fatal("oversized message did not close session")
	}
}

func TestRejectsBadAudioAndConfigurationBeforeSending(t *testing.T) {
	for _, key := range []string{"", "bad\r\nkey"} {
		if _, err := New(Config{APIKey: key}); !errors.Is(err, providers.ErrInvalid) {
			t.Fatal(err)
		}
	}
	c, _ := New(Config{APIKey: "test-only"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Open(ctx, providers.TranscriptOpen{}, func(providers.TranscriptUpdate) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := c.Open(context.Background(), providers.TranscriptOpen{}, nil); !errors.Is(err, providers.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := c.Open(context.Background(), providers.TranscriptOpen{CallID: callID, Format: providers.AudioFormat{}}, func(providers.TranscriptUpdate) error { return nil }); !errors.Is(err, providers.ErrUnsupportedFormat) {
		t.Fatal(err)
	}
	c = server(t, func(c *websocket.Conn) { _, _, _ = c.Read(context.Background()) })
	s := open(t, c, context.Background(), func(providers.TranscriptUpdate) error { return nil })
	for _, chunk := range []providers.AudioChunk{{Seq: 1, Format: providers.TelephonyFormat}, {Seq: 2, Format: providers.TelephonyFormat, Data: []byte{1}}, {Seq: 1, Format: providers.TelephonyFormat, Data: make([]byte, providers.MaxChunkBytes+1)}} {
		if err := s.Push(context.Background(), chunk); !errors.Is(err, providers.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if err := s.Push(context.Background(), providers.AudioChunk{Seq: 1, Data: []byte{1}}); !errors.Is(err, providers.ErrUnsupportedFormat) {
		t.Fatal(err)
	}
}

func TestUnauthorizedIsSafe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte("private provider body"))
	}))
	defer srv.Close()
	c, _ := New(Config{APIKey: "test-only"})
	c.endpoint = "ws" + strings.TrimPrefix(srv.URL, "http")
	_, err := c.Open(context.Background(), providers.TranscriptOpen{CallID: callID, Format: providers.TelephonyFormat}, func(providers.TranscriptUpdate) error { return nil })
	if !errors.Is(err, providers.ErrUnavailable) || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
}

func TestSegmenterDuplicateAndSilence(t *testing.T) {
	msgs := fixture(t)
	var s segmenter
	for _, msg := range msgs[:4] {
		if _, err := s.consume(msg); err != nil {
			t.Fatal(err)
		}
	}
	if u, err := s.consume(msgs[3]); err != nil || u != nil {
		t.Fatalf("duplicate = %+v %v", u, err)
	}
	if u, err := s.consume([]byte(`{"type":"Results","start":1,"duration":0.5,"is_final":true,"speech_final":true,"channel_index":[0,1],"channel":{"alternatives":[{"transcript":""}]}}`)); err != nil || u == nil || !u.IsFinal || u.Text != "Table for four." {
		t.Fatalf("silence endpoint = %+v %v", u, err)
	}
}

func TestEndpointDoesNotInventFinalTextAndRepeatedEndpointFinalizesOnce(t *testing.T) {
	msgs := fixture(t)
	var s segmenter
	if _, err := s.consume(msgs[2]); err != nil {
		t.Fatal(err)
	}
	pendingID := s.id
	empty := []byte(`{"type":"Results","start":0.2,"duration":0.8,"is_final":true,"speech_final":true,"channel_index":[0,1],"channel":{"alternatives":[{"transcript":""}]}}`)
	if u, err := s.consume(empty); err != nil || u != nil {
		t.Fatalf("invented final from interim: %+v %v", u, err)
	}
	if s.id != pendingID {
		t.Fatal("empty endpoint abandoned a delivered pending turn")
	}
	if u, err := s.consume(msgs[6]); err != nil || u == nil || !u.IsFinal || u.TurnID != pendingID || u.Text != "Thank you." {
		t.Fatalf("later final did not resolve the same pending turn: %+v %v", u, err)
	}
	s = segmenter{}
	if _, err := s.consume(msgs[3]); err != nil {
		t.Fatal(err)
	}
	endpoint := []byte(strings.Replace(string(msgs[3]), `"speech_final":false`, `"speech_final":true`, 1))
	if u, err := s.consume(endpoint); err != nil || u == nil || !u.IsFinal || u.Text != "Table for four." {
		t.Fatalf("endpoint did not finalize committed text: %+v %v", u, err)
	}
	if u, err := s.consume(endpoint); err != nil || u != nil {
		t.Fatalf("duplicate endpoint reopened turn: %+v %v", u, err)
	}
}

func TestMalformedResultsAndOversizedText(t *testing.T) {
	base := string(fixture(t)[2])
	for _, bad := range []string{
		strings.Replace(base, `"start":0.2`, `"start":-1`, 1),
		strings.Replace(base, `"duration":0.4`, `"duration":90000`, 1),
		strings.Replace(base, `"channel_index":[0,1]`, `"channel_index":[1,2]`, 1),
		strings.Replace(base, `"speech_final":false`, `"speech_final":true`, 1),
		strings.Replace(base, "Table for", strings.Repeat("😀", 5001), 1),
	} {
		var s segmenter
		if _, err := s.consume([]byte(bad)); !errors.Is(err, providers.ErrUnavailable) {
			t.Fatalf("accepted invalid Results, error=%v", err)
		}
	}
}

func BenchmarkFixtureMapping(b *testing.B) {
	data, err := os.ReadFile("testdata/results.jsonl")
	if err != nil {
		b.Fatal(err)
	}
	msgs := strings.Split(strings.TrimSpace(string(data)), "\n")
	b.ReportAllocs()
	for b.Loop() {
		var s segmenter
		for _, msg := range msgs {
			if _, err := s.consume([]byte(msg)); err != nil {
				b.Fatal(err)
			}
		}
	}
}
