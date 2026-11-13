package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/auth"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/events"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/calls"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/control"
	eventroutes "github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/events"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/telephony"
)

// liveRuntime is the process-local live state that start, media, callbacks and
// controls share: one live-call registry and one event hub.
type liveRuntime struct {
	calls *telephony.Calls
	hub   *events.Hub
	media *liveMedia
}

func newLiveRuntime() *liveRuntime {
	c := telephony.NewCalls()
	return &liveRuntime{calls: c, hub: events.NewHub(0), media: newLiveMedia(c)}
}

// publish fans committed events out to stream subscribers. A committed terminal call
// (for example a provider "completed" callback) also stops its live work.
func (rt *liveRuntime) publish(evs ...repository.Event) {
	rt.hub.Publish(evs...)
	for _, e := range evs {
		if e.Type != "call.updated" {
			continue
		}
		var c struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(e.Payload, &c) == nil && (c.Status == "ended" || c.Status == "failed" || c.Status == "cancelled") {
			go func(id string) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = rt.calls.End(ctx, id)
			}(e.CallID)
		}
	}
}

type updateStore interface {
	Update(ctx context.Context, owner, callID, action, key string, request any, fn func(*repository.Tx) (any, error)) (json.RawMessage, error)
}

// registerCallRuntime mounts the durable event stream (#23) and call controls (#31).
// Both need the repository; without DATABASE_URL neither route exists.
func registerCallRuntime(mux *http.ServeMux, store calls.DraftStore, sessions *auth.Sessions, rt *liveRuntime, real providers.Telephony) error {
	if nilStore(store) {
		return nil
	}
	if es, ok := store.(eventroutes.Store); ok {
		if err := eventroutes.Register(mux, eventroutes.Config{Store: es, Hub: rt.hub, RequireOwner: sessions.RequireOwner}); err != nil {
			return err
		}
	}
	if rs, ok := store.(telephony.ReconcileStore); ok {
		// Orphan dialing cleanup (#36) for the life of the process.
		reconciler := &telephony.Reconciler{Store: rs, Calls: rt.calls, Publish: rt.publish, Providers: map[string]providers.Telephony{}}
		if real != nil {
			reconciler.Providers["real"] = real
		}
		go reconciler.Run(context.Background(), 30*time.Second)
	}
	if up, ok := store.(updateStore); ok {
		return control.Register(mux, control.Config{Store: control.Repository{Store: up}, Runtime: rt.calls, Publish: rt.publish, RequireOwner: sessions.RequireOwner})
	}
	return nil
}

// realTelephony returns the Twilio client for hanging up calls placed before a
// restart, or nil when real calls are disabled. startService already validated it.
func realTelephony(cfg apiStartConfig) providers.Telephony {
	if !cfg.policy.EnableReal {
		return nil
	}
	client, err := telephony.New(cfg.provider)
	if err != nil {
		return nil
	}
	return client
}
