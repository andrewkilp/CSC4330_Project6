package main

import (
	"context"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/auth"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/conversation"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/calls"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/responses"
	"net/http"
)

func registerApprovals(mux *http.ServeMux, store calls.DraftStore, sessions *auth.Sessions, origin string, development bool) error {
	service := conversation.DurableApprovals{}
	if source, ok := store.(conversation.ApprovalStore); ok && !nilStore(source) {
		service.Store = source
	}
	h, err := responses.New(responses.Config{OwnerID: auth.OwnerID, Origin: origin, AllowHTTP: development, Approve: func(ctx context.Context, owner, id string, a conversation.Approval) (any, error) {
		return service.Approve(ctx, owner, id, a)
	}})
	if err != nil {
		return err
	}
	mux.Handle("POST /v1/calls/{id}/responses/{response_id}/approve", sessions.RequireOwner(h))
	return nil
}
