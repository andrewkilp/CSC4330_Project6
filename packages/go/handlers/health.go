package handlers

import (
	"encoding/json"
	"net/http"
)

// NewRouter exposes only readiness. Provider routes arrive through planned issues.
func NewRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "mode": "template"})
	})
	return mux
}
