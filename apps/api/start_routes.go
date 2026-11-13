package main

import (
	"errors"
	"os"
	"reflect"
	"strings"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/mock"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/telephony"
)

type apiStartConfig struct {
	policy   telephony.StartConfig
	provider telephony.Config
}

func startEnvironment() (apiStartConfig, error) {
	raw := os.Getenv("ENABLE_REAL_CALLS")
	if raw != "" && raw != "false" && raw != "true" {
		return apiStartConfig{}, errors.New("invalid call start configuration")
	}
	if raw != "true" {
		return apiStartConfig{}, nil
	}
	var allowed []string
	for _, number := range strings.Split(os.Getenv("CALL_ALLOWED_NUMBERS"), ",") {
		allowed = append(allowed, strings.TrimSpace(number))
	}
	return apiStartConfig{policy: telephony.StartConfig{EnableReal: true, AllowedNumbers: allowed, PublicBaseURL: os.Getenv("PUBLIC_BASE_URL"), AccountSID: os.Getenv("TWILIO_ACCOUNT_SID")}, provider: telephony.Config{AccountSID: os.Getenv("TWILIO_ACCOUNT_SID"), AuthToken: os.Getenv("TWILIO_AUTH_TOKEN"), FromNumber: os.Getenv("TWILIO_FROM_NUMBER")}}, nil
}

func startService(store telephony.StartStore, cfg apiStartConfig) (*telephony.StartService, error) {
	fixture, err := mock.NewProviders(mock.RestaurantTimeConflict, mock.DefaultSeed, mock.Options{})
	if err != nil {
		return nil, errors.New("invalid mock call configuration")
	}
	var real providers.Telephony
	if cfg.policy.EnableReal {
		real, err = telephony.New(cfg.provider)
		if err != nil {
			return nil, errors.New("invalid call start configuration")
		}
	}
	service, err := telephony.NewStartService(store, real, fixture.Telephony, cfg.policy)
	if err != nil {
		return nil, errors.New("invalid call start configuration")
	}
	return service, nil
}

// Interface parameters can contain typed nil pointers. Normalize optional stores
// before registering either route instead of constructing a broken DB fallback.
func nilStore(store any) bool {
	if store == nil {
		return true
	}
	v := reflect.ValueOf(store)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
