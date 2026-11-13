package calls

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/auth"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

type CallStarter interface {
	Start(context.Context, string, string, string) (repository.Call, error)
}
type StartHandler struct {
	service  CallStarter
	sessions *auth.Sessions
}

func NewStartHandler(service CallStarter, sessions *auth.Sessions) *StartHandler {
	return &StartHandler{service: service, sessions: sessions}
}
func RegisterStart(mux *http.ServeMux, h *StartHandler) {
	mux.Handle("POST /v1/calls/{id}/start", h.sessions.RequireOwner(http.HandlerFunc(h.start)))
}

var startUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func startError(w http.ResponseWriter, status int, code, message, field string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	err := map[string]string{"code": code, "message": message}
	if field != "" {
		err["field"] = field
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"error": err})
}
func (h *StartHandler) start(w http.ResponseWriter, r *http.Request) {
	owner, ok := auth.OwnerID(r.Context())
	if !ok {
		startError(w, 401, "unauthenticated", "A valid session is required.", "")
		return
	}
	if !startUUID.MatchString(r.PathValue("id")) {
		startError(w, 400, "invalid_argument", "Call id must be a UUID.", "id")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		startError(w, 400, "invalid_argument", "Expected a JSON request.", "")
		return
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	raw, field, err := startObject(d)
	if err != nil {
		startError(w, 400, "invalid_argument", "Expected a bounded JSON object without duplicate fields.", field)
		return
	}
	for key := range raw {
		if key != "idempotency_key" {
			startError(w, 400, "invalid_argument", "Unknown request field.", key)
			return
		}
	}
	var key string
	if json.Unmarshal(raw["idempotency_key"], &key) != nil || !startUUID.MatchString(key) {
		startError(w, 400, "invalid_argument", "An idempotency UUID is required.", "idempotency_key")
		return
	}
	if h.service == nil {
		startError(w, 503, "provider_unavailable", "Call starting is not configured.", "")
		return
	}
	call, err := h.service.Start(r.Context(), owner, r.PathValue("id"), key)
	switch {
	case err == nil:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(call)
	case errors.Is(err, repository.ErrNotFound):
		startError(w, 404, "not_found", "Call not found.", "")
	case errors.Is(err, repository.ErrInvalid):
		startError(w, 400, "invalid_argument", "This call cannot be started with these settings.", "")
	case errors.Is(err, repository.ErrConflict):
		startError(w, 409, "conflict", "This call has already been started or needs reconciliation. It will not be dialed again.", "status")
	default:
		startError(w, 503, "provider_unavailable", "Call start could not be confirmed. It will not be retried automatically.", "")
	}
}

func startObject(d *json.Decoder) (map[string]json.RawMessage, string, error) {
	token, err := d.Token()
	if err != nil {
		return nil, "", err
	}
	if token != json.Delim('{') {
		return nil, "", io.ErrUnexpectedEOF
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return nil, "", err
		}
		key, ok := token.(string)
		if !ok {
			return nil, "", io.ErrUnexpectedEOF
		}
		if _, exists := out[key]; exists {
			return nil, key, io.ErrUnexpectedEOF
		}
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return nil, key, err
		}
		out[key] = value
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return nil, "", io.ErrUnexpectedEOF
	}
	if d.Decode(new(json.RawMessage)) != io.EOF {
		return nil, "", io.ErrUnexpectedEOF
	}
	return out, "", nil
}
