package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type ApprovalStore interface {
	Update(context.Context, string, string, string, string, any, func(*repository.Tx) (any, error)) (json.RawMessage, error)
}

// DurableApprovals serializes with repository revision/lifecycle mutations.
// No transaction performs vendor I/O or mutates an in-memory conversation loop.
type DurableApprovals struct {
	Store   ApprovalStore
	Publish func(...repository.Event)
}

func validateDurableApproval(a Approval) error {
	if !uuidInput.MatchString(a.ResponseID) {
		return &ApprovalError{ErrInvalidInput, "response_id"}
	}
	if !uuidInput.MatchString(a.IdempotencyKey) {
		return &ApprovalError{ErrInvalidInput, "idempotency_key"}
	}
	if a.ContextRevision > 1<<53-1 {
		return &ApprovalError{ErrInvalidInput, "context_revision"}
	}
	if a.EditedText != nil && (!utf8.ValidString(*a.EditedText) || strings.TrimSpace(*a.EditedText) == "" || len(utf16.Encode([]rune(*a.EditedText))) > 1500) {
		return &ApprovalError{ErrInvalidInput, "edited_text"}
	}
	return nil
}
func (s DurableApprovals) Approve(ctx context.Context, owner, callID string, a Approval) (repository.Response, error) {
	if !uuidInput.MatchString(owner) || !uuidInput.MatchString(callID) {
		return repository.Response{}, ErrResponseNotFound
	}
	if err := validateDurableApproval(a); err != nil {
		return repository.Response{}, err
	}
	if s.Store == nil {
		return repository.Response{}, repository.ErrUnavailable
	}
	a.ResponseID = strings.ToLower(a.ResponseID)
	a.IdempotencyKey = strings.ToLower(a.IdempotencyKey)
	request := struct {
		ResponseID string  `json:"response_id"`
		Revision   uint64  `json:"context_revision"`
		Edited     *string `json:"edited_text,omitempty"`
	}{a.ResponseID, a.ContextRevision, a.EditedText}
	var rejection error
	var committed []repository.Event
	result, err := s.Store.Update(ctx, strings.ToLower(owner), strings.ToLower(callID), "approve_response", a.IdempotencyKey, request, func(tx *repository.Tx) (any, error) {
		reject := func(field string) (any, error) {
			rejection = &ApprovalError{ErrApprovalConflict, field}
			return nil, repository.ErrConflict
		}
		call := tx.Call()
		if call.ContextRevision != int64(a.ContextRevision) {
			return reject("context_revision")
		}
		response, e := tx.Response(a.ResponseID)
		if e != nil {
			return nil, e
		}
		if response.ContextRevision != call.ContextRevision {
			return reject("context_revision")
		}
		if call.Status != "connected" || response.Status != "suggested" {
			return reject("status")
		}
		current, interrupted, e := tx.ResponseCheckpoint()
		if e != nil {
			return nil, e
		}
		if current != response.ID || interrupted {
			return reject("status")
		}
		all, e := tx.Responses()
		if e != nil {
			return nil, e
		}
		for _, other := range all {
			if other.ID != response.ID && other.ContextRevision == call.ContextRevision && (other.Status == "generating" || other.Status == "suggested" || other.Status == "approved" || other.Status == "speaking") {
				return reject("status")
			}
		}
		if a.EditedText != nil {
			response.Text = *a.EditedText
		}
		response.Status = "approved"
		if e = tx.PutResponse(response); e != nil {
			return nil, e
		}
		event, e := tx.AppendEvent("response.updated", response)
		if e != nil {
			return nil, e
		}
		committed = append(committed, event)
		return response, nil
	})
	if err != nil {
		if rejection != nil {
			return repository.Response{}, rejection
		}
		if errors.Is(err, repository.ErrConflict) {
			return repository.Response{}, &ApprovalError{ErrApprovalConflict, "idempotency_key"}
		}
		if errors.Is(err, repository.ErrNotFound) {
			return repository.Response{}, ErrResponseNotFound
		}
		return repository.Response{}, err
	}
	var response repository.Response
	if json.Unmarshal(result, &response) != nil {
		return repository.Response{}, repository.ErrUnavailable
	}
	if len(committed) > 0 && s.Publish != nil {
		s.Publish(committed...)
	}
	return response, nil
}

// ClaimForSynthesis is server-only: it accepts no text, atomically claims the
// stored approved response once, and returns only that stored text to a worker.
// Speaking is a pending playback state, not a spoken-turn acknowledgement (#30).
// A failed/uncertain worker must not automatically repeat a claimed synthesis.
func (s DurableApprovals) ClaimForSynthesis(ctx context.Context, owner, callID, responseID string) (repository.Response, error) {
	if s.Store == nil {
		return repository.Response{}, repository.ErrUnavailable
	}
	var committed []repository.Event
	var rejected bool
	data, err := s.Store.Update(ctx, owner, callID, "claim_approved_speech", repository.NewID(), map[string]string{"response_id": responseID}, func(tx *repository.Tx) (any, error) {
		response, e := tx.Response(responseID)
		if e != nil {
			return nil, e
		}
		call := tx.Call()
		current, interrupted, e := tx.ResponseCheckpoint()
		if e != nil {
			return nil, e
		}
		if call.Status != "connected" || response.Status != "approved" || response.ContextRevision != call.ContextRevision || current != responseID || interrupted {
			rejected = true
			return nil, repository.ErrConflict
		}
		response.Status = "speaking"
		if e = tx.PutResponse(response); e != nil {
			return nil, e
		}
		event, e := tx.AppendEvent("response.updated", response)
		if e != nil {
			return nil, e
		}
		committed = append(committed, event)
		return response, nil
	})
	if err != nil {
		if rejected {
			return repository.Response{}, ErrApprovalConflict
		}
		return repository.Response{}, err
	}
	var response repository.Response
	if json.Unmarshal(data, &response) != nil {
		return repository.Response{}, repository.ErrUnavailable
	}
	if s.Publish != nil {
		s.Publish(committed...)
	}
	return response, nil
}
