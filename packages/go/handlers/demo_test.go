package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/demo"
)

func TestDemoRoutesRequireLocalHostAndOrigin(t *testing.T) {
	router := NewDemoRouter(t.TempDir(), demo.New(0))
	for _, tc := range []struct {
		name, host, origin string
		want               int
	}{
		{"same origin", "127.0.0.1:8080", "http://127.0.0.1:8080", 200},
		{"foreign origin", "127.0.0.1:8080", "https://example.com", 403},
		{"foreign host", "example.com", "", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://"+tc.host+"/demo/sessions", strings.NewReader(`{"scenario":"restaurant"}`))
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d", w.Code, tc.want)
			}
		})
	}
}
func TestDemoRejectsMalformedOrRealCallInput(t *testing.T) {
	router := NewDemoRouter(t.TempDir(), demo.New(0))
	for _, body := range []string{`{"scenario":"real"}`, `{"scenario":"restaurant","to":"+12025550123"}`, `{"scenario":"restaurant"} {}`, strings.Repeat("x", 5000)} {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/demo/sessions", strings.NewReader(body))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("malformed demo request returned %d", w.Code)
		}
	}
}
