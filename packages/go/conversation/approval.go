package conversation

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
)

var (
	ErrApprovalConflict = errors.New("approval conflicts with current response")
	ErrResponseNotFound = errors.New("response not found")
	ErrApprovalLimit    = errors.New("approval journal is full")
	uuidInput           = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// ApprovalError identifies a contract field without retaining user text.
type ApprovalError struct {
	Kind  error
	Field string
}

func (e *ApprovalError) Error() string { return e.Kind.Error() }
func (e *ApprovalError) Unwrap() error { return e.Kind }

type Approval struct {
	ResponseID      string
	ContextRevision uint64
	EditedText      *string
	IdempotencyKey  string
}

type approvalRecord struct {
	responseID string
	revision   uint64
	edited     bool
	text       string
	reply      Reply
}

// CallID is immutable. Authorization belongs to the owner-scoped API boundary.
func (l *Loop) CallID() string { return l.callID }

// ApproveResponse atomically validates and records one approval. A retry returns
// its original acknowledgement, never a fresh synthesis authorization. The local
// journal is bounded and process-local; persistent deduplication belongs to #18/#28.
func (l *Loop) ApproveResponse(ctx context.Context, a Approval) (Reply, error) {
	if err := ctx.Err(); err != nil {
		return Reply{}, err
	}
	if !uuidInput.MatchString(a.ResponseID) {
		return Reply{}, &ApprovalError{ErrInvalidInput, "response_id"}
	}
	if !uuidInput.MatchString(a.IdempotencyKey) {
		return Reply{}, &ApprovalError{ErrInvalidInput, "idempotency_key"}
	}
	if a.ContextRevision > 1<<53-1 {
		return Reply{}, &ApprovalError{ErrInvalidInput, "context_revision"}
	}
	text := ""
	if a.EditedText != nil {
		text = *a.EditedText
		if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || len(utf16.Encode([]rune(text))) > 1500 {
			return Reply{}, &ApprovalError{ErrInvalidInput, "edited_text"}
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Reply{}, err
	}
	key := strings.ToLower(a.IdempotencyKey)
	id := strings.ToLower(a.ResponseID)
	if old, ok := l.approvals[key]; ok {
		if old.responseID != id || old.revision != a.ContextRevision || old.edited != (a.EditedText != nil) || old.text != text {
			return Reply{}, &ApprovalError{ErrApprovalConflict, "idempotency_key"}
		}
		return old.reply, nil
	}
	if len(l.approvals) >= 128 || len(l.events) >= 4094 {
		return Reply{}, ErrApprovalLimit
	}
	if a.ContextRevision != l.state.Snapshot().ContextRevision {
		return Reply{}, &ApprovalError{ErrApprovalConflict, "context_revision"}
	}
	if id != l.reply.ID {
		for _, e := range l.events {
			if e.Payload.ID == id {
				field := "status"
				if e.ContextRevision != a.ContextRevision {
					field = "context_revision"
				}
				return Reply{}, &ApprovalError{ErrApprovalConflict, field}
			}
		}
		return Reply{}, ErrResponseNotFound
	}
	s := l.state.Snapshot()
	if s.Response == nil || l.reply.Status != Suggested {
		return Reply{}, &ApprovalError{ErrApprovalConflict, "status"}
	}
	next, err := l.state.Approve(id, s.Response.Token, a.ContextRevision)
	if err != nil {
		return Reply{}, &ApprovalError{ErrApprovalConflict, "status"}
	}
	l.state = next
	if a.EditedText != nil {
		l.reply.Text = text
	}
	l.reply.Status = Approved
	l.emitLocked()
	if l.approvals == nil {
		l.approvals = make(map[string]approvalRecord)
	}
	l.approvals[key] = approvalRecord{id, a.ContextRevision, a.EditedText != nil, text, l.reply}
	return l.reply, nil
}

// SynthesizeApproved is a server-only handoff, not an arbitrary-text endpoint.
// It can claim the current approved response only once. Pause, instructions,
// interruption and end cancel it. It does not mark speech played or create an
// assistant turn: #30 owns playback acknowledgements. Downstream queues must
// fence each chunk's token again, including an in-flight callback on cancellation.
func (l *Loop) SynthesizeApproved(parent context.Context, responseID string, synth providers.Synthesizer, voice string, format providers.AudioFormat, emit func(providers.SpeechChunk) error) error {
	if synth == nil || emit == nil {
		return ErrInvalidInput
	}
	if err := parent.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	s := l.state.Snapshot()
	if s.Response == nil || l.reply.ID != responseID || l.reply.Status != Approved || l.speechClaimed || l.state.checkResponse(responseID, s.Response.Token) != nil {
		l.mu.Unlock()
		return ErrApprovalConflict
	}
	token := s.Response.Token
	req := providers.SpeechRequest{Text: l.reply.Text, Voice: voice, Format: format, Token: providers.Token{ContextRevision: token.ContextRevision, Generation: token.Generation}}
	if err := req.Validate(); err != nil {
		l.mu.Unlock()
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	l.speechCancel = cancel
	l.speechClaimed = true
	l.mu.Unlock()
	defer cancel()
	var callbackErr error
	var lastSeq uint64
	sawLast := false
	finished := false
	err := synth.Stream(ctx, req, func(chunk providers.SpeechChunk) error {
		l.mu.Lock()
		if finished {
			l.mu.Unlock()
			return ErrStaleGeneration
		}
		if callbackErr != nil {
			err := callbackErr
			l.mu.Unlock()
			return err
		}
		current := l.state.checkResponse(responseID, token) == nil && l.reply.Status == Approved
		if err := ctx.Err(); err != nil {
			l.mu.Unlock()
			return err
		}
		if !current {
			l.mu.Unlock()
			return ErrStaleGeneration
		}
		if sawLast || chunk.Audio.Seq != lastSeq+1 || chunk.Token != req.Token || chunk.Audio.Format != format || chunk.Audio.Validate() != nil {
			callbackErr = ErrInvalidInput
			l.mu.Unlock()
			return ErrInvalidInput
		}
		lastSeq = chunk.Audio.Seq
		sawLast = chunk.Last
		l.mu.Unlock()
		err := emit(chunk)
		if err != nil {
			l.mu.Lock()
			callbackErr = err
			l.mu.Unlock()
		}
		return err
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	finished = true
	if l.state.checkResponse(responseID, token) != nil {
		return ErrStaleGeneration
	}
	l.speechCancel = nil
	if callbackErr != nil {
		err = callbackErr
	}
	if err == nil && !sawLast {
		err = ErrInvalidInput
	}
	if err != nil || ctx.Err() != nil {
		l.state, _ = l.state.FailResponse(responseID, token)
		l.reply.Status = ResponseFailed
		l.reply.Text = ""
		l.emitLocked()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}
