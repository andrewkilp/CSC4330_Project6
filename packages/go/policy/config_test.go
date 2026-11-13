package policy_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/policy"
)

func env(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := values[key]
		return v, ok
	}
}

func TestFromEnvDefaultsWithoutRates(t *testing.T) {
	c, err := policy.FromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxCallDuration != 180*time.Second || c.MaxConcurrentRealCalls != 1 || c.Rates != (policy.Rates{}) {
		t.Fatalf("config = %+v", c)
	}
}

func TestFromEnvReadsEnvExample(t *testing.T) {
	c, err := policy.FromEnv(env(map[string]string{
		"MAX_CALL_SECONDS": "120", "MAX_CONCURRENT_CALLS": "1", "TELEPHONY_TIMEOUT_MS": "8000",
		"STT_TIMEOUT_MS": "9000", "LLM_TIMEOUT_MS": "25000", "TTS_TIMEOUT_MS": "20000",
		"USAGE_RATES_CHECKED_ON": "2026-11-10", "USAGE_RATE_TELEPHONY_USD_PER_MIN": "0.014",
		"USAGE_RATE_MEDIA_USD_PER_MIN": "0.0044", "USAGE_RATE_STT_USD_PER_MIN": "0.0077",
		"USAGE_RATE_LLM_INPUT_USD_PER_MTOK": "2", "USAGE_RATE_LLM_OUTPUT_USD_PER_MTOK": "10",
		"USAGE_RATE_TTS_USD_PER_1K_CHARS": "0.04",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxCallDuration != 120*time.Second || c.Timeouts.Telephony != 8*time.Second || c.Timeouts.TTS != 20*time.Second ||
		c.Rates != matrixRates {
		t.Fatalf("config = %+v", c)
	}
}

func TestFromEnvRejectsOutOfRangeSettings(t *testing.T) {
	cases := []struct {
		name, key, value, setting string
	}{
		{"above contract duration", "MAX_CALL_SECONDS", "181", "max call duration"},
		{"zero duration", "MAX_CALL_SECONDS", "0", "max call duration"},
		{"malformed duration", "MAX_CALL_SECONDS", "3m", "max call duration"},
		{"second concurrent call", "MAX_CONCURRENT_CALLS", "2", "max concurrent calls"},
		{"timeout above 60 s", "LLM_TIMEOUT_MS", "60001", "llm timeout"},
		{"negative rate", "USAGE_RATE_TTS_USD_PER_1K_CHARS", "-0.04", "tts rate"},
		{"not a number", "USAGE_RATE_STT_USD_PER_MIN", "NaN", "stt rate"},
		{"undated rate", "USAGE_RATE_TELEPHONY_USD_PER_MIN", "0.014", "rates checked date"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := policy.FromEnv(env(map[string]string{tc.key: tc.value}))
			if !errors.Is(err, policy.ErrInvalidConfig) || !strings.Contains(err.Error(), tc.setting) {
				t.Fatalf("err = %v, want invalid %q", err, tc.setting)
			}
			if strings.Contains(err.Error(), tc.value) {
				t.Fatalf("error echoes the configured value: %v", err)
			}
		})
	}
	if _, err := policy.FromEnv(env(map[string]string{"USAGE_RATES_CHECKED_ON": "Nov 10"})); !errors.Is(err, policy.ErrInvalidConfig) {
		t.Fatalf("malformed date = %v", err)
	}
	if _, err := policy.New(policy.Config{EndMargin: 180 * time.Second}, nil); !errors.Is(err, policy.ErrInvalidConfig) {
		t.Fatalf("margin equal to limit = %v", err)
	}
}

func TestEstimateRounding(t *testing.T) {
	if got := policy.Estimate(matrixRates, policy.Meter{}, 0); got != 0 {
		t.Fatalf("zero call = %v", got)
	}
	// 61 s is two started minutes of voice and media.
	if got := policy.Estimate(policy.Rates{CheckedOn: "2026-11-10", TelephonyPerMinute: 0.014}, policy.Meter{}, 61*time.Second); got != 0.028 {
		t.Fatalf("61 s = %v", got)
	}
	if got := policy.Estimate(matrixRates, policy.Meter{LLMInputTokens: -5}, -time.Second); got != 0 {
		t.Fatalf("negative inputs = %v", got)
	}
}

// The documented three-minute sample in docs/costs.md. The call is hung up 5 s before
// the 180 s limit; volumes are the provider matrix's rounded-up mock-scenario figures.
func ExampleEstimate() {
	connected := 175 * time.Second
	meter := policy.Meter{LLMInputTokens: 50000, LLMOutputTokens: 3000, TTSCharacters: 1500}
	usage := policy.Usage{
		ElapsedSeconds:   int64(connected / time.Second),
		EstimatedCostUSD: policy.Estimate(matrixRates, meter, connected),
		EstimateOnly:     true,
	}
	data, _ := json.Marshal(usage)
	fmt.Println(string(data))
	// Output: {"elapsed_seconds":175,"estimated_cost_usd":0.2677,"estimate_only":true}
}
