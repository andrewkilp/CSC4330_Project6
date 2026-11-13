package conversation

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Remote interruption (#26). When the business starts talking over the assistant,
// the runtime clears the queued audio and calls InterruptPlayback with how much of the
// response's audio had actually played (from acknowledged playback marks). The
// response is cancelled like any interruption, so its generation token is retired and
// a late mark can never complete or restart it, and the caller receives the words
// that were really heard. Those words become the assistant's transcript turn.

// Interruption is the outcome of InterruptPlayback.
type Interruption struct {
	// Reply is the response as it was when interrupted, before cancellation.
	Reply Reply
	// Spoken is the part of Reply.Text that played, cut at a word boundary. It never
	// includes a word that had not finished playing; it is empty if nothing played.
	Spoken string
}

// SpokenPortion returns the leading words of text that fit in the played share of its
// audio (played of queued bytes). Speech is assumed to be evenly paced across the text,
// and the result is rounded down to a whole word so it never overstates what was said.
func SpokenPortion(text string, played, queued int) string {
	if played <= 0 || queued <= 0 {
		return ""
	}
	if played >= queued {
		return text
	}
	budget := utf8.RuneCountInString(text) * played / queued
	cut, n := len(text), 0
	for i := range text {
		if n == budget {
			cut = i
			break
		}
		n++
	}
	prefix := text[:cut]
	// A word cut off mid-way did not finish playing: drop it.
	if next, _ := utf8.DecodeRuneInString(text[cut:]); cut < len(text) && !unicode.IsSpace(next) {
		if j := strings.LastIndexFunc(prefix, unicode.IsSpace); j >= 0 {
			prefix = prefix[:j]
		} else {
			prefix = ""
		}
	}
	return strings.TrimRightFunc(prefix, unicode.IsSpace)
}

// InterruptPlayback cancels responseID because remote speech started while it was
// approved or speaking. played and queued are the acknowledged and sent audio bytes.
// It returns ErrStaleGeneration if that response is no longer the one playing.
func (l *Loop) InterruptPlayback(responseID string, played, queued int) (Interruption, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.state.Snapshot()
	if s.Response == nil || l.reply.ID != responseID || (l.reply.Status != Approved && l.reply.Status != Speaking) {
		return Interruption{}, ErrStaleGeneration
	}
	out := Interruption{Reply: l.reply, Spoken: SpokenPortion(l.reply.Text, played, queued)}
	next, err := l.state.Interrupt()
	if err != nil {
		return Interruption{}, err
	}
	l.syncLocked(next)
	return out, nil
}
