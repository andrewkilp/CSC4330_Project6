package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionCookieEnvironment(t *testing.T) {
	for _, env := range []string{"development", "production"} {
		t.Run(env, func(t *testing.T) {
			router := newRouter(env)
			req := httptest.NewRequest(http.MethodPost, "/v1/session", strings.NewReader("{}"))
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			resp := recorder.Result()
			defer resp.Body.Close()
			cookies := resp.Cookies()
			if resp.StatusCode != http.StatusCreated || len(cookies) != 1 {
				t.Fatalf("session creation returned status %d and %d cookies", resp.StatusCode, len(cookies))
			}
			if cookies[0].Secure != (env == "production") {
				t.Errorf("Secure cookie flag = %t for %s", cookies[0].Secure, env)
			}
		})
	}
}
