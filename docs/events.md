# Event reducer and reconnect rules (#9)

`packages/ts/api/src/events.ts` exports typed event parsing, an immutable client projection, and snapshot recovery. It contains no network connection, UI, timers or persistence. `AGENTS.md` is the project guide; `docs/contracts.md` defines the planned wire envelope. The Go state machine from #4 is the server decision layer. This reducer projects its events and rejects obsolete client updates; it does not authorize calls or speech.

## Public functions

| Function | Result |
| --- | --- |
| `parseEvent(decodedJson)` | A discriminated `CallEvent`, or `EventValidationError` with only the invalid field name |
| `createEventState(callId)` | Empty state at sequence 0 for one call |
| `reduceEvent(state, decodedJson)` | New projection, retaining the input state unchanged |
| `expireCursor(state)` | Preserve known data, hide the active suggestion and require a snapshot |
| `hydrateSnapshot(state, snapshot)` | Replace the projection atomically with a validated, consistent snapshot and cursor |

Import these through `@switchboard/api`. All accepted payloads are copied from decoded input; unknown fields are discarded. Treat returned state as readonly. A response status update never creates a transcript turn. Only `turn.updated` supplies spoken text.

The parser validates all seven event types: `call.updated`, `turn.updated`, `instruction.updated`, `response.updated`, `summary.ready`, `provider.error`, `usage.updated`. It checks UUIDs, safe integer sequences/revisions, timestamp forms, booleans, closed enums, text bounds and entity identity. Payload call IDs and context revisions must match the envelope. Envelope and server metadata timestamps are UTC with `Z`; business preferences may carry an offset and a valid named time zone. Zero-based durations are integers. No invalid event changes the cursor or enters the projection.

The `Event*` names are aliases of the canonical types in `types.ts` frozen by #1, and the entity parsers live there; new code should import `Call`, `Turn`, `Response` and so on directly. `EventValidationError` is an alias of `ContractValidationError`. #1 added `paused` as a call status: the reducer allows `connected <-> paused`, retires the active suggestion on pause, never reopens a terminal call, and treats a paused snapshot as having no actionable response. The parser enforces the currently documented field limits; it is not a substitute for server validation or session ownership.

## Ordering and invalidation

| Condition | Behavior |
| --- | --- |
| `seq <= lastSeq` | Duplicate/older replay is ignored; return the same state |
| `seq == lastSeq + 1` | Consume the event and advance the cursor |
| `seq > lastSeq + 1` | Do not consume it; record `sequence_gap` and hide the suggestion |
| Recovery is pending | Freeze event application until snapshot hydration succeeds |
| Older context revision | Consume the contiguous sequence but ignore its entity changes |
| Newer context revision | Update the projection revision and retire the old suggestion |
| Same turn ID, partial -> final | Replace that turn, keeping its first-seen position |
| Already-final turn | Ignore subsequent changes, including a late partial |
| New final business turn | Retire the prior suggestion even when goal revision is unchanged |
| New response ID | Retire the previous active ID so late callbacks cannot select it again |
| Illegal response regression | Keep the existing response; consume the event cursor |
| Terminal call | Do not reopen it or apply late transcript/response/instruction events |

Responses follow generating -> suggested -> approved -> speaking -> spoken; active responses can also fail or cancel. Terminal response IDs cannot reopen. A projection may first encounter an entity at a later status, but once present it must progress in order. The server must emit its intermediate durable transitions in order. Only the active response at the current context revision is actionable. An archived suggestion remains in `responses` for inspection; the UI must use `currentResponseId`, not search the map for a suggested status.

Instructions replace pending entries by ID; applied/failed entries are final. Usage cannot go backward in elapsed time. Summaries are accepted only after a known terminal call; a confirmed summary must cite known finalized business turns. The backend in #39 remains responsible for deciding whether the cited words actually confirm a booking.

## Reconnect handoff for #22 and #23

