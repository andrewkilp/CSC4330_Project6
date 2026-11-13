// Package transcription implements the selected server-only Deepgram Nova-3
// streaming adapter. Only the call composition layer may open a real provider.
package transcription

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
	"github.com/coder/websocket"
)

const endpoint = "wss://api.deepgram.com/v1/listen"

type Config struct{ APIKey string }
type Client struct {
	key      string
	endpoint string
	http     *http.Client
}

var _ providers.Transcriber = (*Client)(nil)

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" || len(cfg.APIKey) > 1024 || strings.ContainsAny(cfg.APIKey, "\r\n") {
		return nil, providers.ErrInvalid
	}
	return &Client{key: cfg.APIKey, endpoint: endpoint, http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) Open(ctx context.Context, open providers.TranscriptOpen, emit func(providers.TranscriptUpdate) error) (providers.TranscriptSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !providers.ValidUUID(open.CallID) || emit == nil {
		return nil, providers.ErrInvalid
	}
	if open.Format != providers.TelephonyFormat {
		return nil, providers.ErrUnsupportedFormat
	}
	u, err := url.Parse(c.endpoint)
	if err != nil {
		return nil, providers.ErrUnavailable
	}
	q := u.Query()
	for k, v := range map[string]string{"model": "nova-3", "language": "en-US", "encoding": "mulaw", "sample_rate": "8000", "channels": "1", "interim_results": "true", "endpointing": "300", "punctuate": "true", "mip_opt_out": "true"} {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	conn, res, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPClient: c.http, HTTPHeader: http.Header{"Authorization": []string{"Token " + c.key}}, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		if res != nil && res.Body != nil {
			res.Body.Close()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, providers.ErrUnavailable
	}
	conn.SetReadLimit(64 * 1024)
	listen, cancel := context.WithCancel(ctx)
	s := &session{ctx: listen, cancel: cancel, conn: conn, emit: emit, format: open.Format}
	go s.read()
	go s.keepAlive()
	return s, nil
}

type session struct {
	ctx      context.Context
	cancel   context.CancelFunc
	conn     *websocket.Conn
	emit     func(providers.TranscriptUpdate) error
	format   providers.AudioFormat
	mu       sync.Mutex
	closed   bool
	inFlight bool
	err      error
	write    sync.Mutex
	seq      uint64
}

func (s *session) stop(err error) {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.err = err
		s.cancel()
	}
	s.mu.Unlock()
	_ = s.conn.CloseNow()
}

// Close cancels rather than finalizes pending recognition, including when called
// inside emit. A callback already reserved for delivery may finish.
func (s *session) Close() error { s.stop(providers.ErrClosed); return nil }

func (s *session) status() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil || s.closed {
		if s.err != nil {
			return s.err
		}
		return providers.ErrClosed
	}
	return nil
}

// admit is the logical start of a serialized callback, sharing the same state
// lock as Close. Close must not wait for it: emit itself may call Close.
func (s *session) admit() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil || s.inFlight {
		return false
	}
	s.inFlight = true
	return true
}

func (s *session) finishDelivery() {
	s.mu.Lock()
	s.inFlight = false
	s.mu.Unlock()
}

func (s *session) Push(ctx context.Context, chunk providers.AudioChunk) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.status(); err != nil {
		return err
	}
	if chunk.Format != s.format {
		return providers.ErrUnsupportedFormat
	}
	if err := chunk.Validate(); err != nil {
		return err
	}
	s.write.Lock()
	defer s.write.Unlock()
	if err := s.status(); err != nil {
		return err
	}
	if chunk.Seq != s.seq+1 {
		return providers.ErrInvalid
	}
	write, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.conn.Write(write, websocket.MessageBinary, chunk.Data); err != nil {
		s.stop(providers.ErrUnavailable)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return providers.ErrUnavailable
	}
	s.seq = chunk.Seq
	return nil
}

func (s *session) keepAlive() {
	timer := time.NewTicker(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-s.ctx.Done():
			s.stop(providers.ErrClosed)
			return
		case <-timer.C:
			s.write.Lock()
			ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
			err := s.conn.Write(ctx, websocket.MessageText, []byte(`{"type":"KeepAlive"}`))
			cancel()
			s.write.Unlock()
			if err != nil {
				s.stop(providers.ErrUnavailable)
				return
			}
		}
	}
}

