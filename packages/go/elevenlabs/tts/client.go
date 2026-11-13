// Package tts implements ElevenLabs HTTP streaming behind providers.Synthesizer.
// The caller must approve and revision-fence text before calling Stream and must
// check each chunk's token before playback. This package never places a call.
package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
)

const (
	// 200 ms of raw 8 kHz mono mu-law, below providers.MaxChunkBytes.
	ChunkBytes = 1600
	// MaxAudioBytes caps each reply at three minutes, including the final chunk.
	MaxAudioBytes  = 8000 * 180
	defaultTimeout = 30 * time.Second
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

// Config is server-owned. No environment is read and no provider is selected
// implicitly. Model and voice must be chosen explicitly for the account.
type Config struct {
	APIKey  string
	VoiceID string
	Model   string
	// Timeout bounds the entire response, including stalled body reads. Zero is 30s.
	Timeout time.Duration
}

type Client struct {
	config Config
	http   *http.Client
}

var _ providers.Synthesizer = (*Client)(nil)

func New(config Config) (*Client, error) {
	if config.Timeout == 0 {
		config.Timeout = defaultTimeout
	}
	if strings.TrimSpace(config.APIKey) == "" || strings.IndexFunc(config.APIKey, func(r rune) bool { return r < 33 || r > 126 }) >= 0 ||
		!idPattern.MatchString(config.VoiceID) || !idPattern.MatchString(config.Model) || config.Timeout < time.Millisecond || config.Timeout > time.Minute {
		return nil, fmt.Errorf("%w: synthesis configuration", providers.ErrInvalid)
	}
	return &Client{config: config, http: &http.Client{Timeout: config.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Stream makes one request, with no automatic retries that could duplicate paid
// synthesis or playback. Errors never include vendor bodies or credentials.
func (c *Client) Stream(ctx context.Context, req providers.SpeechRequest, emit func(providers.SpeechChunk) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || c.http == nil || emit == nil {
		return fmt.Errorf("%w: synthesis callback or client", providers.ErrInvalid)
	}
	if err := req.Validate(); err != nil {
		return err
	}
	if req.Format != (providers.AudioFormat{Encoding: providers.EncodingMulaw, SampleRate: 8000, Channels: 1}) {
		return fmt.Errorf("%w: synthesis requires 8 kHz mono mu-law", providers.ErrUnsupportedFormat)
	}
	if req.Voice != c.config.VoiceID || !utf8.ValidString(req.Text) || len(utf16.Encode([]rune(req.Text))) > providers.MaxSpeechText {
		return fmt.Errorf("%w: synthesis text or configured voice", providers.ErrInvalid)
	}
	body, err := json.Marshal(struct {
		Text  string `json:"text"`
		Model string `json:"model_id"`
	}{req.Text, c.config.Model})
	if err != nil {
		return fmt.Errorf("%w: synthesis text", providers.ErrInvalid)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.elevenlabs.io/v1/text-to-speech/"+c.config.VoiceID+"/stream?output_format=ulaw_8000", bytes.NewReader(body))
	if err != nil {
		return providers.ErrInvalid
	}
	request.Header.Set("xi-api-key", c.config.APIKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/octet-stream")
	res, err := c.http.Do(request)
	if err != nil {
		return failure(ctx)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return failure(ctx)
	}
	if !rawMulawContentType(res.Header.Get("Content-Type")) {
		return fmt.Errorf("%w: synthesis response content type", providers.ErrUnsupportedFormat)
	}
	if res.ContentLength > MaxAudioBytes {
		return providers.ErrUnavailable
	}
	// Hold at most one chunk to mark Last only after successful EOF. On a read
	// error pending audio is dropped and no successful completion is emitted.
	var pending []byte
	var seq uint64
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		data := make([]byte, ChunkBytes)
		n, readErr := readChunk(ctx, res.Body, data)
		if err := ctx.Err(); err != nil {
			return err
		}
		if readErr != nil && readErr != io.EOF {
			return failure(ctx)
		}
		total += n
		if total > MaxAudioBytes {
			return providers.ErrUnavailable
		}
		// A clean transport EOF must also agree with an advertised length.
		if res.ContentLength >= 0 && (int64(total) > res.ContentLength || (readErr != nil && int64(total) != res.ContentLength)) {
			return providers.ErrUnavailable
		}
		if n > 0 {
			if len(pending) > 0 {
				seq++
				if err := emit(providers.SpeechChunk{Audio: providers.AudioChunk{Format: req.Format, Seq: seq, Data: pending}, Token: req.Token}); err != nil {
					return err
				}
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			pending = data[:n]
		}
		if readErr != nil {
			if len(pending) == 0 {
				return providers.ErrUnavailable
			}
			seq++
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := emit(providers.SpeechChunk{Audio: providers.AudioChunk{Format: req.Format, Seq: seq, Data: pending}, Token: req.Token, Last: true}); err != nil {
				return err
			}
			return ctx.Err()
		}
	}
}

// Preserve the body's actual framing errors. io.ReadFull would turn a clean
// short final read into ErrUnexpectedEOF, making it indistinguishable from a
// truncated Content-Length or chunked HTTP response.
func readChunk(ctx context.Context, body io.Reader, data []byte) (int, error) {
	total, emptyReads := 0, 0
	for total < len(data) {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := body.Read(data[total:])
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			emptyReads++
			if emptyReads >= 100 {
				return total, io.ErrNoProgress
			}
		} else {
			emptyReads = 0
		}
	}
	return total, nil
}

func failure(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return providers.ErrUnavailable
}

func rawMulawContentType(value string) bool {
	media, params, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	if media != "application/octet-stream" && media != "audio/basic" && media != "audio/x-mulaw" && media != "audio/mulaw" {
		return false
	}
	for name, value := range params {
		if (name == "rate" && value != "8000") || (name == "channels" && value != "1") {
			return false
		}
	}
	return true
}
