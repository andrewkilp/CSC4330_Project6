# Provider interfaces (#6)

`packages/go/providers` defines the four server-side boundaries from `docs/contracts.md`. It has no dependencies, network code, vendor SDKs or credentials.

| Interface | Methods | Notes |
| --- | --- | --- |
| `Telephony` | `Start(ctx, Dial) -> providerCallID`, `End(ctx, id)`, `Media(ctx, id, emit) -> MediaStream` | `Dial` needs our call UUID, an E.164 destination, and https/wss callback URLs. `MediaStream` has `Send`, `Mark`, `Clear` and `Close`. Inbound events are `audio`, `mark` and finally `stop`. |
| `Transcriber` | `Open(ctx, {CallID, Format}, emit) -> TranscriptSession` (`Push`, `Close`) | Partial updates share a `TurnID` and are replaced; finals are never changed. `Close` discards pending partial text. |
| `Responder` | `Generate(ctx, ResponseRequest, emit)` | Takes only finalized turns and applied instructions, with instructions separate from the transcript. Drafts are cumulative; only `Final` is complete, and it is still just a suggestion. |
| `Synthesizer` | `Stream(ctx, SpeechRequest, emit)` | Each `SpeechChunk` carries the request's generation `Token`, so audio from a stale generation can be dropped before it reaches the call. |

**Audio metadata.** `AudioFormat{Encoding, SampleRate, Channels}`. `TelephonyFormat` is 8 kHz mono mu-law with 160-byte, 20 ms frames. Chunks carry decoded bytes; base64 is the transport adapter's job. Each `AudioChunk` has a `Seq` that starts at 1 and increases. Chunks hold at most 64 KiB.

**Cancellation.** All adapters follow the same rules:
- Callbacks are serialized and never run after a blocking method returns.
- Cancelling the context stops further callbacks, and the method returns `context.Canceled`.
- An error returned by a callback stops delivery. Blocking methods return it unchanged; a session closes instead.
- A closed session rejects further use with `ErrClosed`.
- `Clear` drops queued outbound audio and any marks queued with it, so those marks are never reported.
- `End` makes `stop` the last media event.

**Errors.** `ErrInvalid` means nothing was sent. The others are `ErrUnsupportedFormat`, `ErrNotFound` (unknown provider call id), `ErrClosed`, and `ErrUnavailable`, the user-safe wrapper for any vendor failure. Adapters never return vendor bodies.

## Contract suite

`providertest.Run(t, factory)` runs the same checks against any adapter set:

- **lifecycle:** start → inbound audio → transcript partials and final → generate → synthesize → send → mark → end → stop.
- **cancellation:** responder and synthesizer cancel stops drafts and audio; transcriber close and cancel; `Clear` drops queued audio and its mark; media close and cancel stop events.
- **errors:** callback errors stop delivery.
- **invalid input:** bad dial, unknown id, nil callback, unsupported format, bad transcript or instructions, bad speech request.

`packages/go/mock` runs it against the scenario mocks. When #11, #14, #15 and #20 add real adapters, they should run the same suite against recorded fixtures in CI. Any real-call run is a separate, labelled manual check; the suite never dials by itself.

## Integration notes

- `claude.Responder{Client}` (in `packages/go/claude`) adapts #14's client to `Responder`. Invalid input wraps `ErrInvalid`. Provider failures wrap `ErrUnavailable` and keep the specific claude error (for example `claude.ErrRateLimited`). Context and callback errors pass through unchanged. Adapters import this package, never the other way round.
- `Token` mirrors `conversation.Token`, so this package stays dependency-free.
