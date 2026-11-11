package mock

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
)

// Mock provider adapters (#6). They implement the four interfaces in
// packages/go/providers from one #5 scenario, without network, audio devices, real
// clocks beyond optional pacing, or credentials. Their audio is synthetic mu-law that
// only these mocks understand; it is never real speech.

// ErrNoScriptedReply reports a response request the scenario never scripted.
var ErrNoScriptedReply = errors.New("mock scenario has no scripted reply for this request")

// fictionalDestination is the North American 555-0100..0199 range reserved for fiction.
// The mock telephony refuses anything else, so a mock can never look like a real call.
var fictionalDestination = regexp.MustCompile(`^\+1[2-9][0-9]{2}55501[0-9]{2}$`)

// Options tunes pacing. The zero value is fast and deterministic.
type Options struct {
	// FrameDelay is the time the mock telephony takes to play or deliver one 20 ms
	// frame. Default 1 ms, fast enough for tests yet slow enough that Clear can drop
	// audio that has not played. Negative means no delay.
	FrameDelay time.Duration
	// FramesPerUpdate is how many inbound frames carry each partial or final transcript
	// update. Default 5.
	FramesPerUpdate int
	// Pace, when set, runs before every callback the responder, synthesizer and
	// telephony worker make. Tests use it to stop a stream at an exact point.
	Pace func(context.Context) error
}

func (o Options) withDefaults() Options {
	if o.FrameDelay == 0 {
		o.FrameDelay = time.Millisecond
	}
	if o.FrameDelay < 0 {
		o.FrameDelay = 0
	}
	if o.FramesPerUpdate <= 0 {
		o.FramesPerUpdate = 5
	}
	return o
}

