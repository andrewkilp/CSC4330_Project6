# Response approval boundary (#29)

`New(Config)` serves the existing
`POST /v1/calls/{id}/responses/{response_id}/approve` contract. It requires a
trusted context owner resolver, a server-owned call lookup, and an explicit web
origin. The browser cannot provide ownership. Wrong owners get the same 404 as
missing calls, including on idempotent retries. Same-origin JSON checks reject
cross-site form submissions; native JSON requests without browser Origin headers
still require authenticated context identity.

The template API does **not** register this handler yet. #18's call repository
and #28's transactional instruction/event integration are not implemented on this
branch. Integration must provide a stable per-call `conversation.Loop`, trusted
`OwnedCall` metadata and identity middleware (the #8 PR exposes `auth.OwnerID`).
Do not derive identity from a body field, URL, arbitrary header or provider SID.
Do not substitute a new unauthenticated call endpoint to make this handler usable.

`Loop.ApproveResponse` performs the response/revision/state check and optional
1..1500 UTF-16-unit edit under the same lock as instructions and remote speech.
It emits exactly one approved event. The local idempotency journal distinguishes
omitted edits from supplied edits, rejects changed input under the same UUID,
and returns the original acknowledgement for an identical retry. Its 128-entry
limit rejects new approvals without evicting old keys. This journal and events
are in memory, not persistent transaction/restart evidence. #18/#28 integration
must persist request results and state/events atomically; the local method alone
does not meet that production guarantee.

Approval never starts speech automatically. Server-side `SynthesizeApproved`
claims only the current approved response, constructs the speech request from its
stored text and token, and prevents a second synthesis claim. It accepts no
browser text. The voice and format are server configuration. Instructions,
pause, remote speech and end cancel an active synthesis context and fence late
chunks. Provider output must preserve format, token, sequence and a final chunk;
failure retires the response. Playback, buffer clearing and spoken assistant
transcript acknowledgement remain #30/#26 responsibilities. An in-flight callback
may finish during invalidation, so downstream queues must recheck its generation
token at enqueue/playback; do not treat an approved HTTP retry as speech authority.

Validation:

```sh
go test ./packages/go/conversation/... ./packages/go/handlers/...
go test -race ./packages/go/conversation/... ./packages/go/handlers/...
moon run :build :lint :typecheck :test
```

Tests exercise the actual loop behind HTTP mock storage: wrong owner, missing
session, stale approval after instructions, duplicate and changed requests,
concurrent retries, pause/end/remote-speech conflicts, edited-only synthesis,
UTF-16 limits, malformed/oversized JSON and cross-site requests. Channel-based
cancellation tests prove stale synthesis cannot publish into the newest response.
Fixtures and injected synthesizers perform no external provider requests or calls.
This is an integration-ready component, not a connected UI or live calling demo.
