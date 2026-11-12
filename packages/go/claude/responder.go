package claude

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
)

// Responder adapts Client to the shared providers.Responder boundary (#6). It maps the
// request and drafts field by field and adds no behavior: approval, revision fencing
// and the decision to speak stay with the caller.
//
// Errors: invalid input wraps providers.ErrInvalid. Provider failures wrap
// providers.ErrUnavailable and keep this package's specific error (ErrRateLimited,
// ErrIncomplete, ErrLimit) for errors.Is. Context and callback errors are returned
// unchanged.
type Responder struct {
	Client *Client
}

var _ providers.Responder = Responder{}

func (r Responder) Generate(ctx context.Context, req providers.ResponseRequest, emit func(providers.ResponseDraft) error) error {
	if r.Client == nil || emit == nil {
		return fmt.Errorf("%w: responder not configured", providers.ErrInvalid)
	}
	if err := req.Validate(); err != nil {
		return err
	}
	if req.ContextRevision > math.MaxInt64 {
		return fmt.Errorf("%w: context revision", providers.ErrInvalid)
	}
	in := Input{Goal: req.Goal, Instructions: req.Instructions, ContextRevision: int64(req.ContextRevision)}
	for _, t := range req.Transcript {
		in.Transcript = append(in.Transcript, Turn{Speaker: string(t.Speaker), Text: t.Text})
	}
	var callbackErr error
	err := r.Client.Generate(ctx, in, func(u Update) error {
		callbackErr = emit(providers.ResponseDraft{Text: u.Text, ContextRevision: uint64(u.ContextRevision), Final: u.Final})
		return callbackErr
	})
	switch {
	case err == nil:
		return nil
	case callbackErr != nil && errors.Is(err, callbackErr):
		return callbackErr
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, ErrInvalid):
		return fmt.Errorf("%w: %w", providers.ErrInvalid, err)
	default:
		return fmt.Errorf("%w: %w", providers.ErrUnavailable, err)
	}
}