func (o Options) pace(ctx context.Context) error {
	if o.Pace != nil {
		if err := o.Pace(ctx); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// Providers is one scenario's full adapter set. The adapters share a read-only script;
// each has its own state.
type Providers struct {
	Fixture     Fixture
	Telephony   *ScenarioTelephony
	Transcriber *ScenarioTranscriber
	Responder   *ScenarioResponder
	Synthesizer *ScenarioSynthesizer
}

var (
	_ providers.Telephony   = (*ScenarioTelephony)(nil)
	_ providers.Transcriber = (*ScenarioTranscriber)(nil)
	_ providers.Responder   = (*ScenarioResponder)(nil)
	_ providers.Synthesizer = (*ScenarioSynthesizer)(nil)
	_ providers.MediaStream = (*ScenarioMediaStream)(nil)
)

// NewProviders builds all four adapters from the named scenario and seed.
func NewProviders(name string, seed uint64, opts Options) (*Providers, error) {
	f, err := Generate(name, seed)
	if err != nil {
		return nil, err
	}
	s, err := newScript(f)
	if err != nil {
		return nil, err
	}
	opts = opts.withDefaults()
	return &Providers{
		Fixture:     f,
		Telephony:   &ScenarioTelephony{script: s, opts: opts, src: source{seed: seed, scenario: name}, calls: map[string]*telephonyCall{}},
		Transcriber: &ScenarioTranscriber{script: s, opts: opts},
		Responder:   &ScenarioResponder{script: s, opts: opts},
		Synthesizer: &ScenarioSynthesizer{opts: opts},
	}, nil
}

// ---------------------------------------------------------------------------
// Script extracted from a fixture.

type businessTurn struct {
	id       string
	partials []string
	final    string
	started  int64
	ended    int64
}

// ScriptedReply is one suggestion from the scenario, keyed by what the responder sees.
type ScriptedReply struct {
	ResponseID    string
	BusinessTurns int      // finalized business turns before the reply
	Instructions  []string // applied instructions before the reply, in order
	Text          string
}

type script struct {
	goal     json.RawMessage
	business []businessTurn
	replies  []ScriptedReply
}

func newScript(f Fixture) (*script, error) {
	s := &script{}
	index := map[string]int{}
	var instructions []string
	finals := 0
	for _, e := range f.Events {
		switch p := e.Payload.(type) {
		case Call:
			if s.goal == nil {
				goal, err := json.Marshal(p.Goal)
				if err != nil {
					return nil, err
				}
				s.goal = goal
			}
		case Turn:
			if p.Speaker != "business" {
				continue
			}
			i, ok := index[p.ID]
			if !ok {
				i = len(s.business)
				index[p.ID] = i
				s.business = append(s.business, businessTurn{id: p.ID, started: p.StartedAtMS})
			}
			if p.IsFinal {
				s.business[i].final = p.Text
				if p.EndedAtMS != nil {
					s.business[i].ended = *p.EndedAtMS
				}
				finals++
			} else {
				s.business[i].partials = append(s.business[i].partials, p.Text)
			}
		case Instruction:
			if p.Status == "applied" {
				instructions = append(instructions, p.Text)
			}
		case Response:
			if p.Status == "suggested" {
				s.replies = append(s.replies, ScriptedReply{ResponseID: p.ID, BusinessTurns: finals,
					Instructions: append([]string(nil), instructions...), Text: p.Text})
			}
		}
	}
	if len(s.business) == 0 || len(s.replies) == 0 || len(s.business) > 255 {
		return nil, fmt.Errorf("mock scenario %q cannot drive the provider adapters", f.Scenario)
	}
	return s, nil
}

// Script returns the scenario's replies in order, for test drivers.
func (p *Providers) Script() []ScriptedReply {
	return append([]ScriptedReply(nil), p.Responder.script.replies...)
}

// ---------------------------------------------------------------------------
// Synthetic audio. Inbound business frames carry a marker so the mock transcriber can
// tell which scripted turn and frame it received; everything else is mu-law silence.

const (
	frameMarker   = 0x7E
	mulawSilence  = 0xFF
	maxQueueBytes = 60 * 8000 // sixty seconds of telephony audio
)

func businessFrame(turn, frame, total int) []byte {
	b := make([]byte, providers.TelephonyFormat.BytesPerFrame())
	for i := range b {
		b[i] = mulawSilence
	}
	b[0], b[1] = frameMarker, byte(turn)
	b[2], b[3] = byte(frame>>8), byte(frame)
	b[4], b[5] = byte(total>>8), byte(total)
	return b
}

func readFrame(b []byte) (turn, frame, total int, ok bool) {
	if len(b) < 6 || b[0] != frameMarker {
		return 0, 0, 0, false
	}
	return int(b[1]), int(b[2])<<8 | int(b[3]), int(b[4])<<8 | int(b[5]), true
}

// ---------------------------------------------------------------------------
// Telephony.

// ScenarioTelephony "places" calls to fictional numbers only. On attach it plays the
// first business turn; after each played mark it plays the next one, like a business
// answering once the assistant has finished speaking.
type ScenarioTelephony struct {
	script *script
	opts   Options
	src    source

	mu    sync.Mutex
	calls map[string]*telephonyCall
	count int
}

type telephonyCall struct {
	nextBusiness int
	ended        bool
	stream       *ScenarioMediaStream
}

func (t *ScenarioTelephony) Start(ctx context.Context, dial providers.Dial) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := dial.Validate(); err != nil {
		return "", err
	}
	if !fictionalDestination.MatchString(dial.Destination) {
		return "", fmt.Errorf("%w: mock calls use fictional 555-01XX numbers only", providers.ErrInvalid)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.count++
	sum := t.src.sum(fmt.Sprintf("provider-call/%s/%d", dial.CallID, t.count))
	id := "MOCKCA" + hex.EncodeToString(sum[:16])
	t.calls[id] = &telephonyCall{}
	return id, nil
}

func (t *ScenarioTelephony) End(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	c, ok := t.calls[id]
	if !ok {
		t.mu.Unlock()
		return providers.ErrNotFound
	}
	c.ended = true
	stream := c.stream
	c.stream = nil
	t.mu.Unlock()
	if stream != nil {
		stream.hangUp()
	}
	return nil
}

func (t *ScenarioTelephony) Media(ctx context.Context, id string, emit func(providers.MediaEvent) error) (providers.MediaStream, error) {
	if emit == nil {
		return nil, fmt.Errorf("%w: nil media callback", providers.ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.mu.Lock()
	c, ok := t.calls[id]
	switch {
	case !ok:
		t.mu.Unlock()
		return nil, providers.ErrNotFound
	case c.ended:
		t.mu.Unlock()
		return nil, providers.ErrClosed
	}
	previous := c.stream
	s := &ScenarioMediaStream{owner: t, ctx: ctx, call: c, opts: t.opts, emit: emit, wake: make(chan struct{}, 1), done: make(chan struct{})}
	c.stream = s
	if c.nextBusiness == 0 {
		t.queueBusinessLocked(c, s)
	}
	t.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	stop := context.AfterFunc(ctx, func() { s.detach(false) })
	go func() {
		defer stop()
		s.run(ctx)
	}()
	return s, nil
}

// queueBusinessLocked appends the next business turn's audio. Caller holds t.mu.
func (t *ScenarioTelephony) queueBusinessLocked(c *telephonyCall, s *ScenarioMediaStream) {
	if c.nextBusiness >= len(t.script.business) {
		return
	}
	turn := t.script.business[c.nextBusiness]
	total := (len(turn.partials) + 1) * t.opts.FramesPerUpdate
	s.mu.Lock()
	for i := range total {
		s.inbound = append(s.inbound, businessFrame(c.nextBusiness, i, total))
	}
	s.mu.Unlock()
	c.nextBusiness++
	s.signal()
}

type outboundItem struct {
	audio []byte
	mark  string
}

// ScenarioMediaStream is the mock bidirectional media stream. One worker goroutine
// makes every callback, so callbacks never overlap.
type ScenarioMediaStream struct {
	owner *ScenarioTelephony
	ctx   context.Context
	call  *telephonyCall
	opts  Options
	emit  func(providers.MediaEvent) error
	wake  chan struct{}
	done  chan struct{}

	mu         sync.Mutex
	inbound    [][]byte
	inboundSeq uint64
	outbound   []outboundItem
	queued     int
	played     int
	closed     bool
	hungUp     bool
	inCallback bool
}

func (s *ScenarioMediaStream) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Played reports how many outbound audio bytes have played. Mock-only.
func (s *ScenarioMediaStream) Played() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.played
}

func (s *ScenarioMediaStream) Send(ctx context.Context, chunk providers.AudioChunk) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := chunk.Validate(); err != nil {
		return err
	}
	if chunk.Format != providers.TelephonyFormat {
		return providers.ErrUnsupportedFormat
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.hungUp || s.ctx.Err() != nil {
		return providers.ErrClosed
	}
	if s.queued+len(chunk.Data) > maxQueueBytes {
		return fmt.Errorf("%w: outbound audio queue is full", providers.ErrInvalid)
	}
	s.outbound = append(s.outbound, outboundItem{audio: append([]byte(nil), chunk.Data...)})
	s.queued += len(chunk.Data)
	s.signal()
	return nil
}

func (s *ScenarioMediaStream) Mark(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" || len(name) > 64 {
		return fmt.Errorf("%w: mark name", providers.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.hungUp || s.ctx.Err() != nil {
		return providers.ErrClosed
	}
	s.outbound = append(s.outbound, outboundItem{mark: name})
	s.signal()
	return nil
}

func (s *ScenarioMediaStream) Clear(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.hungUp || s.ctx.Err() != nil {
		return 0, providers.ErrClosed
	}
	dropped := s.queued
	s.outbound, s.queued = nil, 0
	return dropped, nil
}

// Close detaches the stream. No callback starts after Close returns; if Close is
// called from inside the callback, the worker stops as soon as that callback returns.
func (s *ScenarioMediaStream) Close() error {
	s.detach(true)
	return nil
}

func (s *ScenarioMediaStream) detach(wait bool) {
	s.mu.Lock()
	s.closed = true
	inCallback := s.inCallback
	s.mu.Unlock()
	s.signal()
	if wait && !inCallback {
		<-s.done
	}
	s.owner.mu.Lock()
	if s.call.stream == s {
		s.call.stream = nil
	}
	s.owner.mu.Unlock()
}

// hangUp makes stop the final event and rejects further outbound audio.
func (s *ScenarioMediaStream) hangUp() {
	s.mu.Lock()
	s.hungUp = true
	s.outbound, s.queued, s.inbound = nil, 0, nil
	s.mu.Unlock()
	s.signal()
}

func (s *ScenarioMediaStream) run(ctx context.Context) {
	defer close(s.done)
	for {
		event, ok, last := s.next()
		if last && !ok {
			return
		}
		if ok {
			if !s.deliver(ctx, event) || last {
				return
			}
			continue
		}
		select {
		case <-ctx.Done():
			s.detach(false)
			return
		case <-s.wake:
		}
	}
}

// next advances playback by one step and returns the event to deliver, if any.
func (s *ScenarioMediaStream) next() (event providers.MediaEvent, ok, last bool) {
	if s.opts.FrameDelay > 0 {
		time.Sleep(s.opts.FrameDelay)
	}
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.closed:
		return event, false, true
	case s.hungUp:
		s.closed = true
		return providers.MediaEvent{Kind: providers.MediaStop}, true, true
	}
	// Outbound playback: one frame per step; a mark is reported when reached.
	if len(s.outbound) > 0 {
		item := s.outbound[0]
		if item.mark != "" {
			s.outbound = s.outbound[1:]
			// The business answers once the assistant has finished speaking.
			if s.call.stream == s {
				s.mu.Unlock()
				s.owner.queueBusinessLocked(s.call, s)
				s.mu.Lock()
			}
			return providers.MediaEvent{Kind: providers.MediaMark, Mark: item.mark}, true, false
		}
		n := min(len(item.audio), providers.TelephonyFormat.BytesPerFrame())
		s.played += n
		s.queued -= n
		if n == len(item.audio) {
			s.outbound = s.outbound[1:]
		} else {
			s.outbound[0].audio = item.audio[n:]
		}
		if len(s.inbound) == 0 {
			s.signal() // keep playing
		}
	}
	if len(s.inbound) > 0 {
		frame := s.inbound[0]
		s.inbound = s.inbound[1:]
		s.inboundSeq++
		s.signal()
		return providers.MediaEvent{Kind: providers.MediaAudio, Audio: providers.AudioChunk{
			Format: providers.TelephonyFormat, Seq: s.inboundSeq, Data: frame}}, true, false
	}
	return event, false, false
}

func (s *ScenarioMediaStream) deliver(ctx context.Context, event providers.MediaEvent) bool {
	if s.opts.pace(ctx) != nil {
		s.detach(false)
		return false
	}
	s.mu.Lock()
	if s.closed && event.Kind != providers.MediaStop {
		s.mu.Unlock()
		return false
	}
	s.inCallback = true
	s.mu.Unlock()
	err := s.emit(event)
	s.mu.Lock()
	s.inCallback = false
	s.mu.Unlock()
	if err != nil {
		s.detach(false)
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Transcriber.

// ScenarioTranscriber decodes the mock telephony's marked frames back into the
// scenario's business turns: one update per FramesPerUpdate frames, final last.
type ScenarioTranscriber struct {
	script *script
	opts   Options
}

func (t *ScenarioTranscriber) Open(ctx context.Context, open providers.TranscriptOpen, emit func(providers.TranscriptUpdate) error) (providers.TranscriptSession, error) {
	if emit == nil || !providers.ValidUUID(open.CallID) {
		return nil, fmt.Errorf("%w: transcript open", providers.ErrInvalid)
	}
	if err := open.Format.Validate(); err != nil {
		return nil, err
	}
	if open.Format != providers.TelephonyFormat {
		return nil, providers.ErrUnsupportedFormat
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &transcriptSession{t: t, ctx: ctx, emit: emit, finals: map[int]bool{}}, nil
}

type transcriptSession struct {
	t    *ScenarioTranscriber
	ctx  context.Context
	emit func(providers.TranscriptUpdate) error

	// push serializes Push calls, so callbacks never overlap. mu guards closed only,
	// so Close may be called from inside the callback.
	push   sync.Mutex
	mu     sync.Mutex
	closed bool
	finals map[int]bool
}

func (s *transcriptSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *transcriptSession) Push(ctx context.Context, chunk providers.AudioChunk) error {
	s.push.Lock()
	defer s.push.Unlock()
	if s.isClosed() {
		return providers.ErrClosed
	}
	if err := s.ctx.Err(); err != nil {
		_ = s.Close()
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := chunk.Validate(); err != nil {
		return err
	}
	if chunk.Format != providers.TelephonyFormat {
		return providers.ErrUnsupportedFormat
	}
	index, frame, total, ok := readFrame(chunk.Data)
	if !ok || index >= len(s.t.script.business) || s.finals[index] {
		return nil // silence, assistant echo or an already finalized turn
	}
	turn := s.t.script.business[index]
	per := s.t.opts.FramesPerUpdate
	if (frame+1)%per != 0 {
		return nil
	}
	update := providers.TranscriptUpdate{TurnID: turn.id, StartedAtMS: turn.started}
	if step := (frame + 1) / per; step <= len(turn.partials) && frame+1 < total {
		update.Text = turn.partials[step-1]
	} else if frame+1 == total {
		update.Text, update.IsFinal, update.EndedAtMS = turn.final, true, turn.ended
		s.finals[index] = true
	} else {
		return nil
	}
	if err := s.t.opts.pace(s.ctx); err != nil {
		_ = s.Close()
		return err
	}
	if s.isClosed() {
		return providers.ErrClosed
	}
	if err := s.emit(update); err != nil {
		_ = s.Close()
		return err
	}
	return nil
}

func (s *transcriptSession) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

// ---------------------------------------------------------------------------
// Responder.

// ScenarioResponder returns the scenario's suggestion for the request's finalized
// business turns and applied instructions, streamed a few words at a time.
type ScenarioResponder struct {
	script *script
	opts   Options
}

func (r *ScenarioResponder) Generate(ctx context.Context, req providers.ResponseRequest, emit func(providers.ResponseDraft) error) error {
	if emit == nil {
		return fmt.Errorf("%w: nil draft callback", providers.ErrInvalid)
	}
	if err := req.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	business := 0
	for _, turn := range req.Transcript {
		if turn.Speaker == providers.SpeakerBusiness {
			business++
		}
	}
	text := ""
	for _, reply := range r.script.replies {
		if reply.BusinessTurns == business && equalStrings(reply.Instructions, req.Instructions) {
			text = reply.Text
			break
		}
	}
	if text == "" {
		return ErrNoScriptedReply
	}
	words := strings.Fields(text)
	for i := 3; ; i += 3 {
		final := i >= len(words)
		draft := text
		if !final {
			draft = strings.Join(words[:i], " ")
		}
		if err := r.opts.pace(ctx); err != nil {
			return err
		}
		if err := emit(providers.ResponseDraft{Text: draft, ContextRevision: req.ContextRevision, Final: final}); err != nil {
			return err
		}
		if final {
			return nil
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Synthesizer.

// ScenarioSynthesizer produces deterministic placeholder mu-law audio, about as long
// as the text would take to say (70 ms per character), in 200 ms chunks.
type ScenarioSynthesizer struct {
	opts Options
}

const (
	speechMSPerChar = 70
	framesPerChunk  = 10
)

func (s *ScenarioSynthesizer) Stream(ctx context.Context, req providers.SpeechRequest, emit func(providers.SpeechChunk) error) error {
	if emit == nil {
		return fmt.Errorf("%w: nil speech callback", providers.ErrInvalid)
	}
	if err := req.Validate(); err != nil {
		return err
	}
	if req.Format != providers.TelephonyFormat {
		return providers.ErrUnsupportedFormat
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f := providers.TelephonyFormat
	frames := max(1, len([]rune(req.Text))*speechMSPerChar/providers.FrameMS)
	chunks := (frames + framesPerChunk - 1) / framesPerChunk
	seed := byte(len(req.Text))
	for i := range chunks {
		n := min(framesPerChunk, frames-i*framesPerChunk) * f.BytesPerFrame()
		data := make([]byte, n)
		for j := range data {
			// Quiet, varying mu-law values; never the inbound frame marker.
			data[j] = 0xF0 | (seed+byte(i*7+j))&0x0F
		}
		if err := s.opts.pace(ctx); err != nil {
			return err
		}
		if err := emit(providers.SpeechChunk{Audio: providers.AudioChunk{Format: f, Seq: uint64(i + 1), Data: data},
			Token: req.Token, Last: i == chunks-1}); err != nil {
			return err
		}
	}
	return nil
}
