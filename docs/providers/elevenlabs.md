# ElevenLabs speech synthesis (#15)

`packages/go/elevenlabs/tts.Client` implements the shared
`providers.Synthesizer`. Construct it with a server-owned API key, voice ID and
model ID. Its constructor does not read environment variables, start synthesis or
place a call. Compose it explicitly when the later playback task is ready; no app
route or automatic provider fallback is added here.

Protocol checked November 13, 2026 against the official
[stream endpoint](https://elevenlabs.io/docs/api-reference/text-to-speech/stream)
and [streaming guide](https://elevenlabs.io/docs/eleven-api/guides/how-to/text-to-speech/streaming).
The adapter sends one HTTPS POST to `/v1/text-to-speech/{voice_id}/stream`, with
`xi-api-key`, a JSON `text`/`model_id` body and `output_format=ulaw_8000`. This
selects raw G.711 mu-law at 8 kHz for telephony rather than the endpoint's MP3
default. Account/model/voice availability must be checked during a separate
manual provider rehearsal; the adapter does not assume a free account can use a
particular voice or model. It does not request enterprise-only zero retention;
provider retention settings must be reviewed before sending private text.

Only 8 kHz mono mu-law requests are accepted. PCM, MP3, different rates and stereo
are rejected before network access. Request voice must match the configured
server voice. Text must be valid UTF-8, nonblank and at most 1500 UTF-16 code
units, matching `docs/contracts.md` (including astral characters). No resampling,
transcoding, WAV headers or base64 wrapping is performed: the future Twilio
transport owns base64 encoding of raw bytes.

The client accepts generic binary or explicit mu-law content types and rejects
incompatible content types/rate/channel parameters. This checks the declared
encoding; it cannot independently infer an arbitrary compressed codec from raw
bytes. A separate paid-provider check must verify actual format and speech
quality before a real demo.

Output callbacks are synchronous, serial and tagged with the request's generation
token. Chunks contain at most 1600 bytes (200 ms); sequences start at 1. One chunk
is held until the next read so `Last` is emitted only at a successful EOF. Empty,
oversized and interrupted responses fail without a final chunk. Total output is
capped at 1,440,000 bytes (180 seconds). Retained buffering is at most two chunks;
downstream consumers must separately bound their playback queues.

Timeout defaults to 30 seconds for the complete request, including stalled body
reads; explicit values from 1 ms to 60 seconds are accepted. Cancellation closes
the HTTP stream and stops further callbacks; callback errors are returned
unchanged. Other network/vendor failures become `providers.ErrUnavailable`, with
no vendor messages, response bodies or credentials. Redirects are refused and no
automatic retry duplicates synthesis or cost. A cancelled stream may already
have delivered earlier chunks: the playback owner must fence tokens and clear
queued audio on pause, instruction, interruption or end.

## Approval and integration boundary

The shared `SpeechRequest` carries no approval record. Its caller must verify the
current response ID/revision and approval before calling `Stream`, then recheck
each token before playback. Possession of a token is not authorization. #29 owns
approval, #30 owns playback/acknowledgement, and #35 owns call-wide limits. This
adapter creates no transcript turn and never treats generation or a final chunk
as proof that the business heard a reply or confirmed a booking.

## Offline verification

```sh
moon run elevenlabs:build elevenlabs:lint elevenlabs:test
moon run :build :lint :typecheck :test
```

Tests use injected transports/local HTTP servers only; production exposes no
endpoint override. `tts/testdata/stream.json` specifies synthetic mu-law bytes,
not recorded or generated speech. Its test verifies request encoding/model/voice,
exact streamed bytes, metadata, ordered sequences, preserved tokens and final
chunk. Failure tests cover invalid/oversized Unicode text, unsupported format,
wrong voice, empty/audio-overflow responses, 401/403/422/429/5xx, malformed content
types, read failure, redirect, stalled headers/body, cancellation and callback
failure. The unauthorized response fixture contains only invented data.

No live provider request, paid synthesis, audible speech-quality test or real
call has been performed for this task. API billing and the configured model/voice
remain prerequisites for a separately authorized manual check.
