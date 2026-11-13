package providers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The fixtures are shared with packages/ts/api/test/providers.test.mjs so both sides
// agree on the wire shape.
type fixtureFile struct {
	Sentinels []string `json:"secret_sentinels"`
	Cases     []struct {
		Name        string            `json:"name"`
		Env         map[string]string `json:"env"`
		Implemented struct {
			Telephony bool `json:"telephony"`
			STT       bool `json:"stt"`
			LLM       bool `json:"llm"`
			TTS       bool `json:"tts"`
		} `json:"implemented"`
		Outages  []Kind `json:"outages"`
		Expected Status `json:"expected"`
	} `json:"cases"`
}

func loadFixtures(t *testing.T) fixtureFile {
	t.Helper()
	data, err := os.ReadFile("../../../ts/api/test/fixtures/providers.json")
	if err != nil {
		t.Fatal(err)
	}
	var f fixtureFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) < 5 || len(f.Sentinels) == 0 {
		t.Fatal("provider fixtures are incomplete")
	}
	return f
}

func TestFixturesThroughHTTP(t *testing.T) {
	f := loadFixtures(t)
	for _, tc := range f.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			lookup := func(k string) (string, bool) { v, ok := tc.Env[k]; return v, ok }
			r := New(FromEnv(lookup, Implemented(tc.Implemented)), nil)
			for _, k := range tc.Outages {
				r.ReportOutage(k)
			}
			mux := http.NewServeMux()
			Register(mux, r)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/providers", nil))
			if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("status %d headers %v", w.Code, w.Header())
			}
			body := w.Body.String()
			for _, secret := range f.Sentinels {
				if strings.Contains(body, secret) {
					t.Fatalf("response leaked a configured secret")
				}
			}
			for _, v := range tc.Env {
				if len(v) > 6 && v != "mock" && v != "real" && v != "true" && v != "false" && strings.Contains(body, v) {
					t.Fatalf("response echoed a configured value")
				}
			}
			var got Status
			d := json.NewDecoder(strings.NewReader(body))
			d.DisallowUnknownFields()
			if err := d.Decode(&got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.Expected) {
				t.Fatalf("got %+v\nwant %+v", got, tc.Expected)
			}
			err := r.RequireReal()
			if (tc.Expected.Mode == "real") != (err == nil) || (err != nil && !errors.Is(err, ErrRealUnavailable)) {
				t.Fatalf("RequireReal = %v for mode %s", err, tc.Expected.Mode)
			}
		})
	}
}

func TestOutageExpiresAndRecovers(t *testing.T) {
	now := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	ready := Implemented{true, true, true, true}
	r := New(Config{RealCallsEnabled: true, RealMode: true, Implemented: ready, Configured: ready, OutageCooldown: 30 * time.Second},
		func() time.Time { return now })
	if r.RequireReal() != nil {
		t.Fatal("ready server rejected real start")
	}
	r.ReportOutage(TTS)
	if s := r.Status(); s.Mode != "mock" || s.TTS.Available || !errors.Is(r.RequireReal(), ErrRealUnavailable) {
		t.Fatalf("outage not reflected: %+v", s)
	}
	now = now.Add(29 * time.Second)
	if r.Status().TTS.Available {
		t.Fatal("outage cleared before cooldown")
	}
	now = now.Add(time.Second)
	if s := r.Status(); !s.TTS.Available || s.Mode != "real" {
		t.Fatalf("outage did not expire: %+v", s)
	}
	r.ReportOutage(LLM)
	r.ReportRecovered(LLM)
	if !r.Status().LLM.Available {
		t.Fatal("recovery not reflected")
	}
	r.ReportOutage(Kind("fax"))
	if r.Status().Mode != "real" {
		t.Fatal("unknown kind changed readiness")
	}
}

func TestRealModeNeedsExplicitSetting(t *testing.T) {
	ready := Implemented{true, true, true, true}
	r := New(Config{RealCallsEnabled: true, RealMode: false, Implemented: ready, Configured: ready}, nil)
	if s := r.Status(); s.Mode != "mock" || !s.Telephony.Available {
		t.Fatalf("CALL_MODE=mock still offered real calling: %+v", s)
	}
}
