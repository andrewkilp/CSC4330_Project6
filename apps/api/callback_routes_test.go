package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
)

func callbackOnlyEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("ENABLE_REAL_CALLS", "false")
	t.Setenv("TWILIO_WEBHOOKS_ENABLED", "true")
	t.Setenv("TWILIO_ACCOUNT_SID", "AC11111111111111111111111111111111")
	t.Setenv("TWILIO_AUTH_TOKEN", "fixture-callback-token")
	t.Setenv("PUBLIC_BASE_URL", "https://callbacks.example.invalid")
	// Callback-only mode needs no dialing credentials or destination policy.
	t.Setenv("TWILIO_FROM_NUMBER", "")
	t.Setenv("CALL_ALLOWED_NUMBERS", "")
}

func signedAPICallbackRequest(t *testing.T, kind string) *http.Request {
	t.Helper()
	query := url.Values{"call_id": {"00000000-0000-4000-8000-000000000001"}, "attempt_id": {"00000000-0000-4000-8000-000000000002"}, "token": {base64.RawURLEncoding.EncodeToString(make([]byte, 32))}}
	form := url.Values{"AccountSid": {"AC11111111111111111111111111111111"}, "CallSid": {"CA22222222222222222222222222222222"}, "CallStatus": {"in-progress"}}
	path := "/webhooks/twilio/" + kind + "?" + query.Encode()
	mac := hmac.New(sha1.New, []byte("fixture-callback-token"))
	_, _ = mac.Write([]byte("https://callbacks.example.invalid" + path))
	keys := make([]string, 0, len(form))
	for key := range form {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		_, _ = mac.Write([]byte(key + form.Get(key)))
	}
	r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Twilio-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return r
}

func TestCallbacksDefaultDisabledAndIgnoreUnusedCredentials(t *testing.T) {
	t.Setenv("ENABLE_REAL_CALLS", "false")
	t.Setenv("TWILIO_WEBHOOKS_ENABLED", "")
	for _, name := range []string{"TWILIO_ACCOUNT_SID", "TWILIO_AUTH_TOKEN", "PUBLIC_BASE_URL"} {
		t.Setenv(name, "unused-invalid-secret")
	}
	h, err := configuredRouter("development", nil, "")
	if err != nil {
		t.Fatal("disabled callbacks validated unused credentials")
	}
	for _, kind := range []string{"voice", "status"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, signedAPICallbackRequest(t, kind))
		if w.Code != 404 {
			t.Fatalf("disabled callback route mounted: %s=%d", kind, w.Code)
		}
	}
}

func TestCallbackOnlyModeValidatesWithoutConstructingRESTOrDialing(t *testing.T) {
	callbackOnlyEnvironment(t)
	h, err := configuredRouter("development", nil, "")
	if err != nil {
		t.Fatal("callback-only mode required outbound credentials")
	}
	for _, kind := range []string{"voice", "status"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, signedAPICallbackRequest(t, kind))
		if w.Code != 503 {
			t.Fatalf("callback-only route missing or nil-store panic: %s=%d", kind, w.Code)
		}
		bad := signedAPICallbackRequest(t, kind)
		bad.Header.Set("X-Twilio-Signature", "unsigned")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, bad)
		if w.Code != 403 {
			t.Fatalf("unsigned callback reached store: %s=%d", kind, w.Code)
		}
	}
	start, err := startEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	callback, enabled, err := callbackEnvironment(start)
	if err != nil || !enabled || callback.MediaEnabled {
		t.Fatal("runtime claimed an available media bridge")
	}
}

func TestEnabledCallbackConfigurationFailsClosed(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"TWILIO_WEBHOOKS_ENABLED", "TRUE"}, {"TWILIO_WEBHOOKS_ENABLED", "1"}, {"TWILIO_WEBHOOKS_ENABLED", " true"},
		{"TWILIO_ACCOUNT_SID", "private-invalid-account"}, {"TWILIO_AUTH_TOKEN", ""}, {"PUBLIC_BASE_URL", "http://callbacks.example.invalid"}, {"PUBLIC_BASE_URL", "https://user:private-secret@callbacks.example.invalid"},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			callbackOnlyEnvironment(t)
			t.Setenv(tc.key, tc.value)
			if _, err := configuredRouter("development", nil, ""); err == nil || strings.Contains(err.Error(), "private-") {
				t.Fatalf("invalid callback startup accepted or leaked: %v", err)
			}
		})
	}
}

func TestOutboundCannotBeEnabledWithDisabledCallbacks(t *testing.T) {
	callbackOnlyEnvironment(t)
	t.Setenv("ENABLE_REAL_CALLS", "true")
	t.Setenv("TWILIO_WEBHOOKS_ENABLED", "false")
	t.Setenv("TWILIO_FROM_NUMBER", "+12255550101")
	t.Setenv("CALL_ALLOWED_NUMBERS", "+12255550100")
	if _, err := configuredRouter("development", nil, ""); err == nil {
		t.Fatal("dialing enabled with callback ingress disabled")
	}
}
