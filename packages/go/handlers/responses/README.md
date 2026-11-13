# Response approval boundary (#29)

`New(Config)` serves the existing
`POST /v1/calls/{id}/responses/{response_id}/approve` contract. It requires a
trusted context owner resolver, a server-owned call lookup, and an explicit web
origin. The browser cannot provide ownership. Wrong owners get the same 404 as
missing calls, including on idempotent retries. Same-origin JSON checks reject
cross-site form submissions; native JSON requests without browser Origin headers
still require authenticated context identity.

The API now registers the handler with the shared session middleware, repository
and configured origin. It injects `conversation.DurableApprovals.Approve` rather
than an in-memory loop. Missing storage returns an authenticated 503. The
original `Lookup` mode remains available for loop/component consumers and tests.
With an empty configured origin all browser Origin headers are rejected; native
requests still require a valid session. Never derive identity from a body field,
URL, arbitrary header or provider SID.

`Loop.ApproveResponse` performs the response/revision/state check and optional
1..1500 UTF-16-unit edit under the same lock as instructions and remote speech.
It emits exactly one approved event. The local idempotency journal distinguishes
omitted edits from supplied edits, rejects changed input under the same UUID,
and returns the original acknowledgement for an identical retry. Its 128-entry
limit rejects new approvals without evicting old keys. This journal and events
are in memory, not persistent transaction/restart evidence. #18/#28 integration
must persist request results and state/events atomically; the local method alone
does not meet that production guarantee. The API's durable mode below supplies it.

## Durable approval and server-only handoff

`DurableApprovals` commits the stored response edit, approved state,
`response.updated` event and original idempotency result under one owner-scoped
repository call lock. Identical retries survive a service restart and return the
original acknowledgement; they are not a new speech authorization. Edits omitted
and explicitly supplied remain distinct. Wrong owners cannot read cached results.

Approval requires a connected call, current revision, suggested response, and the
latest published response checkpoint. Any newer business speech (including a
provisional turn), a superseding response or ambiguous active responses fails
closed. Revision/lifecycle writers must use the same repository Update lock and
publish corresponding entity transitions atomically. #28 owns the instruction
endpoint and generation cancellation; this task does not add that endpoint or
construct a parallel process-local loop from a snapshot.

`ClaimForSynthesis` is a server-only consumer boundary, not an HTTP route. It
rechecks ownership, connected state, revision, current response and interruption,
then atomically claims approved -> speaking and returns only the stored response
text. A concurrent/restarted consumer cannot claim it again. Speaking means
pending playback; it is never a spoken assistant turn. An uncertain worker must
not automatically repeat synthesis; worker recovery and playback acknowledgements
remain separate integration work. The API approval route does not call TTS or
start playback. The existing loop's SynthesizeApproved remains the component
boundary used by its playback implementation; a future runtime must choose one
authoritative state path rather than applying both to the same live response.

Optional event fanout runs only after commit. The mounted SSE stream's durable
poll also picks up committed approvals when no process-local publish hint exists.
No migration or vendor request is required by this change.

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
Actual PostgreSQL API tests additionally prove concurrent duplicate approval,
restart replay, owner/revision/lifecycle/interruption rejection, rollback without
publication, edited-only single-claim handoff and no manufactured transcript.
The approval UI remains #32; the complete real conversation runtime is not mounted.
