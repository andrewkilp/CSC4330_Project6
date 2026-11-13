# Twilio media bridge (#21)

`packages/go/telephony/media` is the server end of a bidirectional
[Media Stream](https://www.twilio.com/docs/voice/media-streams/websocket-messages);
`packages/go/handlers/webhooks` serves it at `WSS /webhooks/twilio/media` with
`github.com/coder/websocket` (already the #20 transport dependency). It is not
mounted yet: the outbound start (#11) and the signed voice webhook that returns the
`<Connect><Stream>` TwiML (#16) come first.

## Binding a stream to a call

A provider call SID is never authorization by itself. The voice webhook (#16) adds
two stream parameters to the TwiML:

```xml
<Stream url="wss://PUBLIC_HOST/webhooks/twilio/media">
  <Parameter name="call_id" value="OUR-CALL-UUID"/>
  <Parameter name="token" value="SignStream(MEDIA_STREAM_SECRET, call_id, CallSid)"/>
</Stream>
```

Twilio echoes them in the `start` message. `TokenAuthorizer` recomputes the HMAC and
then checks that the `callSid` is the provider id #11 stored for that call while it
is dialing or connected. Missing, forged or cross-call tokens, an unknown SID and an
inactive call all close the socket with 1008 before any audio reaches recognition.
`MEDIA_STREAM_SECRET` is server-only and at least 32 bytes.

## Protocol

| Inbound | Handling |
| --- | --- |
| `connected` | Optional, before `start` |
| `start` | Required within 10 s. Must carry `audio/x-mulaw`, 8000 Hz, 1 channel and the inbound track, with well-formed stream, call and account SIDs; otherwise `ErrUnsupportedFormat`/`ErrProtocol` (close 1003) |
| `media` | Same `streamSid`, strictly increasing `sequenceNumber`, base64 payload of at most 64 KiB, decoded and passed to `providers.Transcriber` through a queue of 250 frames (5 s). Overflow ends the stream with `ErrBacklog` instead of silently dropping speech |
| `mark` | Reported to `OnEvent` only if it names a mark we sent and have not cleared |
| `stop` | `OnEvent(stop)`, recognition closed, clean close (1000) |
| `dtmf` | Ignored |

Outbound is a `providers.MediaStream`: `Send` (8 kHz mu-law only) and `Mark` queue
`media`/`mark` messages for one writer goroutine, bounded at 512 messages; a full
queue is an error rather than unbounded memory. `Clear` drops unsent audio, forgets
pending marks so a late echo cannot finalize cleared speech, and sends `clear`.
`Close` detaches without ending the call. Context cancellation closes recognition
and rejects further writes. No audio, parameters or phone numbers are logged.

## Verification

```sh
go test -race ./packages/go/telephony/... ./packages/go/handlers/webhooks/...
moon run :build :lint :typecheck :test
```

Tests use an in-memory socket for protocol and ownership cases and a real WebSocket
client that plays Twilio for the end-to-end stream. No real call was placed.
