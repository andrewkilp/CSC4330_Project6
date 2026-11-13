// Package providers defines the server-side boundaries for the four call providers in
// docs/contracts.md: Telephony, Transcriber, Responder and Synthesizer. It contains no
// vendor SDKs, network code or credentials. Real adapters (#11, #14, #15, #20, #21) and
// the deterministic mocks in packages/go/mock implement these interfaces, and the suite
// in providers/providertest checks that they behave the same way.
//
// Cancellation rules shared by every interface:
//
//   - Methods that take an emit callback call it synchronously, from one goroutine at a
//     time, and never after the method has returned.
//   - Cancelling the context stops further callbacks promptly; a blocking method then
//     returns an error wrapping context.Canceled. For sessions, callback invocation
//     begins logically when delivery is admitted under the session's state lock.
//     An admitted callback may finish even if Close/cancellation happens before its
//     user function enters. No callback may be admitted after closure/cancellation.
//   - An error returned by the callback stops delivery and is returned unchanged
//     (sessions instead close, and later use returns ErrClosed).
//   - Sessions (MediaStream, TranscriptSession) admit no callback after Close returns or
//     their context is cancelled, and reject later use with ErrClosed. Close may be
//     called from inside the session's own callback, so Close does not wait for an
//     admitted callback to finish. Consumers must recheck their context/generation
//     token before persisting or playing in-flight updates.
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	// ErrInvalid reports a request that violates this contract. Nothing was sent.
	ErrInvalid = errors.New("invalid provider request")
	// ErrUnsupportedFormat reports an audio format the adapter cannot accept or produce.
	ErrUnsupportedFormat = errors.New("unsupported audio format")
	// ErrNotFound reports an unknown provider call id.
	ErrNotFound = errors.New("provider call not found")
	// ErrClosed reports use of an ended call, closed stream or closed session.
	ErrClosed = errors.New("provider session closed")
	// ErrUnavailable is the user-safe failure for any vendor or network error. Adapters
	// never return vendor bodies, messages or credentials.
	ErrUnavailable = errors.New("provider unavailable")
)

// ---------------------------------------------------------------------------
// Audio metadata.

type Encoding string

const (
	// EncodingMulaw is G.711 mu-law, the format Twilio Media Streams send and accept.
	EncodingMulaw Encoding = "audio/x-mulaw"
	// EncodingPCM16 is signed 16-bit little-endian linear PCM.
	EncodingPCM16 Encoding = "audio/l16"
)

// AudioFormat describes raw audio bytes. Transport encodings such as base64 are the
// adapter's concern; chunks always carry decoded bytes.
type AudioFormat struct {
	Encoding   Encoding
	SampleRate int
	Channels   int
}

// TelephonyFormat is 8 kHz mono mu-law, the media format in docs/contracts.md.
var TelephonyFormat = AudioFormat{Encoding: EncodingMulaw, SampleRate: 8000, Channels: 1}

const (
	// FrameMS is the duration of one telephony media frame.
	FrameMS = 20
	// MaxChunkBytes bounds a single chunk so queues stay bounded.
	MaxChunkBytes = 64 * 1024
)

func (f AudioFormat) Validate() error {
	if f.Encoding != EncodingMulaw && f.Encoding != EncodingPCM16 {
		return fmt.Errorf("%w: encoding", ErrUnsupportedFormat)
	}
	if f.SampleRate < 8000 || f.SampleRate > 48000 || f.Channels != 1 {
		return fmt.Errorf("%w: rate or channels", ErrUnsupportedFormat)
	}
	return nil
}

// BytesPerFrame is the size of one FrameMS frame in this format.
func (f AudioFormat) BytesPerFrame() int {
	samples := f.SampleRate * FrameMS / 1000
	if f.Encoding == EncodingPCM16 {
		return samples * 2 * f.Channels
	}
	return samples * f.Channels
}

