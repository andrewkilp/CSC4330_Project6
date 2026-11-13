# Call controls (#31)

`POST /v1/calls/{id}/control` with `{action: pause|resume|end, idempotency_key}` returns the `Call`. It is owner-gated, takes a bounded JSON body, and rejects unknown or duplicate fields.

| Action | From | To | Live effect (`telephony.Calls`) |
| --- | --- | --- | --- |
| `pause` | `connected` | `paused` | Cancel generation and synthesis, clear queued audio; the line stays open |
| `resume` | `paused` | `connected` | Regenerate from the newest turns and instructions, in the background |
| `end` | `connected`, `paused` | `ended` | Cancel everything, close streams, hang up |
| `end` | `draft`, `dialing` | `cancelled` | Same. A start still dialing is hung up as soon as the provider returns its id |

- **No-ops.** Pause on `paused`, resume on `connected`, and end on any terminal status return the call unchanged with no event. Pause or resume from any other status is `409 conflict` on `action`. A reused key with a different action is `409` on `idempotency_key`.
- **Order of work.** The state change commits first, in one `repository.Update` transaction. Pause and end also cancel unfinished replies in that transaction, emitting `response.updated` before `call.updated`. Events are published only after the commit.
- **Retries are safe.** Live effects run on every successful request, including idempotent replays. A retry after a crash between commit and effect therefore still stops audio and hangs up.
- **Hang-up failure.** It returns `503 provider_unavailable` with no vendor detail. The call is already terminal and published, and retrying the same request retries the hang-up.
- **Provider hang-ups.** A provider status callback that makes a call terminal (for example the business hanging up) also ends its live work.

Tests:
- `control_test.go`: state table, request validation, idempotency, effects, hang-up failure.
- `integration_test.go`: Postgres; runs when `SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL` is set.
- `packages/go/telephony/live_test.go`: race tests for end during dial, pause during synthesis, repeated end, late callbacks after end, and resume cancelled by end.
