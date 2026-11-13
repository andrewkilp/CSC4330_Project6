package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const CookieName = "switchboard_session"
const Lifetime = 24 * time.Hour

type session struct {
	ownerID string
	expires time.Time
}

// Sessions stores local demo sessions for the lifetime of the API process.
type Sessions struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]session
}

func NewSessions() *Sessions {
	return &Sessions{entries: make(map[[sha256.Size]byte]session)}
}

func (s *Sessions) Create() (token, ownerID string) {
	var secret [32]byte
	rand.Read(secret[:])
	token = base64.RawURLEncoding.EncodeToString(secret[:])
	var id [16]byte
	rand.Read(id[:])
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	ownerID = fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, entry := range s.entries {
		if !now.Before(entry.expires) {
			delete(s.entries, key)
		}
	}
	s.entries[sha256.Sum256([]byte(token))] = session{ownerID: ownerID, expires: now.Add(Lifetime)}
	return token, ownerID
}

func (s *Sessions) Lookup(token string) (string, bool) {
	key := sha256.Sum256([]byte(token))
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	if !ok || !time.Now().Before(entry.expires) {
		delete(s.entries, key)
		return "", false
	}
	return entry.ownerID, true
}

func (s *Sessions) Revoke(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, sha256.Sum256([]byte(token)))
}

type ownerKey struct{}

func OwnerID(ctx context.Context) (string, bool) {
	owner, ok := ctx.Value(ownerKey{}).(string)
	return owner, ok
}

func (s *Sessions) RequireOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(CookieName)
		if err == nil {
			if owner, ok := s.Lookup(cookie.Value); ok {
				ctx := context.WithValue(r.Context(), ownerKey{}, owner)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthenticated","message":"A valid session is required."}}`))
	})
}