// DurationMS is the playback length of n bytes in this format.
func (f AudioFormat) DurationMS(n int) int64 {
	perMS := f.BytesPerFrame() / FrameMS
	if perMS == 0 {
		return 0
	}
	return int64(n / perMS)
}

// AudioChunk is a bounded piece of decoded audio. Seq is strictly increasing per stream,
// starting at 1, so a consumer can detect loss.
type AudioChunk struct {
	Format AudioFormat
	Seq    uint64
	Data   []byte
}

func (c AudioChunk) Validate() error {
	if err := c.Format.Validate(); err != nil {
		return err
	}
	if c.Seq == 0 || len(c.Data) == 0 || len(c.Data) > MaxChunkBytes {
		return fmt.Errorf("%w: audio chunk", ErrInvalid)
	}
	return nil
}

// Token fences generated text and audio to one response generation. It mirrors
// conversation.Token; a callback whose token is no longer current must be discarded.
type Token struct {
	ContextRevision uint64
	Generation      uint64
}

// ---------------------------------------------------------------------------
// Telephony: start and end a call and carry its authenticated bidirectional media.

// Callbacks are the absolute HTTPS URLs the provider calls back. The handlers (#16, #21)
// verify signatures; a provider call id is never authorization by itself.
type Callbacks struct {
	Voice  string
	Status string
	Media  string // wss:// URL for the media stream
}

type Dial struct {
	CallID      string // our call UUID
	Destination string // E.164
	Callbacks   Callbacks
}

var (
	uuidPattern  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	e164Pattern  = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)
	httpsPattern = regexp.MustCompile(`^https://[^\s/?#]+[^\s]*$`)
	wssPattern   = regexp.MustCompile(`^wss://[^\s/?#]+[^\s]*$`)
)

// ValidUUID reports whether s is a lowercase RFC 4122 UUID as used for call ids.
func ValidUUID(s string) bool { return uuidPattern.MatchString(s) }

func (d Dial) Validate() error {
	if !ValidUUID(d.CallID) || !e164Pattern.MatchString(d.Destination) ||
		!httpsPattern.MatchString(d.Callbacks.Voice) || !httpsPattern.MatchString(d.Callbacks.Status) ||
		!wssPattern.MatchString(d.Callbacks.Media) {
		return fmt.Errorf("%w: dial", ErrInvalid)
	}
	return nil
}

type MediaEventKind string

const (
	// MediaAudio carries inbound audio from the business.
	MediaAudio MediaEventKind = "audio"
	// MediaMark reports that outbound audio queued before a mark finished playing. It
	// means the provider buffer progressed, not that anyone confirmed anything.
	MediaMark MediaEventKind = "mark"
	// MediaStop reports that the provider closed the stream (hang-up or End). It is the
	// last event for that stream.
	MediaStop MediaEventKind = "stop"
)

type MediaEvent struct {
	Kind  MediaEventKind
	Audio AudioChunk // MediaAudio only
	Mark  string     // MediaMark only
}

// MediaStream sends assistant audio to the call. Outbound audio is queued and played in
// order; Clear drops everything not yet played, including pending marks, which are then
// never reported. All methods are safe for concurrent use.
type MediaStream interface {
	Send(ctx context.Context, chunk AudioChunk) error
	Mark(ctx context.Context, name string) error
	// Clear drops queued outbound audio and returns how many bytes were dropped.
	Clear(ctx context.Context) (dropped int, err error)
	// Close detaches from the stream without ending the call.
	Close() error
}

type Telephony interface {
	// Start places the call and returns the provider's call id. It is called only after
	// an explicit user action; nothing in this package dials automatically.
	Start(ctx context.Context, dial Dial) (providerCallID string, err error)
	// End hangs up. Ending an ended call is a no-op; an unknown id is ErrNotFound.
	End(ctx context.Context, providerCallID string) error
	// Media attaches to the call's media stream. emit receives inbound audio, marks and
	// the final stop event in order, until stop, Close or ctx cancellation.
	Media(ctx context.Context, providerCallID string, emit func(MediaEvent) error) (MediaStream, error)
}

