package webhooks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/telephony/media"
)

type signedFixture struct {
	AuthToken            string `json:"auth_token"`
	URL, Body, Signature string
}

func fixture(t *testing.T, name string) signedFixture {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var f signedFixture
	if err = json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}
func config(t *testing.T) Config {
	f := fixture(t, "status")
	return Config{AccountSID: "AC" + strings.Repeat("a", 32), AuthToken: f.AuthToken, PublicBaseURL: "https://callbacks.example.invalid", MediaURL: "wss://media.example.invalid/webhooks/twilio/media"}
}

type ledgerFunc func(context.Context, repository.TwilioCallback) (repository.CallbackResult, error)

func (f ledgerFunc) ApplyTwilioCallback(ctx context.Context, c repository.TwilioCallback) (repository.CallbackResult, error) {
	return f(ctx, c)
}
func validResult(c repository.TwilioCallback) repository.CallbackResult {
	return repository.CallbackResult{Call: repository.Call{ID: c.CallID, OwnerID: "00000000-0000-4000-8000-000000000300", Mode: "real", Status: "dialing"}}
}
func handler(t *testing.T, cfg Config, ledger Ledger) http.Handler {
	t.Helper()
	h, err := New(cfg, ledger)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}
func request(t *testing.T, f signedFixture, path string) *http.Request {
	t.Helper()
	u, err := url.Parse(f.URL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = path
	r := httptest.NewRequest("POST", u.String(), strings.NewReader(f.Body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	form, err := url.ParseQuery(f.Body)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("X-Twilio-Signature", base64.StdEncoding.EncodeToString(expectedSignature(f.AuthToken, u.String(), form)))
	return r
}
func resign(t *testing.T, r *http.Request, cfg Config, body string) {
	t.Helper()
	form, err := url.ParseQuery(body)
	if err != nil {
		t.Fatal(err)
	}
	r.Body = io.NopCloser(strings.NewReader(body))
	r.ContentLength = int64(len(body))
	r.Header.Set("X-Twilio-Signature", base64.StdEncoding.EncodeToString(expectedSignature(cfg.AuthToken, cfg.PublicBaseURL+r.URL.EscapedPath()+"?"+r.URL.RawQuery, form)))
}

func TestTwilioPublishedSignatureVector(t *testing.T) {
	f := fixture(t, "twilio-published-vector")
	form, err := url.ParseQuery(f.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !validSignature(f.AuthToken, f.URL, f.URL, f.Signature, form) {
		t.Fatal("published Twilio signature vector rejected")
	}
	form.Set("Digits", "4321")
	if validSignature(f.AuthToken, f.URL, f.URL, f.Signature, form) {
		t.Fatal("tampered vector accepted")
	}
}

func TestIndependentSignedFixtureAndUnknownRepeatedValues(t *testing.T) {
	f := fixture(t, "status")
	cfg := config(t)
	calls := 0
	h := handler(t, cfg, ledgerFunc(func(ctx context.Context, c repository.TwilioCallback) (repository.CallbackResult, error) {
		calls++
		if c.Status != "in-progress" || c.AccountSID != cfg.AccountSID || c.CallSID != "CA"+strings.Repeat("b", 32) {
			t.Fatalf("wrong callback association: %+v", c)
		}
		return validResult(c), nil
	}))
	r := httptest.NewRequest("POST", f.URL, strings.NewReader(f.Body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Twilio-Signature", f.Signature)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || calls != 1 {
		t.Fatalf("status=%d calls=%d", w.Code, calls)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("callback response can be cached")
	}
}

func TestRejectsBeforeLedger(t *testing.T) {
	f := fixture(t, "status")
	cfg := config(t)
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
		code   int
	}{
		{"unsigned", func(r *http.Request) { r.Header.Del("X-Twilio-Signature") }, 403},
		{"malformed signature", func(r *http.Request) { r.Header.Set("X-Twilio-Signature", "not-base64") }, 403},
		{"duplicate signature", func(r *http.Request) { r.Header.Add("X-Twilio-Signature", f.Signature) }, 403},
		{"shared key algorithm", func(r *http.Request) { r.Header.Set("X-Twilio-Webhooks-Signature-Settings", "algorithm=HMAC_SHA256") }, 403},
		{"JSON", func(r *http.Request) { r.Header.Set("Content-Type", "application/json") }, 415},
		{"duplicate content type", func(r *http.Request) { r.Header.Add("Content-Type", "application/json") }, 415},
		{"compressed body", func(r *http.Request) { r.Header.Set("Content-Encoding", "gzip") }, 415},
		{"oversized body", func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", maxBody+1))) }, 413},
		{"bad percent encoding", func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader("CallSid=%ZZ")) }, 400},
		{"semicolon parameter", func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader("CallSid=x;AccountSid=y")) }, 400},
		{"invalid UTF8 value", func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader("Unknown=%FF")) }, 400},
		{"changed form", func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(strings.Replace(f.Body, "in-progress", "completed", 1)))
		}, 403},
		{"extra unsigned field", func(r *http.Request) { r.Body = io.NopCloser(strings.NewReader(f.Body + "&FutureField=three")) }, 403},
		{"changed raw query", func(r *http.Request) { r.URL.RawQuery += "&extra=1" }, 403},
		{"unknown signed query", func(r *http.Request) { r.URL.RawQuery += "&extra=1"; resign(t, r, cfg, f.Body) }, 400},
		{"duplicate call id", func(r *http.Request) {
			r.URL.RawQuery += "&call_id=00000000-0000-4000-8000-000000000999"
			resign(t, r, cfg, f.Body)
		}, 400},
		{"malformed attempt", func(r *http.Request) {
			q := r.URL.Query()
			q.Set("attempt_id", "not-uuid")
			r.URL.RawQuery = q.Encode()
			resign(t, r, cfg, f.Body)
		}, 400},
		{"short token", func(r *http.Request) {
			q := r.URL.Query()
			q.Set("token", "short")
			r.URL.RawQuery = q.Encode()
			resign(t, r, cfg, f.Body)
		}, 400},
		{"wrong account", func(r *http.Request) {
			resign(t, r, cfg, strings.Replace(f.Body, strings.Repeat("a", 32), strings.Repeat("c", 32), 1))
		}, 403},
		{"duplicate account", func(r *http.Request) { resign(t, r, cfg, f.Body+"&AccountSid="+cfg.AccountSID) }, 403},
		{"duplicate SID", func(r *http.Request) { resign(t, r, cfg, f.Body+"&CallSid=CA"+strings.Repeat("b", 32)) }, 403},
		{"malformed SID", func(r *http.Request) {
			resign(t, r, cfg, strings.Replace(f.Body, "CA"+strings.Repeat("b", 32), "CA-short", 1))
		}, 403},
		{"duplicate status", func(r *http.Request) { resign(t, r, cfg, f.Body+"&CallStatus=completed") }, 400},
		{"unsupported status", func(r *http.Request) { resign(t, r, cfg, strings.Replace(f.Body, "in-progress", "invented-state", 1)) }, 400},
		{"signature for attacker host", func(r *http.Request) {
			form, _ := url.ParseQuery(f.Body)
			r.Header.Set("X-Twilio-Signature", base64.StdEncoding.EncodeToString(expectedSignature(cfg.AuthToken, "https://attacker.example.invalid"+StatusPath+"?"+r.URL.RawQuery, form)))
			r.Host = "attacker.example.invalid"
			r.Header.Set("X-Forwarded-Host", r.Host)
		}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := handler(t, cfg, ledgerFunc(func(context.Context, repository.TwilioCallback) (repository.CallbackResult, error) {
				t.Fatal("invalid callback reached ledger")
				return repository.CallbackResult{}, nil
			}))
			r := request(t, f, StatusPath)
			tc.mutate(r)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.code {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), cfg.AuthToken) || strings.Contains(w.Body.String(), "CallSid") || strings.Contains(w.Body.String(), "token=") {
				t.Fatal("unsafe failure body")
			}
		})
	}
}

