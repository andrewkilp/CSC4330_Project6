package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/calls"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/webhooks"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

// Callback ingress is independent of accepting new outbound calls. Operators
// can disable new dialing while retaining verified updates for existing calls.
func callbackEnvironment(start apiStartConfig) (webhooks.Config, bool, error) {
	raw := os.Getenv("TWILIO_WEBHOOKS_ENABLED")
	if raw != "" && raw != "false" && raw != "true" {
		return webhooks.Config{}, false, errors.New("invalid callback configuration")
	}
	enabled := start.policy.EnableReal
	if raw != "" {
		enabled = raw == "true"
	}
	if !enabled {
		if start.policy.EnableReal {
			return webhooks.Config{}, false, errors.New("outbound calls require configured callbacks")
		}
		return webhooks.Config{}, false, nil
	}
	account, token, base := start.provider.AccountSID, start.provider.AuthToken, start.policy.PublicBaseURL
	if start.policy.EnableReal {
		if account != start.policy.AccountSID {
			return webhooks.Config{}, false, errors.New("invalid callback configuration")
		}
	} else {
		account, token, base = os.Getenv("TWILIO_ACCOUNT_SID"), os.Getenv("TWILIO_AUTH_TOKEN"), os.Getenv("PUBLIC_BASE_URL")
	}
	return webhooks.Config{AccountSID: account, AuthToken: token, PublicBaseURL: base,
		MediaURL: strings.Replace(strings.TrimSuffix(base, "/"), "https://", "wss://", 1) + "/webhooks/twilio/media",
		// registerTwilioCallbacks enables media only after mounting the #21 bridge.
		MediaEnabled: false}, true, nil
}

type unavailableCallbackLedger struct{}

func (unavailableCallbackLedger) ApplyTwilioCallback(context.Context, repository.TwilioCallback) (repository.CallbackResult, error) {
	return repository.CallbackResult{}, repository.ErrUnavailable
}

func registerTwilioCallbacks(mux *http.ServeMux, store calls.DraftStore, start apiStartConfig, rt *liveRuntime) error {
	config, enabled, err := callbackEnvironment(start)
	if err != nil {
		return err
	}
	secret, lookup, stt, mediaEnabled, err := mediaEnvironment(enabled, store)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	// Preserve the optional-database API mode: unavailable storage answers 503
	// explicitly, and no second pool, owner identity or session store is created.
	var ledger webhooks.Ledger = unavailableCallbackLedger{}
	if !nilStore(store) {
		var ok bool
		ledger, ok = store.(webhooks.Ledger)
		if !ok || nilStore(ledger) {
			return errors.New("invalid callback storage configuration")
		}
	}
	config.MediaEnabled, config.MediaSecret = mediaEnabled, secret
	config.Publish = func(_ context.Context, evs []repository.Event) { rt.publish(evs...) }
	handler, err := webhooks.New(config, ledger)
	if err != nil {
		return errors.New("invalid callback configuration")
	}
	handler.Register(mux)
	// TwiML only points Twilio at the stream when the bridge is mounted here.
	if mediaEnabled {
		registerMedia(mux, rt.media, secret, lookup, stt)
	}
	return nil
}
