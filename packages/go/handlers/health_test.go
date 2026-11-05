package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadiness(t *testing.T) {
	w := httptest.NewRecorder()
	NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || body["status"] != "ok" || body["mode"] != "template" {
		t.Fatalf("unexpected response: %d %v", w.Code, body)
	}
}

func TestTemplateDoesNotExposeCallCreation(t *testing.T) {
	w := httptest.NewRecorder()
	NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/calls", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("template must not initiate calls: %d", w.Code)
	}
}
