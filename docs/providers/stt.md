# Streaming speech recognition (#20)

Decision reviewed November 13, 2026: provisionally implement **Deepgram Nova-3**
for the English-first demo, using the existing `providers.Transcriber` interface.
Only this production adapter is implemented. No application route, microphone
capture, Twilio bridge, database writer or automatic provider fallback is added.

## Comparison and remaining experiment

| Candidate | Protocol / audio | Language and trial considerations | Published rate observed |
| --- | --- | --- | --- |
| Deepgram Nova-3 | Binary WebSocket audio; raw mono mu-law at 8000 Hz; interim results plus explicit utterance endpointing | Adapter pins `en-US`. Vendor lists multilingual coverage separately. Pricing advertises $200 starting credit, no card, no expiry; actual account limits were not tested. | Pricing table lists monolingual Nova-3 at $0.0048/min, while the same page's FAQ says streaming is $0.46/hour. Confirm the account's streaming/MIP rate before budgeting. |
| ElevenLabs Scribe v2 Realtime | JSON WebSocket `input_audio_chunk` with base64 audio; accepts `ulaw_8000` and PCM at 8/16/22.05/24/44.1/48 kHz; partial/committed transcripts; VAD commit available | Vendor advertises 90+ languages. Model documentation lists six concurrent realtime STT sessions for Free; this is capacity, not unlimited free minutes. Actual credits/account eligibility were not tested. | API page lists $0.22/hour, or an arithmetic $0.011 for three minutes before plan/usage conditions. |

