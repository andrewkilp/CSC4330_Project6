package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

type faultCase struct {
	Name       string `json:"name"`
	Deliveries []struct {
		Status    string `json:"status"`
		Ledger    string `json:"ledger"`
		Signature string `json:"signature"`
		Want      int    `json:"want"`
	} `json:"deliveries"`
	Final          string   `json:"final"`
	CallEvents     int      `json:"call_events"`
	ProviderErrors []string `json:"provider_errors"`
}

// memLedger mirrors ApplyTwilioCallback: monotonic status via repository.TwilioStatus,
// one call.updated event per real change, and a durable provider.error log.
type memLedger struct {
	mu         sync.Mutex
	status     string
	callEvents int
	errors     []string
	failNext   bool
	calls      int
}

func (l *memLedger) ApplyTwilioCallback(_ context.Context, c repository.TwilioCallback) (repository.CallbackResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	if l.failNext {
		l.failNext = false
		return repository.CallbackResult{}, repository.ErrUnavailable
	}
	result := validResult(c)
	next := repository.TwilioStatus(l.status, c.Status)
	if next != l.status {
		l.status = next
		l.callEvents++
		result.Changed = true
		result.Events = []repository.Event{{CallID: c.CallID, Seq: int64(l.callEvents), Type: "call.updated"}}
	}
	result.Call.Status = l.status
	return result, nil
}

func (l *memLedger) RecordProviderFailure(_ context.Context, callID string, f repository.ProviderFailure) ([]repository.Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, f.Code)
	return []repository.Event{{CallID: callID, Type: "provider.error"}}, nil
}

func TestFaultInjectionFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/faults.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Cases []faultCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures.Cases) < 8 {
		t.Fatalf("only %d fault cases", len(fixtures.Cases))
	}
	f := fixture(t, "status")
	for _, tc := range fixtures.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			ledger := &memLedger{status: "dialing"}
			cfg := config(t)
			var published []repository.Event
			cfg.Publish = func(_ context.Context, e []repository.Event) { published = append(published, e...) }
			cfg.Failures = ledger
			h := handler(t, cfg, ledger)
			terminalSince := -1
			for i, d := range tc.Deliveries {
				form, _ := url.ParseQuery(f.Body)
				form.Set("CallStatus", d.Status)
				r := request(t, f, StatusPath)
				resign(t, r, cfg, form.Encode())
				if d.Signature == "forged" {
					r.Header.Set("X-Twilio-Signature", "Zm9yZ2Vk")
				}
				ledger.failNext = d.Ledger == "unavailable"
				before := ledger.calls
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				want := d.Want
				if want == 0 {
					want = 204
				}
				if w.Code != want {
					t.Fatalf("delivery %d (%s): HTTP %d, want %d", i, d.Status, w.Code, want)
				}
				if d.Signature == "forged" && ledger.calls != before {
					t.Fatalf("delivery %d: forged signature reached storage", i)
				}
				// Once terminal, the status never changes again.
				if terminalSince >= 0 && ledger.status != tc.Final {
					t.Fatalf("terminal status changed to %s at delivery %d", ledger.status, i)
				}
				if terminalSince < 0 && (ledger.status == "ended" || ledger.status == "failed" || ledger.status == "cancelled") {
					terminalSince = i
				}
			}
			if ledger.status != tc.Final || ledger.callEvents != tc.CallEvents {
				t.Fatalf("final %s with %d call events, want %s with %d", ledger.status, ledger.callEvents, tc.Final, tc.CallEvents)
			}
			if !slices.Equal(ledger.errors, tc.ProviderErrors) && !(len(ledger.errors) == 0 && len(tc.ProviderErrors) == 0) {
				t.Fatalf("provider errors %v, want %v", ledger.errors, tc.ProviderErrors)
			}
			if len(published) != tc.CallEvents+len(tc.ProviderErrors) {
				t.Fatalf("published %d events, want %d", len(published), tc.CallEvents+len(tc.ProviderErrors))
			}
		})
	}
}

// Logs carry a correlation id and the call id, never provider internals or secrets.
func TestCallbackLogsAreRedactedAndCorrelated(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(previous)

	f := fixture(t, "status")
	ledger := &memLedger{status: "dialing"}
	cfg := config(t)
	cfg.Failures = ledger
	h := handler(t, cfg, ledger)
	form, _ := url.ParseQuery(f.Body)
	form.Set("CallStatus", "busy")
	for _, forged := range []bool{false, true} {
		r := request(t, f, StatusPath)
		resign(t, r, cfg, form.Encode())
		r.Header.Set("I-Twilio-Idempotency-Token", "0123abcd-retry-7")
		if forged {
			r.Header.Set("X-Twilio-Signature", "Zm9yZ2Vk")
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	logs := buf.String()
	if strings.Count(logs, `"correlation":"0123abcd-retry-7"`) < 2 {
		t.Fatalf("correlation id missing from logs:\n%s", logs)
	}
	query, _ := url.Parse(f.URL)
	for _, secret := range []string{cfg.AuthToken, form.Get("CallSid"), form.Get("AccountSid"), form.Get("From"), form.Get("To"), query.Query().Get("token"), "Zm9yZ2Vk"} {
		if secret != "" && strings.Contains(logs, secret) {
			t.Fatalf("log leaked %q:\n%s", secret, logs)
		}
	}
	// An attacker-controlled header cannot inject log content.
	buf.Reset()
	r := request(t, f, StatusPath)
	r.Header.Set("I-Twilio-Idempotency-Token", `x" injected="1`)
	r.Header.Set("X-Twilio-Signature", "Zm9yZ2Vk")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if strings.Contains(buf.String(), "injected") {
		t.Fatal("unvalidated correlation header was logged")
	}
}
