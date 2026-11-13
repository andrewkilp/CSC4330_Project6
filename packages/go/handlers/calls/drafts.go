// Package calls exposes owner-scoped call operations. Creating a draft never dials.
package calls

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/auth"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

type DraftStore interface {
	Create(context.Context, string, string, repository.Draft) (repository.Call, error)
	GetCall(context.Context, string, string) (repository.Call, error)
}
type DraftHandler struct {
	store    DraftStore
	sessions *auth.Sessions
}

func NewDraftHandler(store DraftStore, sessions *auth.Sessions) *DraftHandler {
	return &DraftHandler{store, sessions}
}

func RegisterDrafts(mux *http.ServeMux, h *DraftHandler) {
	wrap := func(fn http.HandlerFunc) http.Handler {
		if h == nil || h.sessions == nil {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				draftError(w, 503, "provider_unavailable", "Call storage is unavailable.", "")
			})
		}
		return h.sessions.RequireOwner(fn)
	}
	mux.Handle("POST /v1/calls", wrap(h.create))
	mux.Handle("GET /v1/calls/{id}", wrap(h.get))
}

func (h *DraftHandler) create(w http.ResponseWriter, r *http.Request) {
	owner, _ := auth.OwnerID(r.Context())
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		draftError(w, 400, "invalid_argument", "Expected a JSON request.", "request")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16*1024))
	if err != nil || !utf8.Valid(data) {
		draftError(w, 400, "invalid_argument", "Invalid call request.", "request")
		return
	}
	draft, key, field := decodeDraft(data)
	if field != "" {
		draftError(w, 400, "invalid_argument", "Check the call details.", field)
		return
	}
	if h.store == nil {
		draftError(w, 503, "provider_unavailable", "Call storage is unavailable.", "")
		return
	}
	call, err := h.store.Create(r.Context(), owner, key, draft)
	if err != nil {
		draftStoreError(w, err)
		return
	}
	draftReply(w, 201, call)
}
func (h *DraftHandler) get(w http.ResponseWriter, r *http.Request) {
	owner, _ := auth.OwnerID(r.Context())
	id := strings.ToLower(r.PathValue("id"))
	if !draftUUID.MatchString(id) {
		draftError(w, 404, "not_found", "Call not found.", "")
		return
	}
	if h.store == nil {
		draftError(w, 503, "provider_unavailable", "Call storage is unavailable.", "")
		return
	}
	call, err := h.store.GetCall(r.Context(), owner, id)
	if err != nil {
		draftStoreError(w, err)
		return
	}
	draftReply(w, 200, call)
}
func draftStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		draftError(w, 404, "not_found", "Call not found.", "")
	case errors.Is(err, repository.ErrConflict):
		draftError(w, 409, "conflict", "This idempotency key was used for different call details.", "idempotency_key")
	case errors.Is(err, repository.ErrInvalid):
		draftError(w, 400, "invalid_argument", "Check the call details.", "request")
	case errors.Is(err, repository.ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		draftError(w, 503, "provider_unavailable", "Call storage is unavailable.", "")
	default:
		draftError(w, 500, "internal", "The request could not be completed.", "")
	}
}
func draftReply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func draftError(w http.ResponseWriter, status int, code, message, field string) {
	type detail struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Field   string `json:"field,omitempty"`
	}
	draftReply(w, status, struct {
		Error detail `json:"error"`
	}{detail{code, message, field}})
}

var draftUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var draftPhone = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)
var draftFictional = regexp.MustCompile(`^\+1[2-9][0-9]{2}55501[0-9]{2}$`)
var draftField = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,79}$`)
var draftDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-](?:0\d|1[0-4]):[0-5]\d)$`)

