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

The payload structs mirror `docs/contracts.md` for fixtures only. T01 owns the canonical domain types and T06 owns provider interfaces and mock adapters; those can replay these events without changing their shape.

Run `moon run mock:build mock:lint mock:test`, or regenerate with `scripts/scenarios/generate.sh`.
