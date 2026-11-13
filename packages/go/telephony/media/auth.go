package media

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// SignStream returns the per-call stream credential. The voice webhook (#16) puts the
// call id and this token in the TwiML <Stream> as <Parameter name="call_id"> and
// <Parameter name="token">; Twilio echoes them in the start message's
// customParameters. The token binds our call id to the provider call SID, so a
// leaked SID alone, or a token for another call, is rejected.
func SignStream(secret []byte, callID, callSID string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("switchboard-media-stream\x00" + callID + "\x00" + callSID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// TokenAuthorizer verifies the stream credential and that the provider call SID is the
// one our start route (#11) stored for that call.
type TokenAuthorizer struct {
	// Secret is server-only, at least 32 bytes (MEDIA_STREAM_SECRET).
	Secret []byte
	// ProviderCallID returns the stored provider call SID for one of our calls that is
	// currently dialing or connected, or an error.
	ProviderCallID func(ctx context.Context, callID string) (string, error)
}

func (a TokenAuthorizer) Authorize(ctx context.Context, start Start) (string, error) {
	if len(a.Secret) < 32 || a.ProviderCallID == nil {
		return "", errors.New("media stream authorizer is not configured")
	}
	callID, token := start.CustomParameters["call_id"], start.CustomParameters["token"]
	if callID == "" || len(callID) > 64 || token == "" || len(token) > 128 {
		return "", ErrUnauthorized
	}
	want := SignStream(a.Secret, callID, start.CallSID)
	if !hmac.Equal([]byte(want), []byte(token)) {
		return "", ErrUnauthorized
	}
	stored, err := a.ProviderCallID(ctx, callID)
	if err != nil || !hmac.Equal([]byte(stored), []byte(start.CallSID)) {
		return "", ErrUnauthorized
	}
	return callID, nil
}
