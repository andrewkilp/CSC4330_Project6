# Contracts and ownership

The API serves `GET /health` with `{"status":"ok","mode":"template"}` and implements session plus call draft creation/read endpoints below. The remaining routes describe the implementation contract for their owning issues. The schemas were frozen by #1 on November 10, 2026. Their executable form is `packages/ts/api/src/types.ts` (types, enums, `LIMITS`, request/error parsers), checked by `packages/ts/api/test/contracts.test.mjs` against the reviewed samples in `packages/ts/api/test/fixtures/contracts.json`. Server metadata timestamps are RFC 3339 UTC with `Z`; a goal's `preferred_at` carries the business's local offset; all durations use milliseconds unless a field ends in `_seconds`. Business scheduling includes an explicit IANA time zone.

## HTTP API

HTTP JSON, versioned under `/v1`. Client calls go through packages/ts/api. The proto directory is retained as a boundary from Project 5; there is no active protobuf generator or Connect dependency. A contract change requires updating this document, fixtures and both language types in one PR.

| Method and path | Request / response | Owner task |
| --- | --- | --- |
| POST /v1/session | `{}` creates a session; secure HttpOnly cookie, account id returned | T08 |
| DELETE /v1/session | `{}` ends the current session | T08 |
| POST /v1/calls | `{mode, destination, business_name, goal, approval_mode, consent_acknowledged, idempotency_key}` -> Call | T18 |
| GET /v1/calls | `?cursor=&limit=20` -> `{calls, next_cursor}` | T33 |
| GET /v1/calls/{id} | Call, private to session owner | T18 |
| POST /v1/calls/{id}/start | `{idempotency_key}` -> Call; explicit action, never implicit in create | T11 |
| POST /v1/calls/{id}/instructions | `{text, expected_revision, idempotency_key}` -> Instruction | T28 |
| POST /v1/calls/{id}/responses/{response_id}/approve | `{context_revision, edited_text?, idempotency_key}` -> Response | T29 |
| POST /v1/calls/{id}/control | `{action: pause\|resume\|end, idempotency_key}` -> Call | T31 |
| GET /v1/calls/{id}/events | SSE, Last-Event-ID and initial `?after_seq=` supported; owner-gated | T23 |
| GET /v1/calls/{id}/snapshot | CallSnapshot `{call, last_seq, turns, instructions, responses, summary?, usage?}` from one consistent checkpoint; owner-gated | T23 |
| GET /v1/calls/{id}/summary | Summary, available only after terminal state | T39 |
| DELETE /v1/calls/{id} | Ended calls only, deletes associated text and events | T38 |
| GET /v1/providers | `{mode, telephony, stt, llm, tts}` availability booleans and user-safe reasons, never keys | T40 |
| POST /webhooks/twilio/voice | Signed provider request -> TwiML with media stream | T16 |
| POST /webhooks/twilio/status | Signed status callback, idempotent | T16 |
| WSS /webhooks/twilio/media | Provider audio, tied to verified call and stream | T21 |

Normal creates return 201; reads and actions 200; deletes 204. Errors: `{error:{code,message,field?}}`; `message` is 1..500 user-safe characters and `field` names the offending request field (for example `goal.party_size`), never the rejected value. Codes: `invalid_argument` 400, `unauthenticated` 401, `not_found` 404 (also wrong owner), `conflict` 409 (stale revision/state), `rate_limited` 429, `provider_unavailable` 503, `internal` 500. Do not return vendor errors or credentials. Mutations accept only a JSON body of at most 16 KiB and reject unknown fields with `invalid_argument` naming the field; the browser never supplies `owner_id`. `consent_acknowledged` must be the literal `true`. List `limit` is 1..100 (default 20); `cursor` is an opaque string of at most 200 characters.

Stale actions are `conflict` (409) with `field` set to the stale input:

| Situation | `field` |
| --- | --- |
| Instruction `expected_revision` differs from the call's `context_revision` | `expected_revision` |
| Approval `context_revision` differs from the call's, or the response belongs to an older revision | `context_revision` |
| Response is not `suggested`, or the call is not `connected` (including `paused`) | `status` |
| Idempotency key reused with different input | `idempotency_key` |
| Control action not allowed from the current status, such as resume after end | `action` |

The reviewed stale-approval body is `{"error":{"code":"conflict","message":"This suggestion is out of date. Review the newest suggestion before approving.","field":"context_revision"}}`. Mutations accept only a bounded JSON body; session cookies, CSRF/origin protection, and destination limits must precede public real-call access.

## Domain types

