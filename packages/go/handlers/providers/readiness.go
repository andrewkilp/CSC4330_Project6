// Package providers serves GET /v1/providers: secret-free availability of the four
// real-call providers and whether the server offers real or mock calling. It reads
// configuration presence only. It never returns, logs or compares key values, never
// calls a vendor and never switches an in-progress call to another mode.
package providers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Kind is one provider in docs/contracts.md.
type Kind string

const (
	Telephony Kind = "telephony"
	STT       Kind = "stt"
	LLM       Kind = "llm"
	TTS       Kind = "tts"
)

var names = map[Kind]string{Telephony: "Phone calling", STT: "Speech recognition", LLM: "Reply generation", TTS: "Speech playback"}

// ErrRealUnavailable rejects a real-call start when any provider is unavailable or
// real calling is disabled. The start route (#11) maps it to 503 provider_unavailable;
// the operator may explicitly create a new mock call instead.
var ErrRealUnavailable = errors.New("real calling is unavailable")

// Implemented says which real adapters this server build can compose. It is set by
// the composition root, not by configuration.
type Implemented struct {
	Telephony, STT, LLM, TTS bool
}

// Config is presence-only provider configuration.
type Config struct {
	// RealCallsEnabled is ENABLE_REAL_CALLS=true. Real calling also needs
	// CALL_MODE=real.
	RealCallsEnabled bool
	RealMode         bool
	Implemented      Implemented
	Configured       Implemented
	// OutageCooldown is how long a reported outage marks a provider unavailable.
	// Zero means one minute.
	OutageCooldown time.Duration
}

// required lists the server-only settings each provider needs. Only presence is read.
var required = map[Kind][]string{
	Telephony: {"TWILIO_ACCOUNT_SID", "TWILIO_AUTH_TOKEN", "TWILIO_FROM_NUMBER", "PUBLIC_BASE_URL"},
	STT:       {"DEEPGRAM_API_KEY"},
	LLM:       {"ANTHROPIC_API_KEY", "CLAUDE_MODEL"},
	TTS:       {"ELEVENLABS_API_KEY", "ELEVENLABS_VOICE_ID", "ELEVENLABS_MODEL_ID"},
}

// FromEnv reads which settings are present. lookup is usually os.LookupEnv.
// PUBLIC_BASE_URL counts only when it is an https URL, which provider callbacks need.
func FromEnv(lookup func(string) (string, bool), implemented Implemented) Config {
	present := func(kind Kind) bool {
		for _, key := range required[kind] {
			v, ok := lookup(key)
			v = strings.TrimSpace(v)
			if !ok || v == "" || (key == "PUBLIC_BASE_URL" && !strings.HasPrefix(v, "https://")) {
				return false
			}
		}
		return true
	}
	enabled, _ := lookup("ENABLE_REAL_CALLS")
	mode, _ := lookup("CALL_MODE")
	return Config{
		RealCallsEnabled: strings.TrimSpace(enabled) == "true",
		RealMode:         strings.TrimSpace(mode) == "real",
		Implemented:      implemented,
		Configured:       Implemented{Telephony: present(Telephony), STT: present(STT), LLM: present(LLM), TTS: present(TTS)},
	}
}

// Availability matches ProviderAvailability in packages/ts/api/src/types.ts.
type Availability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Status matches Providers in packages/ts/api/src/types.ts.
type Status struct {
	Mode      string       `json:"mode"`
	Telephony Availability `json:"telephony"`
	STT       Availability `json:"stt"`
	LLM       Availability `json:"llm"`
	TTS       Availability `json:"tts"`
}

// Readiness combines configuration with outages reported by provider adapters.
type Readiness struct {
	cfg Config
	now func() time.Time

	mu      sync.Mutex
	outages map[Kind]time.Time // until
}

// New builds readiness. A nil now is time.Now.
func New(cfg Config, now func() time.Time) *Readiness {
	if cfg.OutageCooldown <= 0 {
		cfg.OutageCooldown = time.Minute
	}
	if now == nil {
		now = time.Now
	}
	return &Readiness{cfg: cfg, now: now, outages: map[Kind]time.Time{}}
}

// ReportOutage marks a provider unavailable for the cooldown, for example after an
// adapter returns its user-safe unavailable error. Unknown kinds are ignored.
func (r *Readiness) ReportOutage(kind Kind) {
	if _, ok := names[kind]; !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outages[kind] = r.now().Add(r.cfg.OutageCooldown)
}

// ReportRecovered clears a reported outage after a successful provider operation.
func (r *Readiness) ReportRecovered(kind Kind) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.outages, kind)
}

func pick(i Implemented, kind Kind) bool {
	switch kind {
	case Telephony:
		return i.Telephony
	case STT:
		return i.STT
	case LLM:
		return i.LLM
	}
	return i.TTS
}

func (r *Readiness) availability(kind Kind, now time.Time) Availability {
	name := names[kind]
	switch {
	case !pick(r.cfg.Implemented, kind):
		return Availability{Reason: name + " is not available in this server version yet."}
	case kind == Telephony && !r.cfg.RealCallsEnabled:
		return Availability{Reason: "Real calling is turned off on this server."}
	case !pick(r.cfg.Configured, kind):
		return Availability{Reason: name + " is not set up on this server."}
	}
	r.mu.Lock()
	until, down := r.outages[kind]
	if down && !now.Before(until) {
		delete(r.outages, kind)
		down = false
	}
	r.mu.Unlock()
	if down {
		return Availability{Reason: name + " is temporarily unavailable. Try again later."}
	}
	return Availability{Available: true}
}

// Status reports current availability. Mode is "real" only when real calling is
// enabled, CALL_MODE is real and all four providers are available; otherwise "mock".
func (r *Readiness) Status() Status {
	now := r.now()
	s := Status{
		Telephony: r.availability(Telephony, now), STT: r.availability(STT, now),
		LLM: r.availability(LLM, now), TTS: r.availability(TTS, now),
	}
	s.Mode = "mock"
	if r.cfg.RealCallsEnabled && r.cfg.RealMode && s.Telephony.Available && s.STT.Available && s.LLM.Available && s.TTS.Available {
		s.Mode = "real"
	}
	return s
}

// RequireReal is the gate a real-call start must pass. It never selects mock mode
// on the caller's behalf.
func (r *Readiness) RequireReal() error {
	if r.Status().Mode != "real" {
		return ErrRealUnavailable
	}
	return nil
}

// Register mounts GET /v1/providers. The response contains booleans and fixed
// user-safe reasons only.
func Register(mux *http.ServeMux, r *Readiness) {
	mux.HandleFunc("GET /v1/providers", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(r.Status())
	})
}
