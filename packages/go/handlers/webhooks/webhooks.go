// Package webhooks handles provider-authenticated Twilio voice/status callbacks.
// It never trusts a browser owner id, request Host or reverse-proxy headers.
package webhooks

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/telephony/media"
)

const (
	VoicePath  = "/webhooks/twilio/voice"
	StatusPath = "/webhooks/twilio/status"
	maxBody    = 16 * 1024
	maxQuery   = 2048
)

var (
	uuid       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	accountSID = regexp.MustCompile(`^AC[0-9a-fA-F]{32}$`)
	callSID    = regexp.MustCompile(`^CA[0-9a-fA-F]{32}$`)
	fieldName  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,99}$`)
)

// Ledger must validate attempt token/account/mode/SID and return only after its
// association, monotonic status and durable call.updated event transaction commits.
type Ledger interface {
	ApplyTwilioCallback(context.Context, repository.TwilioCallback) (repository.CallbackResult, error)
}

type Config struct {
	AccountSID string
	AuthToken  string
	// PublicBaseURL is the exact configured HTTPS origin used by outbound start.
	// It must not contain a path, query, userinfo or fragment.
	PublicBaseURL string
	// MediaURL is the server-owned WSS endpoint, with no query or fragment.
	MediaURL string
	// MediaEnabled connects verified active calls to the #21 media bridge. When false,
	// a verified voice request receives safe Hangup TwiML.
	MediaEnabled bool
	// MediaSecret (at least 32 bytes, MEDIA_STREAM_SECRET) signs the per-call stream
	// token with media.SignStream. It is required when MediaEnabled is true. The
	// callback token is never placed in TwiML.
	MediaSecret []byte
	// Publish is an optional post-commit fanout hint, not the durable event store.
	// Implementations must return promptly; durable replay belongs to #23.
	Publish func(context.Context, []repository.Event)
}

type Handler struct {
	config         Config
	ledger         Ledger
	origin, noPort string
}

func New(config Config, ledger Ledger) (*Handler, error) {
	u, err := url.Parse(config.PublicBaseURL)
	m, mediaErr := url.Parse(config.MediaURL)
	if ledger == nil || !accountSID.MatchString(config.AccountSID) || strings.TrimSpace(config.AuthToken) == "" || len(config.AuthToken) > 1024 || strings.ContainsAny(config.AuthToken, "\r\n\x00") ||
		err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Opaque != "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		mediaErr != nil || m.Scheme != "wss" || m.Hostname() == "" || m.Opaque != "" || m.User != nil || m.RawQuery != "" || m.ForceQuery || m.Fragment != "" {
		return nil, errors.New("Twilio callbacks require configured account credentials, HTTPS origin, WSS media URL and durable ledger")
	}
	if config.MediaEnabled && len(config.MediaSecret) < 32 {
		return nil, errors.New("Twilio media streams require a stream secret of at least 32 bytes")
	}
	config.MediaURL = m.String()
	return &Handler{config: config, ledger: ledger, origin: strings.TrimSuffix(config.PublicBaseURL, "/"), noPort: noPortOrigin(u)}, nil
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST "+VoicePath, h.voice)
	mux.HandleFunc("POST "+StatusPath, h.status)
}

func (h *Handler) verified(w http.ResponseWriter, r *http.Request, path string, needStatus bool) (repository.TwilioCallback, bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	bad := func(status int) (repository.TwilioCallback, bool) {
		http.Error(w, "Callback rejected.", status)
		return repository.TwilioCallback{}, false
	}
	if r.URL.EscapedPath() != path || len(r.URL.RawQuery) > maxQuery {
		return bad(http.StatusBadRequest)
	}
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if len(r.Header.Values("Content-Type")) != 1 || err != nil || media != "application/x-www-form-urlencoded" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) || len(r.Header.Values("Content-Encoding")) != 0 {
		return bad(http.StatusUnsupportedMediaType)
	}
	// Only the configured primary account Auth Token algorithm is supported.
	// Do not silently interpret SharedKey settings with a different secret.
	if len(r.Header.Values("X-Twilio-Signature")) != 1 || len(r.Header.Values("X-Twilio-Webhooks-Signature-Settings")) != 0 {
		return bad(http.StatusForbidden)
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		var oversized *http.MaxBytesError
		if errors.As(err, &oversized) {
			return bad(http.StatusRequestEntityTooLarge)
		}
		return bad(http.StatusBadRequest)
	}
	if !utf8.Valid(body) {
		return bad(http.StatusBadRequest)
	}
	form, err := url.ParseQuery(string(body))
	if err != nil || len(form) > 128 {
		return bad(http.StatusBadRequest)
	}
	for key, values := range form {
		if !fieldName.MatchString(key) || len(values) > 16 {
			return bad(http.StatusBadRequest)
		}
		for _, value := range values {
			if !utf8.ValidString(value) || len(value) > 4096 || strings.ContainsRune(value, 0) {
				return bad(http.StatusBadRequest)
			}
		}
	}
	// Never use r.Form: it merges query and body and can shadow signed identities.
	suffix := path + "?" + r.URL.RawQuery
	if !validSignature(h.config.AuthToken, h.origin+suffix, h.noPort+suffix, r.Header.Get("X-Twilio-Signature"), form) {
		return bad(http.StatusForbidden)
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) != 3 || len(query["call_id"]) != 1 || len(query["attempt_id"]) != 1 || len(query["token"]) != 1 || !uuid.MatchString(query.Get("call_id")) || !uuid.MatchString(query.Get("attempt_id")) {
		return bad(http.StatusBadRequest)
	}
	token, err := base64.RawURLEncoding.Strict().DecodeString(query.Get("token"))
	if err != nil || len(token) != 32 {
		return bad(http.StatusBadRequest)
	}
	if len(form["AccountSid"]) != 1 || form.Get("AccountSid") != h.config.AccountSID || len(form["CallSid"]) != 1 || !callSID.MatchString(form.Get("CallSid")) {
		return bad(http.StatusForbidden)
	}
	if len(form["CallStatus"]) > 1 {
		return bad(http.StatusBadRequest)
	}
	status := form.Get("CallStatus")
	if needStatus && !knownStatus(status) {
		return bad(http.StatusBadRequest)
	}
	if !needStatus {
		status = ""
	}
	return repository.TwilioCallback{CallID: query.Get("call_id"), AttemptID: query.Get("attempt_id"), Token: query.Get("token"), AccountSID: form.Get("AccountSid"), CallSID: form.Get("CallSid"), Status: status}, true
}

func knownStatus(s string) bool {
	switch s {
	case "queued", "initiated", "ringing", "in-progress", "completed", "busy", "failed", "no-answer", "canceled":
		return true
	}
	return false
}

func (h *Handler) apply(w http.ResponseWriter, r *http.Request, c repository.TwilioCallback) (repository.CallbackResult, bool) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	result, err := h.ledger.ApplyTwilioCallback(ctx, c)
	if err == nil && (result.Call.ID != c.CallID || result.Call.Mode != "real" || !uuid.MatchString(result.Call.OwnerID)) {
		err = repository.ErrUnavailable
	}
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, repository.ErrNotFound) || errors.Is(err, repository.ErrConflict) {
			status = http.StatusNotFound
		}
		http.Error(w, "Callback could not be accepted.", status)
		return repository.CallbackResult{}, false
	}
	if h.config.Publish != nil && len(result.Events) > 0 {
		h.config.Publish(r.Context(), append([]repository.Event(nil), result.Events...))
	}
	return result, true
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	c, ok := h.verified(w, r, StatusPath, true)
	if !ok {
		return
	}
	if _, ok = h.apply(w, r, c); !ok {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type parameter struct {
	XMLName xml.Name `xml:"Parameter"`
	Name    string   `xml:"name,attr"`
	Value   string   `xml:"value,attr"`
}
type stream struct {
	XMLName    xml.Name    `xml:"Stream"`
	URL        string      `xml:"url,attr"`
	Parameters []parameter `xml:"Parameter"`
}
type connect struct {
	XMLName xml.Name `xml:"Connect"`
	Stream  stream
}
type twiml struct {
	XMLName xml.Name  `xml:"Response"`
	Connect *connect  `xml:"Connect,omitempty"`
	Hangup  *struct{} `xml:"Hangup,omitempty"`
}

func (h *Handler) voice(w http.ResponseWriter, r *http.Request) {
	c, ok := h.verified(w, r, VoicePath, false)
	if !ok {
		return
	}
	result, ok := h.apply(w, r, c)
	if !ok {
		return
	}
	response := twiml{Hangup: &struct{}{}}
	if h.config.MediaEnabled && (result.Call.Status == "dialing" || result.Call.Status == "connected" || result.Call.Status == "paused") {
		response = twiml{Connect: &connect{Stream: stream{URL: h.config.MediaURL, Parameters: []parameter{{Name: "call_id", Value: c.CallID}, {Name: "attempt_id", Value: c.AttemptID},
			// The stream credential binds our call id to the verified provider SID (#21).
			{Name: "token", Value: media.SignStream(h.config.MediaSecret, c.CallID, c.CallSID)}}}}}
	}
	data, err := xml.Marshal(response)
	if err != nil {
		http.Error(w, "Callback could not be completed.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write(append([]byte(xml.Header), data...))
}
