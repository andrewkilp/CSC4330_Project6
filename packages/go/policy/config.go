package policy

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidConfig reports a limit or rate outside its documented range. The message
// names the setting, never its value.
var ErrInvalidConfig = errors.New("invalid call policy configuration")

const (
	// ContractMaxCallDuration is the frozen real-call cap in docs/contracts.md.
	ContractMaxCallDuration = 180 * time.Second
	// ContractMaxConcurrentCalls is the frozen concurrent real-call cap.
	ContractMaxConcurrentCalls = 1

	defaultEndMargin     = 5 * time.Second
	defaultUsageInterval = 5 * time.Second
	maxProviderTimeout   = 60 * time.Second
	maxRateUSD           = 1000.0
)

// Provider names one of the four provider kinds in docs/contracts.md.
type Provider string

const (
	Telephony Provider = "telephony"
	STT       Provider = "stt"
	LLM       Provider = "llm"
	TTS       Provider = "tts"
)

// Timeouts bound one provider operation each (dial, recognition setup, one
// generation, one synthesis). Adapters keep their own internal timeouts; these are the
// call-wide outer bound. Each is 1 ms..60 s.
type Timeouts struct {
	Telephony time.Duration
	STT       time.Duration
	LLM       time.Duration
	TTS       time.Duration
}

// Rates are dated USD list prices from docs/provider-matrix.md, supplied by server
// configuration. Nothing here is a bill: estimates are labelled estimate_only.
// A zero rate contributes nothing to the estimate.
type Rates struct {
	// CheckedOn is the YYYY-MM-DD date the rates were read from the vendor pages.
	// Required when any rate is nonzero.
	CheckedOn          string
	TelephonyPerMinute float64 // outbound voice, billed per started minute
	MediaPerMinute     float64 // media streams, billed per started minute
	STTPerMinute       float64 // streaming recognition, prorated per second
	LLMInputPerMTok    float64
	LLMOutputPerMTok   float64
	TTSPer1KChars      float64
}

// Config holds the server's call limits.
type Config struct {
	// MaxCallDuration is measured from Acquire (the explicit start action), so ringing
	// counts. 1 s..180 s; zero means 180 s.
	MaxCallDuration time.Duration
	// EndMargin is how long before MaxCallDuration the hang-up begins, so the call is
	// over before the limit rather than at it. Zero means min(5 s, MaxCallDuration/10).
	EndMargin time.Duration
	// MaxConcurrentRealCalls must be 1 (zero means 1). The API is a single instance
	// with one real-call permit.
	MaxConcurrentRealCalls int
	// UsageInterval is how often usage.updated is published while connected.
	// 1 s..60 s; zero means 5 s.
	UsageInterval time.Duration
	Timeouts      Timeouts
	Rates         Rates
}

// DefaultTimeouts match the adapters' own defaults (Claude and ElevenLabs use 30 s).
func DefaultTimeouts() Timeouts {
	return Timeouts{Telephony: 10 * time.Second, STT: 10 * time.Second, LLM: 30 * time.Second, TTS: 30 * time.Second}
}

func (c Config) withDefaults() Config {
	if c.MaxCallDuration == 0 {
		c.MaxCallDuration = ContractMaxCallDuration
	}
	if c.EndMargin == 0 {
		c.EndMargin = min(defaultEndMargin, c.MaxCallDuration/10)
	}
	if c.MaxConcurrentRealCalls == 0 {
		c.MaxConcurrentRealCalls = ContractMaxConcurrentCalls
	}
	if c.UsageInterval == 0 {
		c.UsageInterval = defaultUsageInterval
	}
	d := DefaultTimeouts()
	if c.Timeouts.Telephony == 0 {
		c.Timeouts.Telephony = d.Telephony
	}
	if c.Timeouts.STT == 0 {
		c.Timeouts.STT = d.STT
	}
	if c.Timeouts.LLM == 0 {
		c.Timeouts.LLM = d.LLM
	}
	if c.Timeouts.TTS == 0 {
		c.Timeouts.TTS = d.TTS
	}
	return c
}

func invalid(setting string) error { return fmt.Errorf("%w: %s", ErrInvalidConfig, setting) }

