package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfiguredOriginProtectsSessionAndDraftRoutes(t *testing.T) {
	t.Setenv("ENABLE_REAL_CALLS", "false")
	h, err := configuredRouter("development", nil, "http://localhost:8081")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		origin, method string
		want           int
	}{{"https://evil.invalid", "POST", 400}, {"http://localhost:8081", "OPTIONS", 204}, {"http://localhost:8081", "POST", 201}, {"", "POST", 201}} {
		r := httptest.NewRequest(tc.method, "/v1/session", strings.NewReader("{}"))
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Access-Control-Request-Method", "POST")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s %s: %d %s", tc.origin, tc.method, w.Code, w.Body)
		}
		if tc.origin == "https://evil.invalid" && w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("foreign origin allowed")
		}
		if tc.origin == "http://localhost:8081" && (w.Header().Get("Access-Control-Allow-Origin") != tc.origin || w.Header().Get("Access-Control-Allow-Credentials") != "true") {
			t.Fatal("configured browser origin missing")
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/session", strings.NewReader("{}"))
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("cross-site mutation allowed")
	}
	if _, err := configuredRouter("production", nil, "http://localhost:8081"); err == nil {
		t.Fatal("production plaintext origin accepted")
	}
	if _, err := configuredRouter("development", nil, "https://example.invalid/path"); err == nil {
		t.Fatal("non-origin config accepted")
	}
}
