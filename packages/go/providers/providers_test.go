package providers

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestTelephonyFormatIs8kMonoMulaw(t *testing.T) {
	f := TelephonyFormat
	if f.Validate() != nil || f.BytesPerFrame() != 160 || f.DurationMS(8000) != 1000 {
		t.Fatalf("telephony format: valid=%v frame=%d", f.Validate(), f.BytesPerFrame())
	}
	pcm := AudioFormat{Encoding: EncodingPCM16, SampleRate: 16000, Channels: 1}
	if pcm.Validate() != nil || pcm.BytesPerFrame() != 640 {
		t.Fatalf("pcm16 frame = %d", pcm.BytesPerFrame())
	}
	for _, bad := range []AudioFormat{
		{Encoding: "audio/mpeg", SampleRate: 8000, Channels: 1},
		{Encoding: EncodingMulaw, SampleRate: 4000, Channels: 1},
		{Encoding: EncodingMulaw, SampleRate: 8000, Channels: 2},
		{},
	} {
		if err := bad.Validate(); !errors.Is(err, ErrUnsupportedFormat) {
			t.Errorf("%+v: %v, want ErrUnsupportedFormat", bad, err)
		}
	}
}

func TestAudioChunkBounds(t *testing.T) {
	ok := AudioChunk{Format: TelephonyFormat, Seq: 1, Data: make([]byte, 160)}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]AudioChunk{
		"zero seq": {Format: TelephonyFormat, Data: []byte{1}},
		"empty":    {Format: TelephonyFormat, Seq: 1},
		"too big":  {Format: TelephonyFormat, Seq: 1, Data: make([]byte, MaxChunkBytes+1)},
	} {
		if err := c.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
}

const callID = "3d2e9e65-7f30-4ca1-9e80-4b5c6d7e8f92"

func TestDialRequiresUUIDE164AndSecureCallbacks(t *testing.T) {
	ok := Dial{CallID: callID, Destination: "+12255550142", Callbacks: Callbacks{
		Voice: "https://switchboard.test/webhooks/twilio/voice", Status: "https://switchboard.test/webhooks/twilio/status",
		Media: "wss://switchboard.test/webhooks/twilio/media"}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Dial){
		"uppercase id":  func(d *Dial) { d.CallID = strings.ToUpper(callID) },
		"no plus":       func(d *Dial) { d.Destination = "12255550142" },
		"too long":      func(d *Dial) { d.Destination = "+1234567890123456" },
		"http voice":    func(d *Dial) { d.Callbacks.Voice = "http://switchboard.test/v" },
		"status spaces": func(d *Dial) { d.Callbacks.Status = "https://switchboard.test/a b" },
		"ws media":      func(d *Dial) { d.Callbacks.Media = "ws://switchboard.test/m" },
		"no host":       func(d *Dial) { d.Callbacks.Media = "wss:///m" },
	} {
		d := ok
		mutate(&d)
		if err := d.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
}

func TestResponseRequestBounds(t *testing.T) {
	ok := ResponseRequest{CallID: callID, Goal: json.RawMessage(`{"kind":"restaurant"}`),
		Transcript:   []TranscriptTurn{{Speaker: SpeakerBusiness, Text: "Hello?"}},
		Instructions: []string{"8:45 is fine."}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	long := make([]TranscriptTurn, MaxTranscriptTurns+1)
	for i := range long {
		long[i] = TranscriptTurn{Speaker: SpeakerBusiness, Text: "x"}
	}
	for name, mutate := range map[string]func(*ResponseRequest){
		"no goal":           func(r *ResponseRequest) { r.Goal = nil },
		"no transcript":     func(r *ResponseRequest) { r.Transcript = nil },
		"too many turns":    func(r *ResponseRequest) { r.Transcript = long },
		"operator speaker":  func(r *ResponseRequest) { r.Transcript = []TranscriptTurn{{Speaker: "operator", Text: "x"}} },
		"blank turn":        func(r *ResponseRequest) { r.Transcript = []TranscriptTurn{{Speaker: SpeakerBusiness, Text: "  "}} },
		"long turn":         func(r *ResponseRequest) { r.Transcript[0].Text = strings.Repeat("é", MaxTurnText+1) },
		"blank instruction": func(r *ResponseRequest) { r.Instructions = []string{""} },
		"bad call id":       func(r *ResponseRequest) { r.CallID = "" },
	} {
		r := ok
		r.Transcript = append([]TranscriptTurn(nil), ok.Transcript...)
		mutate(&r)
		if err := r.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
}

func TestSpeechRequestBounds(t *testing.T) {
	ok := SpeechRequest{Text: "Thank you.", Voice: "voice-1", Format: TelephonyFormat}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	r := ok
	r.Text = strings.Repeat("a", MaxSpeechText+1)
	if err := r.Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("long text: %v", err)
	}
	r = ok
	r.Format.Channels = 2
	if err := r.Validate(); !errors.Is(err, ErrUnsupportedFormat) {
		t.Errorf("stereo: %v", err)
	}
}
