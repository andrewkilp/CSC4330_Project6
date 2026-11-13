package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/auth"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/calls"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/session"
)

func newRouter(environment string) http.Handler {
	h, _ := configuredRouter(environment, nil, "")
	return h
}

func configuredRouter(environment string, store calls.DraftStore, origin string) (http.Handler, error) {
	if origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") || (u.Scheme == "http" && environment != "" && environment != "development") {
			return nil, errors.New("invalid app origin")
		}
		origin = u.Scheme + "://" + u.Host
	}
	mux := http.NewServeMux()
	mux.Handle("/", handlers.NewRouter())
	sessions := auth.NewSessions()
	session.Register(mux, sessions, environment != "" && environment != "development")
	calls.RegisterDrafts(mux, calls.NewDraftHandler(store, sessions))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		incoming := r.Header.Get("Origin")
		w.Header().Add("Vary", "Origin")
		if (incoming != "" && (origin == "" || incoming != origin)) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "invalid_argument", "message": "The request origin is not allowed.", "field": "origin"}})
			return
		}
		if incoming != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions && incoming != "" {
			if method := r.Header.Get("Access-Control-Request-Method"); method != "GET" && method != "POST" && method != "DELETE" {
				w.WriteHeader(400)
				return
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(204)
			return
		}
		mux.ServeHTTP(w, r)
	}), nil
}
