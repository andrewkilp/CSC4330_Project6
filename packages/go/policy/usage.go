package policy

import (
	"math"
	"time"
)

// Usage is the usage.updated payload in docs/contracts.md. It is always an estimate;
// provider dashboards are the billing authority.
type Usage struct {
	ElapsedSeconds   int64   `json:"elapsed_seconds"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
	EstimateOnly     bool    `json:"estimate_only"`
}

// Meter counts the usage the estimate needs. Callers record what providers report or
// what was actually sent (synthesized characters), never what a draft might cost.
type Meter struct {
	LLMInputTokens  int64
	LLMOutputTokens int64
	TTSCharacters   int64
}

// Estimate prices connected time and metered usage at the configured rates. Voice and
// media are charged per started minute; recognition is prorated per second. The
// result is rounded to 1/10,000 USD and is never negative.
func Estimate(r Rates, m Meter, connected time.Duration) float64 {
	if connected < 0 {
		connected = 0
	}
	startedMinutes := math.Ceil(connected.Seconds() / 60)
	minutes := connected.Seconds() / 60
	cost := startedMinutes*(r.TelephonyPerMinute+r.MediaPerMinute) +
		minutes*r.STTPerMinute +
		float64(max(m.LLMInputTokens, 0))/1e6*r.LLMInputPerMTok +
		float64(max(m.LLMOutputTokens, 0))/1e6*r.LLMOutputPerMTok +
		float64(max(m.TTSCharacters, 0))/1e3*r.TTSPer1KChars
	return math.Round(cost*1e4) / 1e4
}
