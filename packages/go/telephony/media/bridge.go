// Package media is the server side of a Twilio bidirectional Media Stream (#21). It
// speaks the documented WebSocket message protocol (connected, start, media, mark,
// stop inbound; media, mark, clear outbound), binds the stream to a verified call
// before accepting audio, enforces 8 kHz mono mu-law, feeds inbound audio through the
// providers.Transcriber boundary, and exposes outbound audio as a
// providers.MediaStream. Queues are bounded in both directions. It never records
// audio and never logs payloads, parameters or phone numbers.
//
// Protocol: https://www.twilio.com/docs/voice/media-streams/websocket-messages
package media

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
)

var (
	// ErrProtocol reports a message that violates the Media Streams protocol.
	ErrProtocol = errors.New("media stream protocol violation")
	// ErrUnauthorized reports a stream not bound to a verified call.
	ErrUnauthorized = errors.New("media stream not authorized")
	// ErrBacklog reports that inbound audio arrived faster than recognition consumed it.
	ErrBacklog = errors.New("media stream inbound backlog")
)

// Conn is one WebSocket connection carrying text messages. The webhooks handler adapts
// a real WebSocket; tests use an in-memory pair.
type Conn interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, msg []byte) error
}

// Start is the verified part of Twilio's start message.
type Start struct {
	StreamSID        string
	CallSID          string
	AccountSID       string
	CustomParameters map[string]string
}

// Authorizer binds a stream to one of our calls. It must verify a server-issued
// credential, not trust a provider id on its own (docs/contracts.md).
type Authorizer interface {
	Authorize(ctx context.Context, start Start) (callID string, err error)
}

// Config wires one bridge. Callbacks run on the bridge's goroutines, one at a time per
// kind, and must return promptly.
type Config struct {
	Authorizer  Authorizer
	Transcriber providers.Transcriber
	// OnStart receives the outbound stream once the call is verified.
	OnStart func(callID string, out providers.MediaStream)
	// OnTranscript receives recognition updates for the verified call.
	OnTranscript func(callID string, u providers.TranscriptUpdate) error
	// OnEvent receives acknowledged marks and the final stop.
	OnEvent func(callID string, e providers.MediaEvent)
	// InboundFrames bounds audio waiting for recognition. Zero means 250 (5 s).
	InboundFrames int
	// OutboundMessages bounds unsent outbound messages. Zero means 512.
	OutboundMessages int
	// StartTimeout bounds the wait for a verified start. Zero means 10 s.
	StartTimeout time.Duration
}

var (
	streamSID  = regexp.MustCompile(`^MZ[0-9a-f]{32}$`)
	callSID    = regexp.MustCompile(`^CA[0-9a-f]{32}$`)
	accountSID = regexp.MustCompile(`^AC[0-9a-f]{32}$`)
	markName   = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
)

// MaxMessageBytes bounds one inbound WebSocket message; Twilio media frames are small.
const MaxMessageBytes = 64 * 1024

type inbound struct {
	Event          string `json:"event"`
	SequenceNumber string `json:"sequenceNumber"`
	StreamSID      string `json:"streamSid"`
	Start          *struct {
		StreamSID        string            `json:"streamSid"`
		CallSID          string            `json:"callSid"`
		AccountSID       string            `json:"accountSid"`
		Tracks           []string          `json:"tracks"`
		CustomParameters map[string]string `json:"customParameters"`
		MediaFormat      struct {
			Encoding   string `json:"encoding"`
			SampleRate int    `json:"sampleRate"`
			Channels   int    `json:"channels"`
		} `json:"mediaFormat"`
	} `json:"start"`
	Media *struct {
		Track   string `json:"track"`
		Payload string `json:"payload"`
	} `json:"media"`
	Mark *struct {
		Name string `json:"name"`
	} `json:"mark"`
}