// Strict objects reject unknown and duplicate members instead of allowing JSON's
// last-value-wins behavior to alter a reviewed request.
func draftObject(data []byte, prefix string, allowed ...string) (map[string]json.RawMessage, string) {
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, prefix
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, prefix
		}
		key, ok := token.(string)
		if !ok {
			return nil, prefix
		}
		field := key
		if prefix != "request" {
			field = prefix + "." + key
		}
		if !draftField.MatchString(key) {
			return nil, prefix
		}
		known := false
		for _, a := range allowed {
			if a == key {
				known = true
				break
			}
		}
		if !known {
			return nil, field
		}
		if _, dup := out[key]; dup {
			return nil, field
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return nil, field
		}
		out[key] = raw
	}
	if _, err := d.Token(); err != nil {
		return nil, prefix
	}
	if d.Decode(new(json.RawMessage)) != io.EOF {
		return nil, prefix
	}
	return out, ""
}
func draftText(v json.RawMessage, max int) (string, bool) {
	var s string
	if json.Unmarshal(v, &s) != nil || bytes.Equal(bytes.TrimSpace(v), []byte("null")) || !utf8.ValidString(s) || strings.TrimSpace(s) == "" || len(utf16.Encode([]rune(s))) > max {
		return "", false
	}
	return s, true
}
func decodeDraft(data []byte) (repository.Draft, string, string) {
	var draft repository.Draft
	f, field := draftObject(data, "request", "mode", "destination", "business_name", "goal", "approval_mode", "consent_acknowledged", "idempotency_key")
	if field != "" {
		return draft, "", field
	}
	var ok bool
	if draft.Mode, ok = draftText(f["mode"], 4); !ok || (draft.Mode != "mock" && draft.Mode != "real") {
		return draft, "", "mode"
	}
	if draft.Destination, ok = draftText(f["destination"], 16); !ok || !draftPhone.MatchString(draft.Destination) || (draft.Mode == "mock" && !draftFictional.MatchString(draft.Destination)) {
		return draft, "", "destination"
	}
	if draft.BusinessName, ok = draftText(f["business_name"], 120); !ok {
		return draft, "", "business_name"
	}
	if draft.ApprovalMode, ok = draftText(f["approval_mode"], 9); !ok || (draft.ApprovalMode != "always" && draft.ApprovalMode != "uncertain") {
		return draft, "", "approval_mode"
	}
	if !bytes.Equal(bytes.TrimSpace(f["consent_acknowledged"]), []byte("true")) {
		return draft, "", "consent_acknowledged"
	}
	draft.ConsentAcknowledged = true
	key, ok := draftText(f["idempotency_key"], 36)
	if !ok || !draftUUID.MatchString(key) {
		return draft, "", "idempotency_key"
	}
	g, field := draftObject(f["goal"], "goal", "kind", "preferred_at", "timezone", "party_size", "service", "constraints", "contact_name", "contact_phone")
	if field != "" {
		return draft, "", field
	}
	if draft.Goal.Kind, ok = draftText(g["kind"], 11); !ok || (draft.Goal.Kind != "restaurant" && draft.Goal.Kind != "appointment") {
		return draft, "", "goal.kind"
	}
	if draft.Goal.PreferredAt, ok = draftText(g["preferred_at"], 100); !ok || !draftDate.MatchString(draft.Goal.PreferredAt) {
		return draft, "", "goal.preferred_at"
	}
	at, err := time.Parse(time.RFC3339, draft.Goal.PreferredAt)
	if err != nil {
		return draft, "", "goal.preferred_at"
	}
	if draft.Goal.Timezone, ok = draftText(g["timezone"], 100); !ok || draft.Goal.Timezone == "Local" {
		return draft, "", "goal.timezone"
	}
	zone, err := time.LoadLocation(draft.Goal.Timezone)
	if err != nil {
		return draft, "", "goal.timezone"
	}
	_, offset := at.Zone()
	_, want := at.In(zone).Zone()
	if offset != want {
		return draft, "", "goal.preferred_at"
	}
	if draft.Goal.ContactName, ok = draftText(g["contact_name"], 120); !ok {
		return draft, "", "goal.contact_name"
	}
	if raw, present := g["contact_phone"]; present {
		if draft.Goal.ContactPhone, ok = draftText(raw, 16); !ok || !draftPhone.MatchString(draft.Goal.ContactPhone) {
			return draft, "", "goal.contact_phone"
		}
	}
	var constraints []json.RawMessage
	if raw := g["constraints"]; len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &constraints) != nil || len(constraints) > 12 {
		return draft, "", "goal.constraints"
	}
	draft.Goal.Constraints = []string{}
	for _, raw := range constraints {
		s, ok := draftText(raw, 200)
		if !ok {
			return draft, "", "goal.constraints"
		}
		draft.Goal.Constraints = append(draft.Goal.Constraints, s)
	}
	if draft.Goal.Kind == "restaurant" {
		if _, present := g["service"]; present {
			return draft, "", "goal.service"
		}
		var size int
		if json.Unmarshal(g["party_size"], &size) != nil || size < 1 || size > 20 {
			return draft, "", "goal.party_size"
		}
		draft.Goal.PartySize = &size
	} else {
		if _, present := g["party_size"]; present {
			return draft, "", "goal.party_size"
		}
		if draft.Goal.Service, ok = draftText(g["service"], 160); !ok {
			return draft, "", "goal.service"
		}
	}
	return draft, strings.ToLower(key), ""
}