func TestHostHeadersCannotChangeValidConfiguredSignature(t *testing.T) {
	f := fixture(t, "status")
	cfg := config(t)
	calls := 0
	h := handler(t, cfg, ledgerFunc(func(ctx context.Context, c repository.TwilioCallback) (repository.CallbackResult, error) {
		calls++
		return validResult(c), nil
	}))
	r := request(t, f, StatusPath)
	r.Host = "hostile.example.invalid"
	r.Header.Set("X-Forwarded-Host", "other.example.invalid")
	r.Header.Set("X-Forwarded-Proto", "http")
	r.Header.Set("Forwarded", "host=other.example.invalid;proto=http")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || calls != 1 {
		t.Fatalf("status=%d calls=%d", w.Code, calls)
	}
}

func TestBodyRoutingFieldsCannotShadowSignedQuery(t *testing.T) {
	f := fixture(t, "status")
	cfg := config(t)
	h := handler(t, cfg, ledgerFunc(func(ctx context.Context, c repository.TwilioCallback) (repository.CallbackResult, error) {
		if c.CallID != "00000000-0000-4000-8000-000000000100" || c.AttemptID != "00000000-0000-4000-8000-000000000200" {
			t.Fatal("body shadowed route association")
		}
		return validResult(c), nil
	}))
	r := request(t, f, StatusPath)
	resign(t, r, cfg, f.Body+"&call_id=other&attempt_id=other&token=other&owner_id=other")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestVoiceTwiMLIsEscapedOwnedAndMediaGated(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, status := range []string{"dialing", "connected", "paused", "ended", "failed", "cancelled"} {
			t.Run(status+map[bool]string{false: "-disabled", true: "-enabled"}[enabled], func(t *testing.T) {
				f := fixture(t, "status")
				cfg := config(t)
				cfg.MediaEnabled = enabled
				cfg.MediaSecret = []byte(strings.Repeat("s", 32))
				cfg.MediaURL = "wss://media.example.invalid/webhooks/twilio/media&safe"
				var verified repository.TwilioCallback
				h := handler(t, cfg, ledgerFunc(func(ctx context.Context, c repository.TwilioCallback) (repository.CallbackResult, error) {
					verified = c
					if c.Status != "" {
						t.Fatal("voice invented status")
					}
					result := validResult(c)
					result.Call.Status = status
					return result, nil
				}))
				r := request(t, f, VoicePath)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != 200 || w.Header().Get("Content-Type") != "application/xml; charset=utf-8" {
					t.Fatalf("status=%d content-type=%s", w.Code, w.Header().Get("Content-Type"))
				}
				var xmlResult struct {
					XMLName xml.Name
					Connect *struct {
						Stream struct {
							URL        string `xml:"url,attr"`
							Parameters []struct {
								Name  string `xml:"name,attr"`
								Value string `xml:"value,attr"`
							} `xml:"Parameter"`
						} `xml:"Stream"`
					} `xml:"Connect"`
					Hangup *struct{} `xml:"Hangup"`
				}
				if err := xml.Unmarshal(w.Body.Bytes(), &xmlResult); err != nil {
					t.Fatal(err)
				}
				stream := enabled && (status == "dialing" || status == "connected" || status == "paused")
				if !stream {
					if xmlResult.Hangup == nil || xmlResult.Connect != nil {
						t.Fatal("terminal/disabled voice did not hang up safely")
					}
					return
				}
				if xmlResult.Connect == nil || xmlResult.Hangup != nil || xmlResult.Connect.Stream.URL != cfg.MediaURL || !strings.Contains(w.Body.String(), "media&amp;safe") {
					t.Fatal("wrong or unescaped stream URL")
				}
				params := map[string]string{}
				for _, p := range xmlResult.Connect.Stream.Parameters {
					params[p.Name] = p.Value
				}
				if len(params) != 3 || params["call_id"] != r.URL.Query().Get("call_id") || params["attempt_id"] != r.URL.Query().Get("attempt_id") ||
					params["token"] != media.SignStream(cfg.MediaSecret, verified.CallID, verified.CallSID) {
					t.Fatal("wrong verified stream parameters")
				}
				// The webhook callback credential never leaves the server in TwiML.
				if strings.Contains(w.Body.String(), r.URL.Query().Get("token")) {
					t.Fatal("callback token leaked into TwiML")
				}
				// The TwiML token is exactly what the #21 bridge accepts for this call.
				auth := media.TokenAuthorizer{Secret: cfg.MediaSecret, ProviderCallID: func(_ context.Context, id string) (string, error) {
					if id != verified.CallID {
						return "", repository.ErrNotFound
					}
					return verified.CallSID, nil
				}}
				start := media.Start{CallSID: verified.CallSID, CustomParameters: params}
				if got, err := auth.Authorize(context.Background(), start); err != nil || got != verified.CallID {
					t.Fatalf("bridge rejected TwiML stream credential: %v", err)
				}
				start.CallSID = "CA" + strings.Repeat("0", 32)
				if _, err := auth.Authorize(context.Background(), start); err == nil {
					t.Fatal("bridge accepted the TwiML token for another provider call")
				}
			})
		}
	}
}