// Serve runs the protocol until stop, a protocol error, ctx cancellation or a read
// error. It returns nil after a clean stop.
func Serve(ctx context.Context, conn Conn, cfg Config) error {
	if cfg.Authorizer == nil || cfg.Transcriber == nil {
		return errors.New("media bridge requires an authorizer and transcriber")
	}
	if cfg.InboundFrames <= 0 {
		cfg.InboundFrames = 250
	}
	if cfg.OutboundMessages <= 0 {
		cfg.OutboundMessages = 512
	}
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = 10 * time.Second
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	start, err := awaitStart(ctx, conn, cfg.StartTimeout)
	if err != nil {
		return err
	}
	callID, err := cfg.Authorizer.Authorize(ctx, start.Start)
	if err != nil || !providers.ValidUUID(callID) {
		return ErrUnauthorized
	}
	out := newOutbound(ctx, conn, start.StreamSID, cfg.OutboundMessages)
	defer out.Close()
	go func() {
		if err := out.run(); err != nil {
			cancel(err)
		}
	}()

	session, err := cfg.Transcriber.Open(ctx, providers.TranscriptOpen{CallID: callID, Format: providers.TelephonyFormat},
		func(u providers.TranscriptUpdate) error {
			if cfg.OnTranscript == nil {
				return nil
			}
			return cfg.OnTranscript(callID, u)
		})
	if err != nil {
		return fmt.Errorf("transcription unavailable: %w", providers.ErrUnavailable)
	}
	defer session.Close()

	// Recognition runs on its own goroutine behind a bounded queue, so a slow
	// recognizer never stalls reading marks and stop from the provider.
	frames := make(chan providers.AudioChunk, cfg.InboundFrames)
	pushDone := make(chan struct{})
	go func() {
		defer close(pushDone)
		for chunk := range frames {
			if err := session.Push(ctx, chunk); err != nil {
				cancel(fmt.Errorf("transcription stopped: %w", providers.ErrUnavailable))
				return
			}
		}
	}()
	defer func() {
		// Stop a recognizer that is blocked in Push before waiting for it.
		cancel(nil)
		close(frames)
		<-pushDone
	}()

	if cfg.OnStart != nil {
		cfg.OnStart(callID, out)
	}
	emit := func(e providers.MediaEvent) {
		if cfg.OnEvent != nil {
			cfg.OnEvent(callID, e)
		}
	}
	lastSeq, _ := strconv.ParseUint(start.sequence, 10, 64)
	var audioSeq uint64
	for {
		data, err := conn.Read(ctx)
		if err != nil {
			if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
				return cause
			}
			return err
		}
		msg, err := decode(data)
		if err != nil {
			return err
		}
		if msg.StreamSID != start.StreamSID {
			return fmt.Errorf("%w: stream id", ErrProtocol)
		}
		seq, err := strconv.ParseUint(msg.SequenceNumber, 10, 64)
		if err != nil || seq <= lastSeq {
			return fmt.Errorf("%w: sequence", ErrProtocol)
		}
		lastSeq = seq
		switch msg.Event {
		case "media":
			if msg.Media == nil || (msg.Media.Track != "" && msg.Media.Track != "inbound") {
				return fmt.Errorf("%w: media track", ErrProtocol)
			}
			audio, err := base64.StdEncoding.DecodeString(msg.Media.Payload)
			if err != nil || len(audio) == 0 || len(audio) > providers.MaxChunkBytes {
				return fmt.Errorf("%w: media payload", ErrProtocol)
			}
			audioSeq++
			select {
			case frames <- providers.AudioChunk{Format: providers.TelephonyFormat, Seq: audioSeq, Data: audio}:
			default:
				return ErrBacklog
			}
		case "mark":
			if msg.Mark == nil || !markName.MatchString(msg.Mark.Name) {
				return fmt.Errorf("%w: mark", ErrProtocol)
			}
			if out.acknowledge(msg.Mark.Name) {
				emit(providers.MediaEvent{Kind: providers.MediaMark, Mark: msg.Mark.Name})
			}
		case "stop":
			out.Close()
			emit(providers.MediaEvent{Kind: providers.MediaStop})
			return nil
		case "dtmf":
			// Keypad input is not part of the MVP; ignore it.
		default:
			return fmt.Errorf("%w: event", ErrProtocol)
		}
	}
}

type startMessage struct {
	Start
	sequence string
}

func decode(data []byte) (inbound, error) {
	var msg inbound
	if len(data) > MaxMessageBytes || json.Unmarshal(data, &msg) != nil || msg.Event == "" {
		return inbound{}, fmt.Errorf("%w: message", ErrProtocol)
	}
	return msg, nil
}

// awaitStart reads the optional connected message and the start message, enforcing
// the telephony audio format before any audio is accepted.
func awaitStart(ctx context.Context, conn Conn, timeout time.Duration) (startMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for range 2 {
		data, err := conn.Read(ctx)
		if err != nil {
			return startMessage{}, fmt.Errorf("%w: no start", ErrProtocol)
		}
		msg, err := decode(data)
		if err != nil {
			return startMessage{}, err
		}
		switch msg.Event {
		case "connected":
			continue
		case "start":
		default:
			return startMessage{}, fmt.Errorf("%w: %s before start", ErrProtocol, eventName(msg.Event))
		}
		s := msg.Start
		if s == nil || msg.StreamSID != s.StreamSID || !streamSID.MatchString(s.StreamSID) || !callSID.MatchString(s.CallSID) ||
			!accountSID.MatchString(s.AccountSID) {
			return startMessage{}, fmt.Errorf("%w: start identifiers", ErrProtocol)
		}
		if s.MediaFormat.Encoding != string(providers.EncodingMulaw) || s.MediaFormat.SampleRate != 8000 || s.MediaFormat.Channels != 1 {
			return startMessage{}, fmt.Errorf("%w: %w", ErrProtocol, providers.ErrUnsupportedFormat)
		}
		inboundTrack := false
		for _, t := range s.Tracks {
			inboundTrack = inboundTrack || t == "inbound"
		}
		if !inboundTrack || len(s.CustomParameters) > 16 {
			return startMessage{}, fmt.Errorf("%w: start tracks", ErrProtocol)
		}
		return startMessage{Start: Start{StreamSID: s.StreamSID, CallSID: s.CallSID, AccountSID: s.AccountSID,
			CustomParameters: s.CustomParameters}, sequence: msg.SequenceNumber}, nil
	}
	return startMessage{}, fmt.Errorf("%w: no start", ErrProtocol)
}