// ---------------------------------------------------------------------------
// Transcriber: incremental speech-to-text of the business's side of the call.

type TranscriptOpen struct {
	CallID string
	Format AudioFormat
}

// TranscriptUpdate is one partial or final business turn. Partials for a turn share its
// TurnID and are replaced by later updates; a final update is never changed.
type TranscriptUpdate struct {
	TurnID      string
	Text        string
	IsFinal     bool
	StartedAtMS int64
	EndedAtMS   int64 // set on final updates
}

type TranscriptSession interface {
	// Push sends audio in the format given to Open. It may call the Open callback
	// synchronously before returning.
	Push(ctx context.Context, chunk AudioChunk) error
	// Close stops recognition. Pending partial text is discarded, not finalized.
	Close() error
}

type Transcriber interface {
	Open(ctx context.Context, open TranscriptOpen, emit func(TranscriptUpdate) error) (TranscriptSession, error)
}

// ---------------------------------------------------------------------------
// Responder: generate the assistant's next reply.

type Speaker string

const (
	SpeakerAssistant Speaker = "assistant"
	SpeakerBusiness  Speaker = "business"
)

type TranscriptTurn struct {
	Speaker Speaker
	Text    string
}

const (
	MaxTranscriptTurns = 200
	MaxTurnText        = 10000
	MaxInstructions    = 100
	MaxInstructionText = 1000
	MaxResponseText    = 1500
	MaxSpeechText      = 1500
)

// ResponseRequest carries only finalized turns and applied instructions. Instructions
// are private operator context kept separate from untrusted transcript text.
type ResponseRequest struct {
	CallID          string
	Goal            json.RawMessage
	Transcript      []TranscriptTurn
	Instructions    []string
	ContextRevision uint64
}

func (r ResponseRequest) Validate() error {
	if !ValidUUID(r.CallID) || !json.Valid(r.Goal) || len(r.Transcript) == 0 ||
		len(r.Transcript) > MaxTranscriptTurns || len(r.Instructions) > MaxInstructions {
		return fmt.Errorf("%w: response request", ErrInvalid)
	}
	for _, t := range r.Transcript {
		if (t.Speaker != SpeakerAssistant && t.Speaker != SpeakerBusiness) || strings.TrimSpace(t.Text) == "" ||
			utf8.RuneCountInString(t.Text) > MaxTurnText {
			return fmt.Errorf("%w: transcript", ErrInvalid)
		}
	}
	for _, s := range r.Instructions {
		if strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > MaxInstructionText {
			return fmt.Errorf("%w: instruction", ErrInvalid)
		}
	}
	return nil
}

// ResponseDraft is cumulative text. Only Final=true is a complete reply, and it is still
// only a suggestion: approval and revision checks happen in the server.
type ResponseDraft struct {
	Text            string
	ContextRevision uint64
	Final           bool
}

type Responder interface {
	Generate(ctx context.Context, req ResponseRequest, emit func(ResponseDraft) error) error
}

// ---------------------------------------------------------------------------
// Synthesizer: speak approved text.

type SpeechRequest struct {
	Text   string
	Voice  string // server-side voice id; never from the client
	Format AudioFormat
	Token  Token
}

func (r SpeechRequest) Validate() error {
	if strings.TrimSpace(r.Text) == "" || utf8.RuneCountInString(r.Text) > MaxSpeechText ||
		strings.TrimSpace(r.Voice) == "" || len(r.Voice) > 100 {
		return fmt.Errorf("%w: speech request", ErrInvalid)
	}
	return r.Format.Validate()
}

// SpeechChunk is synthesized audio tagged with the generation token it was made for, so
// the caller can drop audio from an obsolete generation before it reaches the call.
type SpeechChunk struct {
	Audio AudioChunk
	Token Token
	Last  bool
}

type Synthesizer interface {
	Stream(ctx context.Context, req SpeechRequest, emit func(SpeechChunk) error) error
}
