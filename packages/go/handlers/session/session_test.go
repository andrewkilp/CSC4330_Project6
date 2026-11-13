package session_test

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/auth"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/session"
)

func TestSessionLifecycle(t *testing.T) {
	store := auth.NewSessions()
	mux := http.NewServeMux()
	session.Register(mux, store, true)
	mux.Handle("GET /owner", store.RequireOwner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		owner, _ := auth.OwnerID(r.Context())
		_ = json.NewEncoder(w).Encode(owner)
	})))
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	client := server.Client()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client.Jar = jar

	request := func(method, path, body string, want int, cookie *http.Cookie) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		if resp.StatusCode != want {
			t.Fatalf("%s %s returned %d, want %d", method, path, resp.StatusCode, want)
		}
		return resp
	}

	request("GET", "/owner", "", http.StatusUnauthorized, nil)
	request("POST", "/v1/session", `{"owner_id":"claimed"}`, http.StatusBadRequest, nil)
	const maxBodyBytes = 16 * 1024
	maxBody := "{}" + strings.Repeat(" ", maxBodyBytes-2)
	request("POST", "/v1/session", maxBody+" ", http.StatusBadRequest, nil)
	created := request("POST", "/v1/session", maxBody, http.StatusCreated, nil)
	var account struct {
		ID string `json:"account_id"`
	}
	if err := json.NewDecoder(created.Body).Decode(&account); err != nil || len(account.ID) != 36 {
		t.Fatalf("invalid generated account: %+v, %v", account, err)
	}
	cookies := created.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d session cookies, want one", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != auth.CookieName || !cookie.Secure || !cookie.HttpOnly ||
		cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" ||
		cookie.Domain != "" || cookie.Value == account.ID || cookie.MaxAge != 86400 {
		t.Fatal("unexpected session cookie attributes")
	}
	for range 2 {
		resp := request("GET", "/owner", "", http.StatusOK, nil)
		var owner string
		if err := json.NewDecoder(resp.Body).Decode(&owner); err != nil || owner != account.ID {
			t.Fatalf("owner = %q, want %q, decode error %v", owner, account.ID, err)
		}
	}
	request("DELETE", "/v1/session", maxBody+" ", http.StatusBadRequest, nil)
	logout := request("DELETE", "/v1/session", maxBody, http.StatusNoContent, nil)
	if cleared := logout.Cookies(); len(cleared) != 1 || cleared[0].MaxAge >= 0 ||
		!cleared[0].Secure || !cleared[0].HttpOnly || cleared[0].Path != "/" {
		t.Fatal("logout did not clear the session cookie")
	}
	request("GET", "/owner", "", http.StatusUnauthorized, nil)
	request("GET", "/owner", "", http.StatusUnauthorized, cookie)
}