1. For a fresh call, begin at sequence 0. For an existing call, hydrate an owner-authorized consistent snapshot first. Never initialize at an arbitrary cursor without the corresponding entities.
2. Subscribe after the last fully applied sequence. SSE `id` is the decimal `seq`, `event` must match the JSON `type`, and `data` is the complete event JSON. SSE comments are heartbeats; do not feed them to the reducer. The future transport validates this framing before `reduceEvent`.
3. A normal disconnect preserves state and resumes using `Last-Event-ID` (or the initial `after_seq` query). Duplicate replay is safe. The reducer does not open the connection or schedule retries.
4. A sequence gap or explicit server cursor-expired response requires recovery. Preserve the transcript for reading, disable suggestion approval and other stale actions, and request a consistent snapshot. Do not silently skip a missing event, invent a sequence, or replay buffered speech.
5. The snapshot contains `{call, last_seq, turns, instructions, responses, summary?, usage?}` from one consistent server checkpoint. Its sequence/revision cannot go backward, entities must belong to that call, IDs must be unique per collection, and at most one nonterminal response can be active at the current revision. It may advance past missed lifecycle states, but cannot regress or reopen a terminal call.
6. Once validated, install it with `hydrateSnapshot` and subscribe after `last_seq`. Discard buffered events at/below that checkpoint. A later gap repeats recovery. If parsing or snapshot validation fails, retain the prior state and surface a recoverable stream error without logging private payloads.

`EventSnapshot` is an in-memory handoff contract, not an implemented or newly named HTTP endpoint. The currently planned GET call response contains only `Call`, which is insufficient for gap recovery. #1, #18 and #23 must finalize an owner-authorized consistent snapshot response before #22 wires recovery. This issue does not add a route or implement their persistence work.

## Fixtures and verification

`packages/ts/api/test/fixtures/events.json` contains synthetic projection data, not Arthur's scenario runner from #5. It uses a fictional phone number and no real user data. No provider or application network request runs in these tests.

Run `moon run client:test` for the Node 24 built-in test runner. No new dependency was added. Tests cover partial/final replacement, duplicate/older replay, sequence gaps, context changes, obsolete response IDs, approval/playback ordering, terminal calls, evidence references, cursor expiry, snapshot rejection and malformed events. Run the repository gate with `moon run :build :lint :typecheck :test` before publishing.

## Server stream (#23)

`packages/go/handlers/events` serves `GET /v1/calls/{id}/events` and
`GET /v1/calls/{id}/snapshot` behind the session middleware; another owner's call is
404. The cursor is `Last-Event-ID` when present, otherwise `?after_seq=` (absent means
0). The handler subscribes to the in-process hub (`packages/go/events`) **before**
reading the replay from the repository, writes replayed events, then a `: connected`
comment, then live events, skipping any `seq` already sent. If a published event
arrives ahead of an earlier commit, the gap is filled from the repository so `seq`
stays contiguous. Heartbeats are `: heartbeat` comments every 15 s.

A cursor beyond the latest `seq`, or more than 500 events behind, is expired:
409 `conflict` with `field: "after_seq"`, and the client hydrates the snapshot. A
subscriber that falls 256 events behind is disconnected and reconnects with
`Last-Event-ID`. Events are published only after their transaction commits, so the
stream never shows an event that a restart could lose. The handler reads through
the #13 repository (`EventsAfter` pages of 100, `Snapshot`); the snapshot route adds
the latest `summary.ready` and `usage.updated` payloads from the log. A cursor is
valid only if that exact `seq` exists for the call (or is 0).

Integration tests use `repositorytest.Open`, which follows the repository suite's
convention (`SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL` naming a `*_test` database, one
disposable schema per test; see `packages/go/repository/README.md`). They cover
wrong-owner denial, replay cursors, two subscribers, storage gap fill, expired
cursors, disconnect cleanup and replay after a restart with a new connection pool.
