// Package responses implements the existing response approval route with injected
// authenticated identity and call lookup. It is not mounted by the template API.
package responses

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/conversation"
)

var ErrNotFound = errors.New("call not found")

// OwnedCall must come from trusted server storage, never the request body.
type OwnedCall struct {
	ID, OwnerID string
	Loop        *conversation.Loop
}
type Config struct {
	// OwnerID reads identity from authenticated middleware context, e.g. auth.OwnerID.
	OwnerID func(context.Context) (string, bool)
	Lookup  func(context.Context, string) (OwnedCall, error)
	// Origin is a configured web origin; request Host/forwarded headers are not trusted.
	Origin string
}

func New(config Config) (http.Handler, error) {
	u, err := url.Parse(config.Origin)
	if config.OwnerID == nil || config.Lookup == nil || err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("approval handler requires identity, call storage and an explicit origin")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/calls/{id}/responses/{response_id}/approve", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		owner, ok := config.OwnerID(r.Context())
		if !ok || owner == "" {
			fail(w, 401, "unauthenticated", "A valid session is required.", "")
			return
		}
		if (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != config.Origin) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			fail(w, 400, "invalid_argument", "The request origin is not allowed.", "origin")
			return
		}
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			fail(w, 400, "invalid_argument", "Expected a JSON request.", "request")
			return
		}
		call, err := config.Lookup(r.Context(), r.PathValue("id"))
		if errors.Is(err, ErrNotFound) || (err == nil && (call.OwnerID != owner || call.ID != r.PathValue("id"))) {
			fail(w, 404, "not_found", "Call not found.", "")
			return
		}
		if err != nil || call.Loop == nil || call.Loop.CallID() != call.ID {
			fail(w, 500, "internal", "The request could not be completed.", "")
			return
		}
		a, field := decode(w, r)
		if field != "" {
			fail(w, 400, "invalid_argument", "Invalid approval request.", field)
			return
		}
		a.ResponseID = r.PathValue("response_id")
		reply, err := call.Loop.ApproveResponse(r.Context(), a)
		if err != nil {
			var approval *conversation.ApprovalError
			switch {
			case errors.As(err, &approval):
				if errors.Is(err, conversation.ErrInvalidInput) {
					fail(w, 400, "invalid_argument", "Invalid approval request.", approval.Field)
					return
				}
				message := "This response cannot be approved in its current state."
				if approval.Field == "context_revision" {
					message = "This suggestion is out of date. Review the newest suggestion before approving."
				}
				if approval.Field == "idempotency_key" {
					message = "This idempotency key was used for a different request."
				}
				fail(w, 409, "conflict", message, approval.Field)
			case errors.Is(err, conversation.ErrResponseNotFound):
				fail(w, 404, "not_found", "Response not found.", "")
			case errors.Is(err, conversation.ErrApprovalLimit):
				fail(w, 429, "rate_limited", "This call has reached its approval limit.", "")
			default:
				fail(w, 500, "internal", "The request could not be completed.", "")
			}
			return
		}
		_ = json.NewEncoder(w).Encode(reply)
	})
	return mux, nil
}

var fieldName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,79}$`)

func decode(w http.ResponseWriter, r *http.Request) (conversation.Approval, string) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16*1024))
	if err != nil || !utf8.Valid(data) {
		return conversation.Approval{}, "request"
	}
	d := json.NewDecoder(bytes.NewReader(data))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return conversation.Approval{}, "request"
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return conversation.Approval{}, "request"
		}
		key, ok := token.(string)
		if !ok {
			return conversation.Approval{}, "request"
		}
		if key != "context_revision" && key != "edited_text" && key != "idempotency_key" {
			if !fieldName.MatchString(key) {
				key = "request"
			}
			return conversation.Approval{}, key
		}
		if _, duplicate := fields[key]; duplicate {
			return conversation.Approval{}, key
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return conversation.Approval{}, key
		}
		fields[key] = raw
	}
	if _, err := d.Token(); err != nil {
		return conversation.Approval{}, "request"
	}
	if d.Decode(new(json.RawMessage)) != io.EOF {
		return conversation.Approval{}, "request"
	}
	var a conversation.Approval
	if len(fields["context_revision"]) == 0 || string(fields["context_revision"]) == "null" || json.Unmarshal(fields["context_revision"], &a.ContextRevision) != nil {
		return a, "context_revision"
	}
	if len(fields["idempotency_key"]) == 0 || string(fields["idempotency_key"]) == "null" || json.Unmarshal(fields["idempotency_key"], &a.IdempotencyKey) != nil {
		return a, "idempotency_key"
	}
	if raw, present := fields["edited_text"]; present {
		var s string
		if strings.TrimSpace(string(raw)) == "null" || json.Unmarshal(raw, &s) != nil {
			return a, "edited_text"
		}
		a.EditedText = &s
	}
	return a, ""
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

func fail(w http.ResponseWriter, status int, code, message, field string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error errorDetail `json:"error"`
	}{errorDetail{code, message, field}})
}
