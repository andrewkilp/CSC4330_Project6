package media

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// ErrStaleMark reports a playback mark that no longer counts: its audio was cleared by
// an interruption, pause or end, or it is unknown, repeated or out of order.
var ErrStaleMark = errors.New("stale playback mark")

var playbackID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,48}$`)

// Playback tracks one response's outbound audio chunk by chunk (#26). Each queued
// chunk gets its own mark, named "<responseID>.<n>". Twilio reports marks in buffer
// order as the audio before them finishes playing, so the acknowledged marks say
// exactly how many bytes were heard.
//
// Clear freezes the ledger: every outstanding mark becomes stale, so a late mark for
// cleared audio can never finish or restart the cancelled speech.
type Playback struct {
	mu       sync.Mutex
	id       string
	marks    []int // cumulative queued bytes at each mark
	queued   int
	acked    int // marks acknowledged
	finished bool
	cleared  bool
}

func NewPlayback(responseID string) (*Playback, error) {
	if !playbackID.MatchString(responseID) {
		return nil, errors.New("invalid playback response id")
	}
	return &Playback{id: responseID}, nil
}

func (p *Playback) ResponseID() string { return p.id }

// Queue records a chunk of n bytes that was just sent and returns the mark to send
// right behind it.
func (p *Playback) Queue(n int) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cleared || p.finished || n <= 0 {
		return "", ErrStaleMark
	}
	p.queued += n
	p.marks = append(p.marks, p.queued)
	return p.id + "." + strconv.Itoa(len(p.marks)), nil
}

// Finish records that every chunk has been queued.
func (p *Playback) Finish() {
	p.mu.Lock()
	p.finished = true
	p.mu.Unlock()
}

// Acknowledge records a mark reported by the provider. It returns complete once the
// last chunk of a finished playback has played. Marks must arrive in order.
func (p *Playback) Acknowledge(name string) (complete bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	prefix, seq, ok := strings.Cut(name, ".")
	n, convErr := strconv.Atoi(seq)
	if !ok || prefix != p.id || convErr != nil || p.cleared || n != p.acked+1 || n > len(p.marks) {
		return false, ErrStaleMark
	}
	p.acked = n
	return p.finished && p.acked == len(p.marks), nil
}

// Clear stops counting: it returns the bytes heard (up to the last acknowledged mark)
// and the bytes queued, and makes every outstanding mark stale. Clearing twice returns
// the same frozen progress.
func (p *Playback) Clear() (played, queued int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cleared = true
	return p.playedLocked(), p.queued
}

// Progress returns the bytes heard so far and the bytes queued.
func (p *Playback) Progress() (played, queued int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.playedLocked(), p.queued
}

// Active reports whether audio may still be playing: not cleared and not complete.
func (p *Playback) Active() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.cleared && !(p.finished && p.acked == len(p.marks))
}

func (p *Playback) playedLocked() int {
	if p.acked == 0 {
		return 0
	}
	return p.marks[p.acked-1]
}