func TestLedgerFailuresAreSafeAndNeverPublish(t *testing.T) {
	f := fixture(t, "status")
	for _, err := range []error{repository.ErrNotFound, repository.ErrConflict, repository.ErrUnavailable, errors.New("private database credential")} {
		cfg := config(t)
		cfg.Publish = func(context.Context, []repository.Event) { t.Fatal("published failed transaction") }
		h := handler(t, cfg, ledgerFunc(func(context.Context, repository.TwilioCallback) (repository.CallbackResult, error) {
			return repository.CallbackResult{}, err
		}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request(t, f, StatusPath))
		want := 503
		if errors.Is(err, repository.ErrNotFound) || errors.Is(err, repository.ErrConflict) {
			want = 404
		}
		if w.Code != want || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
}

func TestPublishRunsOnlyForCommittedNewEvents(t *testing.T) {
	f := fixture(t, "status")
	cfg := config(t)
	var mu sync.Mutex
	committed, published, calls := false, 0, 0
	cfg.Publish = func(ctx context.Context, events []repository.Event) {
		mu.Lock()
		defer mu.Unlock()
		if !committed || len(events) != 1 || events[0].Type != "call.updated" {
			t.Fatal("published before committed result")
		}
		published++
	}
	h := handler(t, cfg, ledgerFunc(func(ctx context.Context, c repository.TwilioCallback) (repository.CallbackResult, error) {
		mu.Lock()
		defer mu.Unlock()
		result := validResult(c)
		calls++
		if calls == 1 {
			result.Changed = true
			result.Events = []repository.Event{{CallID: c.CallID, Type: "call.updated", Seq: 2}}
		}
		committed = true
		return result, nil
	}))
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request(t, f, StatusPath))
		if w.Code != 204 {
			t.Fatal(w.Code)
		}
	}
	if calls != 2 || published != 1 {
		t.Fatalf("ledger=%d publish=%d", calls, published)
	}
}

