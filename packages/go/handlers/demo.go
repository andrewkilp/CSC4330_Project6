package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/demo"
)

// NewDemoRouter is only mounted by explicit DEMO_MODE on a loopback listener.
func NewDemoRouter(webDir string, service *demo.Service) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /health", NewRouter())
	write := func(w http.ResponseWriter, data []byte, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, demo.ErrNotFound) {
				status = 404
			} else if errors.Is(err, demo.ErrConflict) {
				status = 409
			} else if errors.Is(err, demo.ErrCapacity) {
				status = 429
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": err.Error()}})
			return
		}
		_, _ = w.Write(data)
	}
	decode := func(w http.ResponseWriter, r *http.Request, value any) bool {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(value) != nil || d.Decode(new(any)) != io.EOF {
			write(w, nil, demo.ErrInput)
			return false
		}
		return true
	}
	mux.HandleFunc("POST /demo/sessions", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Scenario string `json:"scenario"`
		}
		if !decode(w, r, &input) {
			return
		}
		b, err := service.Start(input.Scenario)
		write(w, b, err)
	})
	mux.HandleFunc("GET /demo/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		after, err := strconv.Atoi(r.URL.Query().Get("after_seq"))
		if err != nil {
			write(w, nil, demo.ErrInput)
			return
		}
		b, err := service.Read(r.PathValue("id"), after)
		write(w, b, err)
	})
	mux.HandleFunc("POST /demo/sessions/{id}/actions", func(w http.ResponseWriter, r *http.Request) {
		var input demo.Action
		if !decode(w, r, &input) {
			return
		}
		b, err := service.Act(r.PathValue("id"), input)
		write(w, b, err)
	})
	mux.Handle("GET /", http.FileServer(http.Dir(webDir)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			http.Error(w, "Local demo only", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host {
				http.Error(w, "Use the demo from its local address", http.StatusForbidden)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
