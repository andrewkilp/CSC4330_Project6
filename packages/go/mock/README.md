# Deterministic mock scenarios (#5)

This module scripts complete mock calls as ordered durable events. The fixture generator performs no network, audio, database or real clock work. It depends on `packages/go/conversation`; the runtime responder uses the existing #14 boundary in `packages/go/claude` without constructing its HTTP client.

| API | Use |
| --- | --- |
| `Names()` | Scenario names in stable order |
| `Generate(name, seed)` | Expand one scenario into a `Fixture`; same inputs, same bytes |
| `Encode(f)` / `Decode(data)` | Stable indented JSON; strict decoding rejects unknown fields and event types |
| `Validate(f)` | Contract, ordering, state progression, fictional-data and scenario checks; returns `*ValidationError{Seq, Field, Message}` |

Scenarios: `restaurant-time-conflict` and `appointment-missing-detail`. See `docs/fixtures/README.md` for their scripts and expected operator input and outcomes.

Identifiers are version-4-shaped UUIDs derived from SHA-256 of the seed, scenario and a label, so they are stable across Go releases. Timestamps start at a fixed scenario time; the seed adds 0-249 ms of jitter per step.

The payload structs mirror `docs/contracts.md` for fixtures only. T01 owns the canonical domain types and T06 owns provider interfaces and mock adapters; those can replay these events without changing their shape.

Run `moon run mock:build mock:lint mock:test`, or regenerate with `scripts/scenarios/generate.sh`.

T19 adds `NewResponder(name, seed)`, which derives suggested replies directly from these fixtures and implements the conversation loop's cancellable boundary. It matches the goal, latest finalized business text and ordered applied instructions. Unknown context returns `ErrUnscripted`, never an invented answer. Reply text is deterministic; runtime loop IDs and timestamps are newly allocated. `NewConversation` explicitly chooses this responder for a mock call, and `RunConversation` runs to the first suggestion without replaying fixture approval/audio events. Integration tests feed both scenarios' business turns and instructions through the actual loop and compare all four suggestions per scenario while proving approval remains blocked.