- `Goal`: `{kind: restaurant|appointment, preferred_at, timezone, party_size?, service?, constraints: string[], contact_name, contact_phone?}`. `preferred_at` includes offset; `timezone` is a valid IANA name recording business intent. A restaurant goal has `party_size` (integer 1..20) and no `service`; an appointment goal has `service` (1..160 characters) and no `party_size`. Constraints: at most 12 strings of 1..200 characters. `contact_name` 1..120 characters; `contact_phone` E.164 and only shared if present. No model may invent private details.
- `Call`: `{id, owner_id, mode: mock|real, destination, business_name, goal, status, approval_mode: always|uncertain, context_revision, created_at, updated_at}`. `status` is one of `draft|dialing|connected|paused|ended|failed|cancelled`. Destination is E.164; mock calls must use the fictional North American range `+1NXX55501XX` (555-0100..0199), which the client and server both reject otherwise. Real-mode destinations are further limited by the server allowlist. Business name 1..120 chars. The browser never supplies owner_id.
- `Instruction`: `{id, call_id, text, context_revision, status: pending|applied|failed, created_at}`. Text 1..1000 chars. Applying one increments the context revision atomically, invalidating older suggestions and queued audio.
- `Turn`: `{id, call_id, speaker: assistant|business, text, is_final, started_at_ms, ended_at_ms?, response_id?}`. Text is at most 10,000 characters (empty allowed for a partial). Partial updates replace the same turn id. Only finalized business turns trigger generation. Spoken assistant turns follow playback acknowledgement, not the model's draft. When the business interrupts (#26), the response is cancelled and its assistant turn holds only the words heard before the interruption: a whole-word prefix of the response text, measured from acknowledged per-chunk playback marks. Nothing is recorded if no audio had played.
- `Response`: `{id, call_id, text, context_revision, status: generating|suggested|approved|speaking|spoken|cancelled|failed, needs_approval, reason?}`. Text is bounded to 1500 chars, may be empty only while `generating` or after `cancelled`/`failed`, and must fit the goal. `reason` (why approval is needed) is at most 1000 chars. An approval's optional `edited_text` is 1..1500 chars and replaces the suggestion before speech. Approval binds to id and revision. No TTS may start for a stale, unapproved or cancelled response.
- `Summary`: `{call_id, outcome: confirmed|declined|needs_follow_up|failed|cancelled, confirmed_details: {business_name, scheduled_at?, timezone?, party_size?, service?, confirmation_reference?}, evidence_turn_ids: string[], unresolved_questions: string[], next_steps: string[]}`. A confirmed outcome requires evidence in finalized business turns. Preserve unknowns as absent fields. Lists hold at most 100 entries of at most 1000 characters; `confirmation_reference` at most 200.

Identifiers are server-generated UUIDs. Idempotency keys are caller-generated UUIDs scoped by owner, action and call; the same key with different input returns conflict. Limit list pages to 100. Never reuse a provider call SID as authorization.

## State and events

Call state: `draft -> dialing -> connected -> ended|failed|cancelled`; draft/dialing may also end or fail. `paused` is an explicit substate of connected and is serialized as `status: "paused"` (matching the `calls.status` column from #3; the Go state machine's `Paused` flag maps to it). Allowed: `connected <-> paused`, and `paused -> ended|failed|cancelled`. Pausing preserves the connection, stops the assistant and retires the active suggestion. End is terminal. The lifecycle owner makes transitions atomic and defines status-callback mappings. Pause, new instruction, remote speech and end cancel obsolete generation/audio with a generation token. Resume generates against the newest final turns and instructions only.

Each durable event has `{id, call_id, seq, type, occurred_at, context_revision, payload}`. seq is strictly increasing per call. SSE `id` is the decimal seq, `event` is type, and `data` is the JSON event. Types: `call.updated` (Call), `turn.updated` (Turn), `instruction.updated` (Instruction), `response.updated` (Response), `summary.ready` (Summary), `provider.error` (`{provider: telephony|stt|llm|tts, code, retryable}`; `code` is a stable user-safe string of at most 100 chars, never a vendor message), `usage.updated` (`{elapsed_seconds, estimated_cost_usd, estimate_only:true}`). Replay is bounded and reports an expired cursor explicitly; reconnect must resync the call and turns if the cursor is too old. Heartbeats are SSE comments and have no durable seq. Recovery uses `CallSnapshot` `{call, last_seq, turns, instructions, responses, summary?, usage?}` from one consistent checkpoint (see `docs/events.md`); an expired cursor is `conflict` with `field: "after_seq"`. #23 serves it at `GET /v1/calls/{id}/snapshot`.

Media uses Twilio's documented base64 8 kHz mono mu-law frames. STT input conversion is explicit and tested for the selected provider; TTS output requests mu-law 8000 when available. Use media, mark and clear events; bound audio queues. A playback mark means the provider buffer progressed, not that a business confirmed a booking.

## Provider interfaces

Stage 1 owns definitions in packages/go/providers and packages/ts/api; signatures below describe capability, not a mandated language syntax:

| Interface | Capability |
| --- | --- |
| Telephony | Start(call, callback URLs) -> provider id; End(provider id); authenticated media lifecycle |
| Transcriber | Open(call, audio format) -> incremental Turn updates; Push audio; Close/cancel |
| Responder | Generate(goal, finalized turns, applied instructions, context_revision) -> cancellable Response stream |
| Synthesizer | Stream(approved text, voice, generation token) -> formatted audio chunks; cancellation |

Mocks use those same interfaces and event types, with deterministic fixtures and no network. Provider decision (see `docs/provider-matrix.md`): the custom pipeline Twilio Media Streams + streaming STT + Claude Messages + ElevenLabs TTS. Managed ElevenLabs Agents was rejected because it offers no per-response approval hold. The STT vendor is chosen in #20 between ElevenLabs Scribe Realtime and Deepgram. No provider is silently substituted in a real call; a provider failure is visible and pauses or ends as documented. `uncertain` mode still requires approval for missing facts and commitments outside the authorized goal; `always` is the default demo mode.

## Limits

| Limit | Value |
| --- | --- |
| Real-call duration | 180 s, enforced server-side (#11, #34) |
| Concurrent real calls per account | 1 |
| Mutation body | 16 KiB |
| List page | default 20, max 100 |

Character limits are UTF-16 code units, matching JavaScript `length`. Go implementations must count the same way (`len(utf16.Encode([]rune(s)))`, not `len(s)` bytes) so a value accepted by one side is not rejected by the other.
