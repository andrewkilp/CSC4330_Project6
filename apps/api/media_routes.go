package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/webhooks"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/telephony/media"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/transcription"
)

// The Twilio media bridge (#21) is mounted only when every piece it needs is
// configured: verified callbacks, a stream secret, durable storage that can bind a
// stream to its provider call, and a speech-to-text key. A partial configuration is a
// startup error rather than a silently unauthenticated or deaf stream.

const minMediaSecret = 32

// ActiveCallLookup is the storage the media authorizer needs (repository.Repository).
type ActiveCallLookup interface {
	ActiveProviderCallID(ctx context.Context, callID string) (string, error)
}

// MediaSink receives live call audio events. The conversation runtime (#41)
// implements it; until one is set, transcripts and events are discarded, never stored.
type MediaSink interface {
	Transcript(callID string, u providers.TranscriptUpdate) error
	Event(callID string, e providers.MediaEvent)
}

// liveMedia tracks each live call's outbound stream so call controls (#31) and the
// runtime can clear or close it.
type liveMedia struct {
	mu      sync.Mutex
	streams map[string]providers.MediaStream
	sink    MediaSink
}

func newLiveMedia() *liveMedia { return &liveMedia{streams: map[string]providers.MediaStream{}} }

func (l *liveMedia) SetSink(sink MediaSink) {
	l.mu.Lock()
	l.sink = sink
	l.mu.Unlock()
}

// Stream returns the call's current outbound stream, if it is attached.
func (l *liveMedia) Stream(callID string) (providers.MediaStream, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.streams[callID]
	return s, ok
}

func (l *liveMedia) attach(callID string, out providers.MediaStream) {
	l.mu.Lock()
	previous := l.streams[callID]
	l.streams[callID] = out
	l.mu.Unlock()
	if previous != nil && previous != out {
		_ = previous.Close()
	}
}

func (l *liveMedia) transcript(callID string, u providers.TranscriptUpdate) error {
	l.mu.Lock()
	sink := l.sink
	l.mu.Unlock()
	if sink == nil {
		return nil
	}
	return sink.Transcript(callID, u)
}

func (l *liveMedia) event(callID string, e providers.MediaEvent) {
	l.mu.Lock()
	sink := l.sink
	if e.Kind == providers.MediaStop {
		delete(l.streams, callID)
	}
	l.mu.Unlock()
	if sink != nil {
		sink.Event(callID, e)
	}
}

func (l *liveMedia) config(secret []byte, lookup ActiveCallLookup, stt providers.Transcriber) media.Config {
	return media.Config{
		Authorizer:   media.TokenAuthorizer{Secret: secret, ProviderCallID: lookup.ActiveProviderCallID},
		Transcriber:  stt,
		OnStart:      l.attach,
		OnTranscript: l.transcript,
		OnEvent:      l.event,
	}
}

// mediaEnvironment reads MEDIA_STREAM_SECRET and DEEPGRAM_API_KEY. Without a secret
// the bridge stays unmounted and verified voice callbacks hang up safely.
func mediaEnvironment(callbacksEnabled bool, store any) (secret []byte, lookup ActiveCallLookup, stt providers.Transcriber, enabled bool, err error) {
	raw := os.Getenv("MEDIA_STREAM_SECRET")
	if raw == "" {
		return nil, nil, nil, false, nil
	}
	if !callbacksEnabled || len(raw) < minMediaSecret {
		return nil, nil, nil, false, errors.New("invalid media stream configuration")
	}
	lookup, ok := store.(ActiveCallLookup)
	if !ok || nilStore(store) {
		return nil, nil, nil, false, errors.New("media streams require durable call storage")
	}
	client, err := transcription.New(transcription.Config{APIKey: os.Getenv("DEEPGRAM_API_KEY")})
	if err != nil {
		return nil, nil, nil, false, errors.New("media streams require a configured transcriber")
	}
	return []byte(raw), lookup, client, true, nil
}

func registerMedia(mux *http.ServeMux, live *liveMedia, secret []byte, lookup ActiveCallLookup, stt providers.Transcriber) {
	webhooks.RegisterMedia(mux, live.config(secret, lookup, stt))
}
