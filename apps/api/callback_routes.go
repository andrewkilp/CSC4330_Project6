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
		// No environment toggle can claim #21's missing authenticated bridge.
		MediaEnabled: false}, true, nil
}

type unavailableCallbackLedger struct{}

func (unavailableCallbackLedger) ApplyTwilioCallback(context.Context, repository.TwilioCallback) (repository.CallbackResult, error) {
	return repository.CallbackResult{}, repository.ErrUnavailable
}

func registerTwilioCallbacks(mux *http.ServeMux, store calls.DraftStore, start apiStartConfig) error {
	config, enabled, err := callbackEnvironment(start)
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
	handler, err := webhooks.New(config, ledger)
	if err != nil {
		return errors.New("invalid callback configuration")
	}
	handler.Register(mux)
	return nil
}
