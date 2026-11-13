package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, e := os.ReadFile("testdata/" + name + ".sse")
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func input() Input {
	return Input{Goal: json.RawMessage(`{"kind":"restaurant"}`), Transcript: []Turn{{"business", "What time?"}}, Instructions: []string{"Ask for 7 pm"}, ContextRevision: 3}
}
func client(t *testing.T, fn transport) *Client {
	t.Helper()
	c, e := New(Config{"fixture-key", "configured-model", 256})
	if e != nil {
		t.Fatal(e)
	}
	c.http.Transport = fn
	return c
}
func response(status int, s string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(s))}
}

func TestCompleteRequest(t *testing.T) {
	c := client(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.anthropic.com/v1/messages" || r.Header.Get("x-api-key") != "fixture-key" || r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Fatal("request headers")
		}
		var b struct {
			Model     string
			Stream    bool
			MaxTokens int `json:"max_tokens"`
			Messages  []struct{ Content string }
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.Model != "configured-model" || !b.Stream || b.MaxTokens != 256 {
			t.Fatal("request config")
		}
		var in Input
		if json.Unmarshal([]byte(b.Messages[0].Content), &in) != nil || len(in.Instructions) != 1 || in.Transcript[0].Text != "What time?" {
			t.Fatal("private context separation")
		}
		return response(200, fixture(t, "complete")), nil
	})
	var updates []Update
	if e := c.Generate(context.Background(), input(), func(u Update) error { updates = append(updates, u); return nil }); e != nil {
		t.Fatal(e)
	}
	if len(updates) != 3 || !updates[2].Final || updates[2].Text != "Is 7 pm available?" || updates[2].ContextRevision != 3 {
		t.Fatalf("updates: %+v", updates)
	}
}

func TestServerPolicyIsSeparateFromUserContext(t *testing.T) {
	c := client(t, func(r *http.Request) (*http.Response, error) {
		var b struct {
			System   string
			Messages []struct{ Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Fatal(err)
		}
		if b.System != "Trusted server policy" || strings.Contains(b.Messages[0].Content, "Trusted server policy") {
			t.Fatal("policy boundary lost")
		}
		return response(200, fixture(t, "complete")), nil
	})
	in := input()
	in.SystemPolicy = "Trusted server policy"
	if err := c.Generate(context.Background(), in, func(Update) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var decoded Input
	if err := json.Unmarshal([]byte(`{"SystemPolicy":"remote override"}`), &decoded); err != nil || decoded.SystemPolicy != "" {
		t.Fatal("JSON can set server policy")
	}
}
func TestFailuresNeverFinalize(t *testing.T) {
	complete := fixture(t, "complete")
	cases := []struct {
		name, body string
		status     int
		want       error
	}{
		{"partial", fixture(t, "partial-failure"), 200, ErrUnavailable},
		{"rate limit", `{"error":{"message":"fixture secret"}}`, 429, ErrRateLimited},
		{"auth", "fixture secret", 401, ErrUnavailable},
		{"truncated", strings.Split(complete, "event: message_delta")[0], 200, ErrIncomplete},
		{"token limit", strings.ReplaceAll(complete, "end_turn", "max_tokens"), 200, ErrIncomplete},
		{"oversized", strings.ReplaceAll(complete, "Is 7 pm ", strings.Repeat("x", 1501)), 200, ErrLimit},
		{"malformed", "data: {broken}\n\n", 200, ErrIncomplete},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := client(t, func(*http.Request) (*http.Response, error) { return response(tc.status, tc.body), nil })
			e := c.Generate(context.Background(), input(), func(u Update) error {
				if u.Final {
					t.Fatal("finalized failed stream")
				}
				return nil
			})
			if !errors.Is(e, tc.want) {
				t.Fatalf("got %v want %v", e, tc.want)
			}
		})
	}
}
func TestCancelDuringRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	reading := make(chan struct{})
	c := client(t, func(r *http.Request) (*http.Response, error) {
		close(reading)
		go func() { <-r.Context().Done(); writer.CloseWithError(r.Context().Err()) }()
		res := response(200, "")
		res.Body = reader
		return res, nil
	})
	done := make(chan error, 1)
	go func() {
		done <- c.Generate(ctx, input(), func(Update) error { t.Error("unexpected callback"); return nil })
	}()
	<-reading
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not unblock")
	}
}
func TestCancelAfterDraft(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := client(t, func(*http.Request) (*http.Response, error) { return response(200, fixture(t, "complete")), nil })
	n := 0
	e := c.Generate(ctx, input(), func(Update) error { n++; cancel(); return nil })
	if !errors.Is(e, context.Canceled) || n != 1 {
		t.Fatalf("%v callbacks=%d", e, n)
	}
}
func TestCallbackError(t *testing.T) {
	want := errors.New("stop")
	c := client(t, func(*http.Request) (*http.Response, error) { return response(200, fixture(t, "complete")), nil })
	if e := c.Generate(context.Background(), input(), func(Update) error { return want }); e != want {
		t.Fatal(e)
	}
}

func TestRecordedRateLimit(t *testing.T) {
	raw, err := os.ReadFile("testdata/rate-limit.http")
	if err != nil {
		t.Fatal(err)
	}
	c := client(t, func(*http.Request) (*http.Response, error) {
		return http.ReadResponse(bufio.NewReader(bytes.NewReader(raw)), nil)
	})
	if err = c.Generate(context.Background(), input(), func(Update) error { t.Fatal("rate limit emitted text"); return nil }); err != ErrRateLimited {
		t.Fatal(err)
	}
}

func TestInvalidInputDoesNotSend(t *testing.T) {
	c := client(t, func(*http.Request) (*http.Response, error) { t.Fatal("invalid input reached network"); return nil, nil })
	in := input()
	in.Instructions = []string{strings.Repeat("x", 1001)}
	if err := c.Generate(context.Background(), in, func(Update) error { return nil }); err != ErrInvalid {
		t.Fatal(err)
	}
	if _, err := New(Config{"key\nsecret", "model", 256}); err != ErrInvalid {
		t.Fatal(err)
	}
}