// Validate checks a configuration after defaults are applied.
func (c Config) Validate() error {
	if c.MaxCallDuration < time.Second || c.MaxCallDuration > ContractMaxCallDuration {
		return invalid("max call duration")
	}
	if c.EndMargin < 0 || c.EndMargin >= c.MaxCallDuration {
		return invalid("end margin")
	}
	if c.MaxConcurrentRealCalls != ContractMaxConcurrentCalls {
		return invalid("max concurrent calls")
	}
	if c.UsageInterval < time.Second || c.UsageInterval > time.Minute {
		return invalid("usage interval")
	}
	for name, d := range map[string]time.Duration{"telephony timeout": c.Timeouts.Telephony, "stt timeout": c.Timeouts.STT,
		"llm timeout": c.Timeouts.LLM, "tts timeout": c.Timeouts.TTS} {
		if d < time.Millisecond || d > maxProviderTimeout {
			return invalid(name)
		}
	}
	return c.Rates.Validate()
}

func (r Rates) Validate() error {
	priced := false
	for name, v := range map[string]float64{"telephony rate": r.TelephonyPerMinute, "media rate": r.MediaPerMinute,
		"stt rate": r.STTPerMinute, "llm input rate": r.LLMInputPerMTok, "llm output rate": r.LLMOutputPerMTok, "tts rate": r.TTSPer1KChars} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > maxRateUSD {
			return invalid(name)
		}
		priced = priced || v > 0
	}
	if r.CheckedOn != "" {
		if _, err := time.Parse(time.DateOnly, r.CheckedOn); err != nil {
			return invalid("rates checked date")
		}
	} else if priced {
		return invalid("rates checked date")
	}
	return nil
}

// FromEnv reads limits from server environment variables (see .env.example and
// docs/costs.md). Unset variables keep their defaults. lookup is usually os.LookupEnv.
func FromEnv(lookup func(string) (string, bool)) (Config, error) {
	var c Config
	var err error
	seconds := func(key, setting string, dst *time.Duration) {
		if v, ok := lookup(key); ok && strings.TrimSpace(v) != "" && err == nil {
			n, e := strconv.Atoi(strings.TrimSpace(v))
			if e != nil || n <= 0 {
				err = invalid(setting)
				return
			}
			*dst = time.Duration(n) * time.Second
		}
	}
	millis := func(key, setting string, dst *time.Duration) {
		if v, ok := lookup(key); ok && strings.TrimSpace(v) != "" && err == nil {
			n, e := strconv.Atoi(strings.TrimSpace(v))
			if e != nil || n <= 0 {
				err = invalid(setting)
				return
			}
			*dst = time.Duration(n) * time.Millisecond
		}
	}
	rate := func(key, setting string, dst *float64) {
		if v, ok := lookup(key); ok && strings.TrimSpace(v) != "" && err == nil {
			f, e := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if e != nil {
				err = invalid(setting)
				return
			}
			*dst = f
		}
	}
	seconds("MAX_CALL_SECONDS", "max call duration", &c.MaxCallDuration)
	if v, ok := lookup("MAX_CONCURRENT_CALLS"); ok && strings.TrimSpace(v) != "" && err == nil {
		n, e := strconv.Atoi(strings.TrimSpace(v))
		if e != nil || n <= 0 {
			err = invalid("max concurrent calls")
		}
		c.MaxConcurrentRealCalls = n
	}
	millis("TELEPHONY_TIMEOUT_MS", "telephony timeout", &c.Timeouts.Telephony)
	millis("STT_TIMEOUT_MS", "stt timeout", &c.Timeouts.STT)
	millis("LLM_TIMEOUT_MS", "llm timeout", &c.Timeouts.LLM)
	millis("TTS_TIMEOUT_MS", "tts timeout", &c.Timeouts.TTS)
	if v, ok := lookup("USAGE_RATES_CHECKED_ON"); ok {
		c.Rates.CheckedOn = strings.TrimSpace(v)
	}
	rate("USAGE_RATE_TELEPHONY_USD_PER_MIN", "telephony rate", &c.Rates.TelephonyPerMinute)
	rate("USAGE_RATE_MEDIA_USD_PER_MIN", "media rate", &c.Rates.MediaPerMinute)
	rate("USAGE_RATE_STT_USD_PER_MIN", "stt rate", &c.Rates.STTPerMinute)
	rate("USAGE_RATE_LLM_INPUT_USD_PER_MTOK", "llm input rate", &c.Rates.LLMInputPerMTok)
	rate("USAGE_RATE_LLM_OUTPUT_USD_PER_MTOK", "llm output rate", &c.Rates.LLMOutputPerMTok)
	rate("USAGE_RATE_TTS_USD_PER_1K_CHARS", "tts rate", &c.Rates.TTSPer1KChars)
	if err != nil {
		return Config{}, err
	}
	c = c.withDefaults()
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}