// eventName keeps unknown provider strings out of errors.
func eventName(e string) string {
	switch e {
	case "media", "mark", "stop", "dtmf":
		return e
	}
	return "unknown event"
}

// outbound is the providers.MediaStream for one verified stream. Messages are written
// in order by one goroutine; Clear drops unsent audio and pending marks.
type outbound struct {
	ctx    context.Context
	conn   Conn
	stream string

	mu      sync.Mutex
	queue   []outMsg
	limit   int
	pending map[string]bool // marks sent or queued, not yet acknowledged
	closed  bool
	wake    chan struct{}
	done    chan struct{}
}

type outMsg struct {
	data  []byte
	audio int
	mark  string
}

var _ providers.MediaStream = (*outbound)(nil)

func newOutbound(ctx context.Context, conn Conn, stream string, limit int) *outbound {
	return &outbound{ctx: ctx, conn: conn, stream: stream, limit: limit, pending: map[string]bool{},
		wake: make(chan struct{}, 1), done: make(chan struct{})}
}

func (o *outbound) signal() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

func (o *outbound) enqueue(m outMsg) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.ctx.Err() != nil {
		return providers.ErrClosed
	}
	if len(o.queue) >= o.limit {
		return fmt.Errorf("%w: outbound media queue is full", providers.ErrInvalid)
	}
	o.queue = append(o.queue, m)
	if m.mark != "" {
		o.pending[m.mark] = true
	}
	o.signal()
	return nil
}

func (o *outbound) Send(ctx context.Context, chunk providers.AudioChunk) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := chunk.Validate(); err != nil {
		return err
	}
	if chunk.Format != providers.TelephonyFormat {
		return providers.ErrUnsupportedFormat
	}
	data, _ := json.Marshal(map[string]any{"event": "media", "streamSid": o.stream,
		"media": map[string]string{"payload": base64.StdEncoding.EncodeToString(chunk.Data)}})
	return o.enqueue(outMsg{data: data, audio: len(chunk.Data)})
}

func (o *outbound) Mark(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !markName.MatchString(name) {
		return fmt.Errorf("%w: mark name", providers.ErrInvalid)
	}
	data, _ := json.Marshal(map[string]any{"event": "mark", "streamSid": o.stream, "mark": map[string]string{"name": name}})
	return o.enqueue(outMsg{data: data, mark: name})
}

// Clear drops unsent audio and marks, forgets every pending mark (so a late
// acknowledgement is never reported) and tells the provider to empty its buffer.
func (o *outbound) Clear(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	o.mu.Lock()
	if o.closed || o.ctx.Err() != nil {
		o.mu.Unlock()
		return 0, providers.ErrClosed
	}
	dropped := 0
	for _, m := range o.queue {
		dropped += m.audio
	}
	data, _ := json.Marshal(map[string]any{"event": "clear", "streamSid": o.stream})
	o.queue = []outMsg{{data: data}}
	o.pending = map[string]bool{}
	o.signal()
	o.mu.Unlock()
	return dropped, nil
}

// Close detaches from the stream without ending the call. Unsent messages are dropped.
func (o *outbound) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.closed {
		o.closed = true
		o.queue = nil
		o.pending = map[string]bool{}
		o.signal()
	}
	return nil
}

// acknowledge reports whether a provider mark matches one still pending.
func (o *outbound) acknowledge(name string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.pending[name] {
		return false
	}
	delete(o.pending, name)
	return true
}

func (o *outbound) run() error {
	defer close(o.done)
	for {
		o.mu.Lock()
		if o.closed {
			o.mu.Unlock()
			return nil
		}
		var next *outMsg
		if len(o.queue) > 0 {
			m := o.queue[0]
			o.queue = o.queue[1:]
			next = &m
		}
		o.mu.Unlock()
		if next == nil {
			select {
			case <-o.ctx.Done():
				return nil
			case <-o.wake:
			}
			continue
		}
		if err := o.conn.Write(o.ctx, next.data); err != nil {
			if o.ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("media write failed: %w", providers.ErrUnavailable)
		}
	}
}
