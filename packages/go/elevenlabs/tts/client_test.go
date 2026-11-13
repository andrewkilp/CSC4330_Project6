package tts

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func config() Config {
	return Config{APIKey: "fixture-key", VoiceID: "configured-voice", Model: "configured-model"}
}
func speech() providers.SpeechRequest {
	return providers.SpeechRequest{Text: "Is a table for four available?", Voice: "configured-voice", Format: providers.TelephonyFormat,
		Token: providers.Token{ContextRevision: 3, Generation: 7}}
}
func client(t *testing.T, tr transport) *Client {
	t.Helper()
	c, err := New(config())
	if err != nil {
		t.Fatal(err)
	}
	c.http.Transport = tr
	return c
}
func response(status int, media string, data []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {media}}, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}
}
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestStreamingFixtureRequestMetadataAndFinalChunk(t *testing.T) {
	var f struct {
		ContentType  string `json:"content_type"`
		Pattern      string `json:"pattern_hex"`
		Repeat       int    `json:"repeat"`
		OutputFormat string `json:"output_format"`
	}
	if err := json.Unmarshal(fixture(t, "stream.json"), &f); err != nil {
		t.Fatal(err)
	}
	pattern, err := hex.DecodeString(f.Pattern)
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat(pattern, f.Repeat)
	var requests int
	c := client(t, func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Method != "POST" || r.URL.Scheme != "https" || r.URL.Host != "api.elevenlabs.io" || r.URL.Path != "/v1/text-to-speech/configured-voice/stream" || r.URL.Query().Get("output_format") != f.OutputFormat {
			t.Fatalf("unexpected synthesis request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("xi-api-key") != "fixture-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatal("missing configured headers")
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 2 || body["text"] != speech().Text || body["model_id"] != "configured-model" {
			t.Fatalf("body = %+v", body)
		}
		return response(200, f.ContentType, want), nil
	})
	var chunks []providers.SpeechChunk
	if err := c.Stream(context.Background(), speech(), func(s providers.SpeechChunk) error { chunks = append(chunks, s); return nil }); err != nil {
		t.Fatal(err)
	}
	var got []byte
	if len(chunks) != 3 || requests != 1 {
		t.Fatalf("chunks=%d requests=%d", len(chunks), requests)
	}
	for i, s := range chunks {
		if err := s.Audio.Validate(); err != nil {
			t.Fatal(err)
		}
		if s.Audio.Format != providers.TelephonyFormat || s.Token != speech().Token || s.Audio.Seq != uint64(i+1) || s.Last != (i == len(chunks)-1) || len(s.Audio.Data) > ChunkBytes {
			t.Fatalf("chunk %d metadata = %+v", i, s)
		}
		got = append(got, s.Audio.Data...)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("stream bytes differ from synthetic fixture")
	}
	// Every callback owns its bytes; subsequent reads do not overwrite them.
	if !bytes.Equal(chunks[0].Audio.Data, want[:ChunkBytes]) {
		t.Fatal("chunk data was reused")
	}
}

func TestInvalidInputNeverMakesRequest(t *testing.T) {
	c := client(t, func(*http.Request) (*http.Response, error) { t.Fatal("network for invalid input"); return nil, nil })
	for _, tc := range []struct {
		name   string
		mutate func(*providers.SpeechRequest)
		want   error
	}{
		{"blank", func(r *providers.SpeechRequest) { r.Text = " " }, providers.ErrInvalid},
		{"long", func(r *providers.SpeechRequest) { r.Text = strings.Repeat("x", 1501) }, providers.ErrInvalid},
		{"utf16 bound", func(r *providers.SpeechRequest) { r.Text = strings.Repeat("😀", 751) }, providers.ErrInvalid},
		{"invalid utf8", func(r *providers.SpeechRequest) { r.Text = string([]byte{0xff}) }, providers.ErrInvalid},
		{"wrong voice", func(r *providers.SpeechRequest) { r.Voice = "browser-voice" }, providers.ErrInvalid},
		{"no voice", func(r *providers.SpeechRequest) { r.Voice = "" }, providers.ErrInvalid},
		{"pcm", func(r *providers.SpeechRequest) { r.Format.Encoding = providers.EncodingPCM16 }, providers.ErrUnsupportedFormat},
		{"wrong rate", func(r *providers.SpeechRequest) { r.Format.SampleRate = 16000 }, providers.ErrUnsupportedFormat},
		{"stereo", func(r *providers.SpeechRequest) { r.Format.Channels = 2 }, providers.ErrUnsupportedFormat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := speech()
			tc.mutate(&r)
			if err := c.Stream(context.Background(), r, func(providers.SpeechChunk) error { t.Fatal("callback"); return nil }); !errors.Is(err, tc.want) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if err := c.Stream(context.Background(), speech(), nil); !errors.Is(err, providers.ErrInvalid) {
		t.Fatalf("nil callback=%v", err)
	}
	var nilClient *Client
	if err := nilClient.Stream(context.Background(), speech(), func(providers.SpeechChunk) error { return nil }); !errors.Is(err, providers.ErrInvalid) {
		t.Fatalf("nil client=%v", err)
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for _, mutate := range []func(*Config){
		func(c *Config) { c.APIKey = "" }, func(c *Config) { c.APIKey = "key\r\nInjected: x" },
		func(c *Config) { c.VoiceID = "../other" }, func(c *Config) { c.Model = "" },
		func(c *Config) { c.Timeout = -time.Second }, func(c *Config) { c.Timeout = 2 * time.Minute },
	} {
		cfg := config()
		mutate(&cfg)
		if _, err := New(cfg); !errors.Is(err, providers.ErrInvalid) {
			t.Fatalf("New error=%v", err)
		}
	}
}

func TestProviderFailuresAreSafeAndNotRetried(t *testing.T) {
	for _, status := range []int{401, 403, 422, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			c := client(t, func(*http.Request) (*http.Response, error) {
				calls++
				return response(status, "application/json", fixture(t, "unauthorized.json")), nil
			})
			err := c.Stream(context.Background(), speech(), func(providers.SpeechChunk) error { t.Fatal("audio from failed response"); return nil })
			if !errors.Is(err, providers.ErrUnavailable) || strings.Contains(err.Error(), "fixture-") || calls != 1 {
				t.Fatalf("error=%v calls=%d", err, calls)
			}
		})
	}
}

func TestRejectsEmptyWrongFormatAndOversizedResponses(t *testing.T) {
	for _, tc := range []struct {
		name, media string
		data        []byte
		want        error
	}{
		{"empty", "application/octet-stream", nil, providers.ErrUnavailable},
		{"json", "application/json", []byte(`{"audio":"not audio"}`), providers.ErrUnsupportedFormat},
		{"mp3", "audio/mpeg", []byte("ID3"), providers.ErrUnsupportedFormat},
		{"wrong rate", "audio/basic; rate=16000", []byte{0xff}, providers.ErrUnsupportedFormat},
		{"stereo", "audio/basic; channels=2", []byte{0xff}, providers.ErrUnsupportedFormat},
		{"oversized", "application/octet-stream", bytes.Repeat([]byte{0xff}, MaxAudioBytes+1), providers.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := client(t, func(*http.Request) (*http.Response, error) { return response(200, tc.media, tc.data), nil })
			if err := c.Stream(context.Background(), speech(), func(providers.SpeechChunk) error { t.Fatal("unexpected audio"); return nil }); !errors.Is(err, tc.want) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	// Unknown-length/chunked responses are bounded too, even without Content-Length.
	c := client(t, func(*http.Request) (*http.Response, error) {
		r := response(200, "application/octet-stream", bytes.Repeat([]byte{0xff}, MaxAudioBytes+1))
		r.ContentLength = -1
		return r, nil
	})
	last := false
	err := c.Stream(context.Background(), speech(), func(s providers.SpeechChunk) error { last = last || s.Last; return nil })
	if !errors.Is(err, providers.ErrUnavailable) || last {
		t.Fatalf("overflow error=%v final=%v", err, last)
	}
}

func TestCancellationAndCallbackErrorStopDelivery(t *testing.T) {
	for _, mode := range []string{"cancel", "callback"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			closed := false
			c := client(t, func(*http.Request) (*http.Response, error) {
				r := response(200, "application/octet-stream", bytes.Repeat([]byte{0xff}, ChunkBytes*4))
				r.Body = &closeRecorder{ReadCloser: r.Body, closed: &closed}
				return r, nil
			})
			callbackErr := errors.New("stop callback")
			calls := 0
			err := c.Stream(ctx, speech(), func(s providers.SpeechChunk) error {
				calls++
				if s.Last {
					t.Fatal("cancelled stream finalized")
				}
				if mode == "cancel" {
					cancel()
					return nil
				}
				return callbackErr
			})
			want := error(context.Canceled)
			if mode == "callback" {
				want = callbackErr
			}
			if !errors.Is(err, want) || calls != 1 || !closed {
				t.Fatalf("err=%v calls=%d closed=%v", err, calls, closed)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := client(t, func(*http.Request) (*http.Response, error) { t.Fatal("request after cancel"); return nil, nil })
	if err := c.Stream(ctx, speech(), func(providers.SpeechChunk) error { t.Fatal("callback"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type closeRecorder struct {
	io.ReadCloser
	closed *bool
}

func (r *closeRecorder) Close() error { *r.closed = true; return r.ReadCloser.Close() }

type failingReader struct{ sent bool }

func (r *failingReader) Read(b []byte) (int, error) {
	if r.sent {
		return 0, errors.New("fixture-private-stream-error")
	}
	r.sent = true
	return copy(b, bytes.Repeat([]byte{0xff}, ChunkBytes)), nil
}
func (r *failingReader) Close() error { return nil }
func TestReadFailureDropsPendingChunk(t *testing.T) {
	c := client(t, func(*http.Request) (*http.Response, error) {
		r := response(200, "application/octet-stream", nil)
		r.Body = &failingReader{}
		r.ContentLength = -1
		return r, nil
	})
	err := c.Stream(context.Background(), speech(), func(providers.SpeechChunk) error { t.Fatal("pending audio emitted after read failure"); return nil })
	if !errors.Is(err, providers.ErrUnavailable) || strings.Contains(err.Error(), "fixture-") {
		t.Fatal(err)
	}
}

// Rewrite only inside tests: every request is sent to a local server, never to
// api.elevenlabs.io. Production exposes no configurable endpoint or redirect.
func localClient(t *testing.T, cfg Config, handler http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	u, err := url.Parse(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		cloneURL := *r.URL
		copy.URL = &cloneURL
		copy.URL.Scheme = u.Scheme
		copy.URL.Host = u.Host
		return http.DefaultTransport.RoundTrip(copy)
	})
	return c
}

func TestTimeoutDuringHeadersAndBody(t *testing.T) {
	for _, body := range []bool{false, true} {
		t.Run(map[bool]string{false: "headers", true: "body"}[body], func(t *testing.T) {
			cfg := config()
			cfg.Timeout = 50 * time.Millisecond
			cancelled := make(chan struct{})
			c := localClient(t, cfg, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if body {
					w.Header().Set("Content-Type", "application/octet-stream")
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
				close(cancelled)
			})
			err := c.Stream(context.Background(), speech(), func(providers.SpeechChunk) error { t.Fatal("timeout emitted audio"); return nil })
			if !errors.Is(err, providers.ErrUnavailable) {
				t.Fatalf("timeout error=%v", err)
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("request did not cancel")
			}
		})
	}
}

func TestCancelUnblocksStalledBody(t *testing.T) {
	started := make(chan struct{})
	c := localClient(t, config(), func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- c.Stream(ctx, speech(), func(providers.SpeechChunk) error { t.Error("audio after cancel"); return nil })
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("body cancellation blocked")
	}
}

func TestDeliversBeforeTheResponseFinishes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := localClient(t, config(), func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(bytes.Repeat([]byte{0xff}, ChunkBytes*2))
		w.(http.Flusher).Flush()
		// Never finish the response until the consumer receives streaming audio.
		<-r.Context().Done()
	})
	calls := 0
	err := c.Stream(ctx, speech(), func(chunk providers.SpeechChunk) error {
		calls++
		if chunk.Last || len(chunk.Audio.Data) != ChunkBytes {
			t.Fatal("expected a bounded chunk before response completion")
		}
		cancel()
		return nil
	})
	if calls != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("callbacks=%d error=%v", calls, err)
	}
}

func TestRedirectDoesNotForwardKey(t *testing.T) {
	var calls atomic.Int32
	c := localClient(t, config(), func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "https://example.invalid/private")
		w.WriteHeader(307)
	})
	err := c.Stream(context.Background(), speech(), func(providers.SpeechChunk) error { t.Fatal("redirect audio"); return nil })
	if !errors.Is(err, providers.ErrUnavailable) || calls.Load() != 1 {
		t.Fatalf("error=%v requests=%d", err, calls.Load())
	}
}
