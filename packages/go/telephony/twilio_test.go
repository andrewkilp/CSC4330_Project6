package telephony

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
)

const testAccount = "AC11111111111111111111111111111111"
const testCallSID = "CA22222222222222222222222222222222"

func config() Config {
	return Config{AccountSID: testAccount, AuthToken: "test-only", FromNumber: "+12255550101"}
}
func dial() providers.Dial {
	return providers.Dial{CallID: "00000000-0000-4000-8000-000000000001", Destination: "+12255550100", Callbacks: providers.Callbacks{Voice: "https://demo.example/webhooks/twilio/voice?call_id=fictional", Status: "https://demo.example/webhooks/twilio/status?call_id=fictional", Media: "wss://demo.example/webhooks/twilio/media"}}
}
func fixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/create-call.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func localClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := New(config())
	if err != nil {
		t.Fatal(err)
	}
	c.endpoint = srv.URL
	return c
}

func TestRESTFixtureUsesBasicAuthFormAndNoRetries(t *testing.T) {
	body := fixture(t)
	var count atomic.Int32
	c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		user, pass, ok := r.BasicAuth()
		if !ok || user != testAccount || pass != "test-only" {
			t.Error("wrong basic auth")
		}
		if r.Method != "POST" || r.URL.Path != "/2010-04-01/Accounts/"+testAccount+"/Calls.json" {
			t.Error("wrong endpoint")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("To") != dial().Destination || r.Form.Get("From") != config().FromNumber || r.Form.Get("Url") != dial().Callbacks.Voice || r.Form.Get("StatusCallback") != dial().Callbacks.Status || r.Form.Get("TimeLimit") != "180" || r.Form.Get("Record") != "false" || strings.Join(r.Form["StatusCallbackEvent"], ",") != "initiated,ringing,answered,completed" {
			t.Error("wrong explicit dialing/callback policy")
		}
		w.WriteHeader(201)
		_, _ = w.Write(body)
	})
	sid, err := c.Start(context.Background(), dial())
	if err != nil || sid != testCallSID || count.Load() != 1 {
		t.Fatalf("Start=%s %v requests=%d", sid, err, count.Load())
	}
}

func TestRESTFailuresNeverLeakBodiesOrFollowRedirects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{{"unauthorized", 401, "private auth token"}, {"provider failure", 500, "private error"}, {"malformed", 201, "{"}, {"wrong account", 201, `{"sid":"` + testCallSID + `","account_sid":"AC99999999999999999999999999999999"}`}, {"oversized", 201, strings.Repeat("x", 32*1024+1)}, {"redirect", 302, ""}} {
		t.Run(tc.name, func(t *testing.T) {
			var n atomic.Int32
			c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
				n.Add(1)
				w.Header().Set("Location", "http://127.0.0.1/never")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			_, err := c.Start(context.Background(), dial())
			if !errors.Is(err, providers.ErrUnavailable) || strings.Contains(err.Error(), "private") || n.Load() != 1 {
				t.Fatalf("failure=%v requests=%d", err, n.Load())
			}
		})
	}
}

func TestRESTCancellationAndValidation(t *testing.T) {
	done := make(chan struct{})
	defer close(done)
	c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-done:
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Start(ctx, dial()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	bad := dial()
	bad.Destination = "invalid"
	if _, err := c.Start(context.Background(), bad); !errors.Is(err, providers.ErrInvalid) {
		t.Fatal(err)
	}
	if err := c.End(context.Background(), "not-a-sid"); !errors.Is(err, providers.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := c.Media(context.Background(), testCallSID, nil); !errors.Is(err, providers.ErrUnavailable) {
		t.Fatal("unimplemented media silently available")
	}
}

func TestRESTEndFixture(t *testing.T) {
	c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.URL.Path != "/2010-04-01/Accounts/"+testAccount+"/Calls/"+testCallSID+".json" || r.Form.Get("Status") != "completed" {
			t.Error("wrong end request")
		}
		_, _ = w.Write(fixture(t))
	})
	if err := c.End(context.Background(), testCallSID); err != nil {
		t.Fatal(err)
	}
}

func TestRESTEndUnknownSIDIsNotFound(t *testing.T) {
	c := localClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte("private unknown call body"))
	})
	if err := c.End(context.Background(), testCallSID); !errors.Is(err, providers.ErrNotFound) {
		t.Fatalf("unknown End=%v", err)
	}
}
