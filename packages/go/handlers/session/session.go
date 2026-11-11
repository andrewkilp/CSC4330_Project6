package session

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/auth"
)

func Register(mux *http.ServeMux, sessions *auth.Sessions, secure bool) {
	cookie := func(value string, age int) *http.Cookie {
		return &http.Cookie{
			Name: auth.CookieName, Value: value, Path: "/",
			HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: age,
		}
	}
	mux.HandleFunc("POST /v1/session", func(w http.ResponseWriter, r *http.Request) {
		if !emptyObject(w, r, false) {
			return
		}
		if existing, err := r.Cookie(auth.CookieName); err == nil {
			sessions.Revoke(existing.Value)
		}
		token, owner := sessions.Create()
		http.SetCookie(w, cookie(token, int(auth.Lifetime/time.Second)))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"account_id": owner})
	})
	mux.HandleFunc("DELETE /v1/session", func(w http.ResponseWriter, r *http.Request) {
		if !emptyObject(w, r, true) {
			return
		}
		if existing, err := r.Cookie(auth.CookieName); err == nil {
			sessions.Revoke(existing.Value)
		}
		http.SetCookie(w, cookie("", -1))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})
}

func emptyObject(w http.ResponseWriter, r *http.Request, allowEmpty bool) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	var body map[string]json.RawMessage
	err := decoder.Decode(&body)
	if allowEmpty && err == io.EOF {
		return true
	}
	if err == nil && body != nil && len(body) == 0 && decoder.Decode(new(json.RawMessage)) == io.EOF {
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(`{"error":{"code":"invalid_argument","message":"Expected an empty JSON object."}}`))
	return false
}
