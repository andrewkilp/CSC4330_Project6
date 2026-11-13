package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionCookieEnvironment(t *testing.T) {
	t.Setenv("ENABLE_REAL_CALLS", "false")
	for _, env := range []string{"development", "production"} {
		t.Run(env, func(t *testing.T) {
			router := newRouter(env)
			health := httptest.NewRecorder()
			router.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
			if health.Code != http.StatusOK || strings.TrimSpace(health.Body.String()) != `{"mode":"template","status":"ok"}` {
				t.Fatalf("session router lost health route: %d %s", health.Code, health.Body)
			}
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

func TestProvidersRouteIsSecretFreeAndMock(t *testing.T) {
	t.Setenv("CALL_MODE", "real")
	t.Setenv("ENABLE_REAL_CALLS", "false")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-SENTINEL")
	t.Setenv("CLAUDE_MODEL", "claude-test-model")
	recorder := httptest.NewRecorder()
	newRouter("development").ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/providers", nil))
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || strings.Contains(body, "SENTINEL") || !strings.Contains(body, `"mode":"mock"`) ||
		!strings.Contains(body, `"llm":{"available":true}`) || !strings.Contains(body, `"telephony":{"available":false`) {
		t.Fatalf("providers route: %d %s", recorder.Code, body)
	}
}
