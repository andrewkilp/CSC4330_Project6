package telephony

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
	"time"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
)

type StartStore interface {
	ReserveCallStart(context.Context, string, string, string, repository.StartPolicy, repository.StartIntent) (repository.StartAttempt, error)
	ConfirmCallStart(context.Context, string, string, string, string) (repository.Call, error)
	MarkCallStartAmbiguous(context.Context, string, string, string) error
}
type StartConfig struct {
	EnableReal                bool
	AllowedNumbers            []string
	PublicBaseURL, AccountSID string
}
type StartService struct {
	store      StartStore
	real, mock providers.Telephony
	cfg        StartConfig
	base       string
}

func NewStartService(store StartStore, real, mock providers.Telephony, cfg StartConfig) (*StartService, error) {
	if mock == nil {
		return nil, providers.ErrInvalid
	}
	base := "https://mock.invalid"
	if cfg.EnableReal {
		u, err := url.Parse(cfg.PublicBaseURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !accountSID.MatchString(cfg.AccountSID) || real == nil {
			return nil, providers.ErrInvalid
		}
		base = u.String()
		if len(cfg.AllowedNumbers) == 0 {
			return nil, providers.ErrInvalid
		}
		for _, n := range cfg.AllowedNumbers {
			if !e164.MatchString(n) {
				return nil, providers.ErrInvalid
			}
		}
	}
	cfg.AllowedNumbers = append([]string(nil), cfg.AllowedNumbers...)
	return &StartService{store: store, real: real, mock: mock, cfg: cfg, base: base}, nil
}

func (s *StartService) Start(ctx context.Context, owner, callID, key string) (repository.Call, error) {
	if s.store == nil {
		return repository.Call{}, repository.ErrUnavailable
	}
	var secret [32]byte
	rand.Read(secret[:])
	token := base64.RawURLEncoding.EncodeToString(secret[:])
	hash := sha256.Sum256([]byte(token))
	attempt := repository.NewID()
	reservation, err := s.store.ReserveCallStart(ctx, owner, callID, key, repository.StartPolicy{EnableReal: s.cfg.EnableReal, AllowedNumbers: s.cfg.AllowedNumbers, AccountSID: s.cfg.AccountSID}, repository.StartIntent{AttemptID: attempt, TokenHash: hash[:]})
	if err != nil {
		return repository.Call{}, err
	}
	if !reservation.Fresh {
		return reservation.Call, nil
	}
	// Once this durable reservation has committed, NO path may call Start again
	// for this call, regardless of errors, alternate keys, crashes or restarts.
	base := s.base
	if reservation.Call.Mode == "mock" {
		base = "https://mock.invalid"
	}
	query := url.Values{"call_id": {callID}, "attempt_id": {attempt}, "token": {token}}.Encode()
	callbacks := providers.Callbacks{Voice: base + "/webhooks/twilio/voice?" + query, Status: base + "/webhooks/twilio/status?" + query, Media: strings.Replace(base, "https://", "wss://", 1) + "/webhooks/twilio/media"}
	provider := s.mock
	if reservation.Call.Mode == "real" {
		provider = s.real
	}
	if provider == nil {
		s.ambiguous(owner, callID, attempt)
		return repository.Call{}, repository.ErrUnavailable
	}
	sid, err := provider.Start(ctx, providers.Dial{CallID: callID, Destination: reservation.Call.Destination, Callbacks: callbacks})
	if err != nil {
		s.ambiguous(owner, callID, attempt)
		return repository.Call{}, repository.ErrUnavailable
	}
	// Use a bounded independent context so a disconnected browser does not prevent
	// retaining an acknowledged SID. Failure still leaves the permanent intent.
	commit, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	call, err := s.store.ConfirmCallStart(commit, owner, callID, attempt, sid)
	if err != nil {
		s.ambiguous(owner, callID, attempt)
		return repository.Call{}, repository.ErrUnavailable
	}
	return call, nil
}

func (s *StartService) ambiguous(owner, callID, attempt string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.store.MarkCallStartAmbiguous(ctx, owner, callID, attempt)
}
