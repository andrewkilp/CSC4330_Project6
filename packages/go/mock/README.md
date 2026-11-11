# Deterministic mock scenarios (#5)

This module scripts complete mock calls as ordered durable events. It performs no network, audio, database or real clock work and has no external dependencies besides `packages/go/conversation`.

| API | Use |
| --- | --- |
| `Names()` | Scenario names in stable order |
| `Generate(name, seed)` | Expand one scenario into a `Fixture`; same inputs, same bytes |
| `Encode(f)` / `Decode(data)` | Stable indented JSON; strict decoding rejects unknown fields and event types |
| `Validate(f)` | Contract, ordering, state progression, fictional-data and scenario checks; returns `*ValidationError{Seq, Field, Message}` |

Scenarios: `restaurant-time-conflict` and `appointment-missing-detail`. See `docs/fixtures/README.md` for their scripts and expected operator input and outcomes.

Identifiers are version-4-shaped UUIDs derived from SHA-256 of the seed, scenario and a label, so they are stable across Go releases. Timestamps start at a fixed scenario time; the seed adds 0-249 ms of jitter per step.

## Provider adapters (#6)

`NewProviders(name, seed, Options)` returns `ScenarioTelephony`, `ScenarioTranscriber`, `ScenarioResponder` and `ScenarioSynthesizer`, which implement `packages/go/providers` from one scenario with no network:

- **Telephony** accepts only fictional `+1NXX55501XX` destinations. When media attaches, it plays the first business turn as marked synthetic mu-law frames. After each played mark it plays the next turn, like a business answering once the assistant finishes. Outbound audio plays one 20 ms frame per `FrameDelay` (default 1 ms), up to 60 seconds of queued audio. `Clear` and `End` drop unplayed audio and marks.
- **Transcriber** decodes those frames back into the scenario's partial and final business turns, using the fixture's turn ids and timing.
- **Responder** returns the scenario's suggestion that matches the request's finalized business turn count and applied instructions, three words per draft. An unscripted request fails with `ErrNoScriptedReply`; it never invents a reply.
- **Synthesizer** emits deterministic placeholder audio (70 ms per character, 200 ms chunks) tagged with the request token. It is not speech.

`Options.Pace` runs before each callback so tests can cancel at an exact point. The tests run `providertest.Run` against both scenarios. They also replay each scenario end to end through all four adapters (business turns, partials, operator instructions and spoken replies all match the fixture), check exact-point cancellation, and check that `Clear` and hang-up stop playback mid-reply.

The payload structs mirror `docs/contracts.md` for fixtures only. T01 owns the canonical domain types and T06 owns provider interfaces and mock adapters; those can replay these events without changing their shape.

Run `moon run mock:build mock:lint mock:test`, or regenerate with `scripts/scenarios/generate.sh`.