func TestRoutesArePOSTOnly(t *testing.T) {
	h := handler(t, config(t), ledgerFunc(func(context.Context, repository.TwilioCallback) (repository.CallbackResult, error) {
		t.Fatal("non-POST reached ledger")
		return repository.CallbackResult{}, nil
	}))
	for _, method := range []string{"GET", "PUT", "OPTIONS"} {
		for _, path := range []string{VoicePath, StatusPath} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			if w.Code != 405 {
				t.Fatalf("%s %s=%d", method, path, w.Code)
			}
		}
	}
}

func TestConfigurationAndConfiguredPortVariants(t *testing.T) {
	store := ledgerFunc(func(ctx context.Context, c repository.TwilioCallback) (repository.CallbackResult, error) {
		return validResult(c), nil
	})
	for _, mutate := range []func(*Config){func(c *Config) { c.AuthToken = "" }, func(c *Config) { c.AccountSID = "AC-short" }, func(c *Config) { c.PublicBaseURL = "http://callbacks.example.invalid" }, func(c *Config) { c.PublicBaseURL = "https://user:secret@callbacks.example.invalid" }, func(c *Config) { c.PublicBaseURL += "/prefix" }, func(c *Config) { c.PublicBaseURL += "?query=1" }, func(c *Config) { c.MediaURL = "https://media.example.invalid" }, func(c *Config) { c.MediaURL += "?token=secret" }, func(c *Config) { c.MediaEnabled, c.MediaSecret = true, []byte("short") }} {
		cfg := config(t)
		mutate(&cfg)
		if _, err := New(cfg, store); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	if _, err := New(config(t), nil); err == nil {
		t.Fatal("nil ledger accepted")
	}
	f := fixture(t, "status")
	cfg := config(t)
	cfg.PublicBaseURL += ":8443"
	h := handler(t, cfg, store)
	// Twilio's HTTPS voice signature may omit the configured port. Both
	// candidates originate entirely from trusted configuration.
	r := request(t, f, StatusPath)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("configured no-port variant rejected: %d", w.Code)
	}
}