Sources: [Deepgram live audio](https://developers.deepgram.com/reference/speech-to-text/listen-streaming),
[Deepgram languages](https://deepgram.com/languages),
[Deepgram pricing](https://deepgram.com/pricing),
[ElevenLabs realtime protocol](https://elevenlabs.io/docs/api-reference/speech-to-text/v-1-speech-to-text-realtime),
[ElevenLabs API pricing](https://elevenlabs.io/pricing/api),
[ElevenLabs plan concurrency](https://elevenlabs.io/docs/overview/models/).

The provisional choice favors Deepgram's explicit separation between finalized
segments and utterance completion. Both candidates can ingest Twilio's mu-law
without transcoding, so a claimed codec advantage would be incorrect. Recognition
quality, actual endpoint latency and account compatibility still need a measured
consenting-speaker experiment against **both** candidates. This change does not
complete that acceptance criterion; keep #20 open until that evidence is recorded.
No paid endpoints or live accounts were contacted by this implementation's tests.

## Adapter behavior

Construct `transcription.New(Config{APIKey: serverKey})` only in the explicitly
enabled real-call composition. It does not read the environment or open a network
connection until `Open`. The model/language are fixed to `nova-3` / `en-US` to keep
the supported behavior reviewable. Credentials are header-only, redirects are
rejected, and vendor bodies/messages are never exposed in returned errors.

Input must be `providers.TelephonyFormat`: decoded raw mu-law, 8000 Hz, one channel.
The media bridge must base64-decode Twilio frames, then pass raw bytes with
contiguous sequence numbers starting at one. No WAV headers, resampling or
mu-law-to-PCM conversion occurs. PCM and other formats fail before dialing the
transcription endpoint. All byte values are valid mu-law samples; malformed audio
here means invalid metadata, an empty/oversized chunk or a sequence gap, rather
than arbitrary sample values.

Each utterance gets a fresh server-generated UUID. Interim segments replace text
under that UUID; finalized segments are accumulated but remain partial at our
boundary. Only `speech_final: true` emits a final `TranscriptUpdate`. This prevents
the conversation loop from answering after a finalized mid-sentence segment.
Duplicate final segments are suppressed; a repeated segment carrying the endpoint
can finalize accumulated text once. Silence alone creates no turn. An endpoint
with empty text may complete previously accumulated speech. Timestamps are rounded
to milliseconds relative to the audio stream; the earliest start survives updates.
An empty endpoint after only an interim hypothesis emits no blank/invented final.
It preserves that pending TurnID so subsequent recognized text can replace and
finalize it. The shared interface has no partial-removal event; abandoning the ID
would strand a partial downstream and block further conversation generation.
See [Deepgram endpointing semantics](https://developers.deepgram.com/docs/understand-endpointing-interim-results).

The adapter bounds inbound messages to 64 KiB, text to 10,000 UTF-16 code units and
finalized segments to 4096 per session. It has no audio queue: concurrent writes
serialize and backpressure the caller. Audio/keepalive writes have five-second
timeouts and the handshake has a ten-second timeout. A text KeepAlive is sent
every three seconds during idle periods, following the vendor's
[3–5-second guidance](https://developers.deepgram.com/docs/audio-keep-alive).
Call duration/concurrency limits remain #35's responsibility.

Cancellation or `Close` discards pending partial text and closes the socket.
Callbacks are serialized, run without the state mutex and may call `Close`.
Callbacks already admitted for delivery may finish; no later callback is admitted
after closure/cancellation. The callback must return promptly. A callback error
closes the session; later pushes return `ErrClosed`. Invalid vendor data or a
disconnect closes recognition and the next `Push` returns `ErrUnavailable`.
The current interface has no asynchronous error callback, so #25's bridge must
observe Push errors and end/pause appropriately; silent reconnection is absent.
Unknown vendor message types fail closed instead of interpreting new semantics.

This PR explicitly clarifies #6's shared session contract: logical callback start
is admission under the state lock, not the first instruction of arbitrary user
code. Reentrant `Close` cannot also wait for an in-flight callback, since a callback
may close its own session. A deterministic concurrent-close test pauses between
admission and user entry, then proves the admitted update can finish and subsequent
admissions are fenced. Tests also close/cancel before admission and inside callbacks.
The #25 bridge must recheck its call context/generation token before persisting
an in-flight update. This clarification applies to both session interfaces and
matches the existing mock's delivery-admission semantics.

`mip_opt_out=true` excludes requests from Deepgram's model-improvement program;
it is not a zero-retention promise. The vendor describes a pricing discount for
participation, so the displayed base rate may not apply to this adapter. Verify
the actual account policy/rate before a real call. See
[MIP documentation](https://developers.deepgram.com/docs/the-deepgram-model-improvement-partnership-program).

Dependency: `github.com/coder/websocket v1.8.14` implements bounded RFC 6455
transport, cancellation and control frames. This avoids a bespoke WebSocket
parser. No provider SDK or second recognizer is bundled. Module checksums are
committed; official API documentation is at
[coder/websocket](https://pkg.go.dev/github.com/coder/websocket).

## Offline evidence and rehearsal

```sh
go test ./packages/go/transcription/...
go vet ./packages/go/transcription/...
go test -race ./packages/go/transcription/...
go test ./packages/go/transcription/... -bench BenchmarkFixtureMapping -benchmem
moon run :build :lint :typecheck :test
```

Tests use a local WebSocket server and synthetic protocol JSON in
`packages/go/transcription/testdata/results.jsonl`. The fixture is hand-authored
from documented message shapes, **not a recording of a vendor recognition run**.
It covers silence, replaceable partials, finalized mid-utterance segments, two
final utterances and unchanged mu-law byte transport. Additional tests cover
authentication failure, handshake deadline, malformed/oversized messages,
disconnect, chunk validation, cancellation and callback-triggered closure.

Measured November 13, 2026 on Windows/amd64, Intel i9-11900H, Go 1.26.4: mapping the
seven-message fixture produced five transcript updates across two utterances at
20,833 ns/op, 5516 B/op and 137 allocs/op (`BenchmarkFixtureMapping`, 56,154
iterations). This measures local parsing/segmentation overhead including UUIDs.
It says nothing about acoustic accuracy, network latency or vendor endpointing
latency. Race tests passed with CGO enabled and MSYS2 UCRT64 GCC on PATH.

Before closing #20, record a manual, approved sample for both candidates using
the same fictional restaurant/appointment script, with an English speaker,
mu-law 8000 Hz audio, partial and final times, word corrections, silence,
interruption, total duration and actual billed usage. No private transcript or raw
audio should be committed. Confirm the provisional selection using that evidence;
do not add a second production adapter as part of the comparison.

Full inherited verification on this branch: `moon run :build :lint :typecheck
:test` exited 0 with **28 tasks completed** in 28.050 seconds, including web export.