func (s *session) read() {
	var turns segmenter
	for {
		kind, data, err := s.conn.Read(s.ctx)
		if err != nil {
			if s.ctx.Err() != nil {
				s.stop(providers.ErrClosed)
			} else {
				s.stop(providers.ErrUnavailable)
			}
			return
		}
		if kind != websocket.MessageText {
			s.stop(providers.ErrUnavailable)
			return
		}
		u, err := turns.consume(data)
		if err != nil {
			s.stop(providers.ErrUnavailable)
			return
		}
		if u == nil {
			continue
		}
		if !s.admit() {
			return
		}
		err = s.emit(*u)
		s.finishDelivery()
		if err != nil {
			s.stop(providers.ErrClosed)
			return
		}
	}
}

type result struct {
	Type         string   `json:"type"`
	Start        *float64 `json:"start"`
	Duration     *float64 `json:"duration"`
	IsFinal      bool     `json:"is_final"`
	SpeechFinal  bool     `json:"speech_final"`
	ChannelIndex []int    `json:"channel_index"`
	Channel      struct {
		Alternatives []struct {
			Transcript string `json:"transcript"`
		} `json:"alternatives"`
	} `json:"channel"`
}

type segmenter struct {
	id, committed      string
	start, end         int64
	lastStart, lastEnd int64
	lastText           string
	hasFinal           bool
	finals             int
}

func (s *segmenter) consume(data []byte) (*providers.TranscriptUpdate, error) {
	var r result
	if json.Unmarshal(data, &r) != nil {
		return nil, providers.ErrUnavailable
	}
	switch r.Type {
	case "Metadata", "SpeechStarted", "UtteranceEnd":
		return nil, nil
	case "Results":
	default:
		return nil, providers.ErrUnavailable
	}
	if r.Start == nil || r.Duration == nil || math.IsNaN(*r.Start) || math.IsNaN(*r.Duration) || *r.Start < 0 || *r.Duration < 0 || *r.Start+*r.Duration > 24*3600 || len(r.Channel.Alternatives) != 1 || len(r.ChannelIndex) != 2 || r.ChannelIndex[0] != 0 || r.ChannelIndex[1] != 1 || (r.SpeechFinal && !r.IsFinal) {
		return nil, providers.ErrUnavailable
	}
	start, end := int64(math.Round(*r.Start*1000)), int64(math.Round((*r.Start+*r.Duration)*1000))
	text := strings.TrimSpace(r.Channel.Alternatives[0].Transcript)
	if s.hasFinal && start < s.lastEnd {
		if r.IsFinal && start == s.lastStart && end == s.lastEnd && text == s.lastText {
			if r.SpeechFinal && s.id != "" {
				u := &providers.TranscriptUpdate{TurnID: s.id, Text: s.committed, IsFinal: true, StartedAtMS: s.start, EndedAtMS: s.end}
				s.id = ""
				s.committed = ""
				s.end = 0
				return u, nil
			}
			return nil, nil
		}
		return nil, providers.ErrUnavailable
	}
	if text == "" && s.id == "" {
		return nil, nil
	}
	if s.id == "" {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, providers.ErrUnavailable
		}
		b[6] = (b[6] & 15) | 64
		b[8] = (b[8] & 63) | 128
		s.id = fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
		s.start = start
	}
	combined := strings.TrimSpace(s.committed + " " + text)
	if r.SpeechFinal && combined == "" {
		// An empty endpoint cannot promote an unconfirmed interim hypothesis.
		s.id = ""
		s.committed = ""
		s.end = 0
		return nil, nil
	}
	if len(utf16.Encode([]rune(combined))) > providers.MaxTurnText {
		return nil, providers.ErrUnavailable
	}
	if r.IsFinal {
		s.finals++
		if s.finals > 4096 {
			return nil, providers.ErrUnavailable
		}
		s.committed = combined
		s.lastStart = start
		s.lastEnd = end
		s.lastText = text
		s.hasFinal = true
	}
	if end > s.end {
		s.end = end
	}
	u := &providers.TranscriptUpdate{TurnID: s.id, Text: combined, IsFinal: r.SpeechFinal, StartedAtMS: s.start}
	if r.SpeechFinal {
		u.EndedAtMS = s.end
		s.id = ""
		s.committed = ""
		s.end = 0
	}
	return u, nil
}
