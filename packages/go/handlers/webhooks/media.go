// Package webhooks holds provider-facing routes. This file serves the Twilio Media
// Streams WebSocket (WSS /webhooks/twilio/media, #21). Signed voice/status callbacks
// and the TwiML that points Twilio here belong to #16; the stream itself is
// authorized by the per-call token that TwiML carries, not by the provider SID.
package webhooks

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/coder/websocket"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/providers"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/telephony/media"
)

// RegisterMedia mounts the media stream route. It is not mounted by the API until the
// real telephony start (#11) and voice webhook (#16) exist.
func RegisterMedia(mux *http.ServeMux, cfg media.Config) {
	mux.Handle("GET /webhooks/twilio/media", MediaHandler(cfg))
}

type socket struct{ c *websocket.Conn }

func (s socket) Read(ctx context.Context) ([]byte, error) {
	kind, data, err := s.c.Read(ctx)
	if err != nil {
		return nil, err
	}
	if kind != websocket.MessageText {
		return nil, media.ErrProtocol
	}
	return data, nil
}

func (s socket) Write(ctx context.Context, msg []byte) error {
	return s.c.Write(ctx, websocket.MessageText, msg)
}

// MediaHandler upgrades the request and runs the bridge. Close codes are generic so
// nothing about the call or the failure leaks to the peer.
func MediaHandler(cfg media.Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
		if err != nil {
			return
		}
		conn.SetReadLimit(media.MaxMessageBytes)
		err = media.Serve(r.Context(), socket{conn}, cfg)
		switch {
		case err == nil:
			_ = conn.Close(websocket.StatusNormalClosure, "")
		case errors.Is(err, media.ErrUnauthorized):
			_ = conn.Close(websocket.StatusPolicyViolation, "unauthorized")
		case errors.Is(err, media.ErrProtocol), errors.Is(err, providers.ErrUnsupportedFormat):
			_ = conn.Close(websocket.StatusUnsupportedData, "protocol")
		case errors.Is(err, context.Canceled):
			_ = conn.Close(websocket.StatusGoingAway, "")
		default:
			slog.Warn("media stream closed with an error")
			_ = conn.Close(websocket.StatusInternalError, "")
		}
	})
}
