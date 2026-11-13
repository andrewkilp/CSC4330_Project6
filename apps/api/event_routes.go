package main

import (
	"context"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/auth"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/events"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/calls"
	eventhandler "github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/events"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"net/http"
)

type unavailableEventStore struct{}

func (unavailableEventStore) EventsAfter(context.Context, string, string, int64, int) ([]repository.Event, error) {
	return nil, repository.ErrUnavailable
}
func (unavailableEventStore) Snapshot(context.Context, string, string) (repository.Snapshot, error) {
	return repository.Snapshot{}, repository.ErrUnavailable
}
func registerCallEvents(mux *http.ServeMux, store calls.DraftStore, sessions *auth.Sessions) error {
	var source eventhandler.Store = unavailableEventStore{}
	if candidate, ok := store.(eventhandler.Store); ok && !nilStore(candidate) {
		source = candidate
	}
	return eventhandler.Register(mux, eventhandler.Config{Store: source, Hub: events.NewHub(0), RequireOwner: sessions.RequireOwner})
}
