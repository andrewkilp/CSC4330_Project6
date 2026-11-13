# Twilio media bridge (#21)

`packages/go/telephony/media` is the server end of a bidirectional
[Media Stream](https://www.twilio.com/docs/voice/media-streams/websocket-messages);
`packages/go/handlers/webhooks` serves it at `WSS /webhooks/twilio/media` with
`github.com/coder/websocket` (already the #20 transport dependency).

## Mounting (apps/api)

The API mounts the route only when all of these are configured. A partial
configuration is a startup error:

| Setting | Purpose |
| --- | --- |
| Signed callbacks enabled (`ENABLE_REAL_CALLS=true` or `TWILIO_WEBHOOKS_ENABLED=true`) | #16 voice TwiML points Twilio at the stream |
| `MEDIA_STREAM_SECRET` (at least 32 bytes, server-only) | Signs and verifies the per-call stream token |
| `DATABASE_URL` | `repository.ActiveProviderCallID` binds the stream to the stored provider SID of a dialing, connected or paused real call |
| `DEEPGRAM_API_KEY` | Inbound audio goes to the #20 transcriber |

Without `MEDIA_STREAM_SECRET`, the route is absent and verified voice callbacks
hang up. The API keeps each call's outbound `providers.MediaStream` in a live-call
registry, so call controls (#31) and the conversation runtime (#41) can clear or close
it. Transcript updates and media events go to that runtime's sink; until a sink is
set they are discarded, never stored.

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

## Remote interruption (#26)

**Detection.** The selected recognizer (#20, Deepgram with `interim_results=true`) delivers interim text within a few hundred milliseconds of the business starting to talk. `telephony.Calls.ObserveRemoteSpeech` treats the first non-empty transcript update while the assistant's audio is still playing as an interruption. No separate voice-activity dependency is used. The API runs this check on every update before forwarding it.

**Playback tracking.** Each approved reply is sent chunk by chunk with its own mark (`media.Playback`, mark `<responseID>.<n>`). Twilio reports marks in buffer order, so the acknowledged marks are exactly the audio that played.

**On interruption:**
1. The ledger is frozen, which makes every outstanding mark stale.
2. Twilio `clear` drops the unplayed audio.
3. `Loop.InterruptPlayback` cancels the reply and retires its generation token.
4. The caller gets the words actually heard (`conversation.SpokenPortion`, rounded down to a whole word) to record as the assistant's turn.

**Late marks.** A late mark for cleared audio is `media.ErrStaleMark`. It can neither complete nor restart the cancelled reply, and the loop rejects `CompletePlayback`, `BeginPlayback` and a second synthesis for it. Pause and end freeze the ledger the same way.

**Evidence:**
- `packages/go/telephony/interrupt_test.go`: deterministic interruption after two acknowledged chunks, plus a 24-run race in which marks arrive while the business speaks and exactly one of interrupt or complete wins each run, with both outcomes exercised.
- `packages/go/telephony/media/playback_test.go`
- `packages/go/conversation/interrupt_test.go`
- `apps/api/media_routes_test.go`
