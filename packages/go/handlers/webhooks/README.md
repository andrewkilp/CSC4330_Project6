# Signed Twilio callbacks (#16)

`New(Config, Ledger)` returns a handler with `Register(*http.ServeMux)` for the
existing `POST /webhooks/twilio/voice` and `POST /webhooks/twilio/status` contracts.
No browser session or supplied owner ID authorizes these routes. API composition
mounts them with the existing shared repository and server-only configuration.
This package performs no dialing, model generation or audio processing.

The API's optional `TWILIO_WEBHOOKS_ENABLED` accepts only exact `true`/`false` and
defaults to `ENABLE_REAL_CALLS`. Explicit `true` preserves callback-only ingress
after new outbound dialing is disabled; account SID, Auth Token and canonical
`PUBLIC_BASE_URL` are still required, while source number/allowlist and outbound
REST client are unnecessary. Default-disabled mode ignores unused credentials and
mounts neither callback route. Real starts with explicitly disabled callbacks are
rejected at startup. Missing optional database storage remains an explicit 503;
the API creates no fallback call identities or second pool.

Configuration: `AccountSID`, primary account `AuthToken`, `PublicBaseURL` (exact
HTTPS origin used by #11), `MediaURL` (server-owned WSS endpoint without query),
`MediaEnabled`, and an optional `Publish(context.Context, []repository.Event)`
post-commit hint. Credentials and callback URLs/tokens must never appear in logs.
The callback attempt token is a separate random 32-byte base64url value from #11;
only its hash is persisted in the durable start ledger.

## Signature and parsing

Protocol checked November 13, 2026 against Twilio's
[request-validation documentation](https://www.twilio.com/docs/usage/security),
[official Node validator](https://github.com/twilio/twilio-node/blob/main/src/webhooks/webhooks.ts)
and [official Python validator](https://github.com/twilio/twilio-python/blob/main/twilio/request_validator.py).
The primary Auth Token form protocol uses HMAC-SHA1 and a Base64 signature. All
decoded POST fields, including unknown fields, participate in case-sensitive name
order. Repeated values are sorted and deduplicated as in the Node/Python helpers.
Identity/status fields must still occur exactly once; duplicate signed identity
values never introduce an ambiguous authorization decision. JSON/bodySHA256 and
SharedKey signature settings are unsupported and fail closed.

The signed URL is built exclusively from configured origin, exact route path and
original raw query. Request Host, Forwarded and X-Forwarded-* cannot change it.
The configured explicit-port and no-port variants are accepted, accommodating
Twilio HTTPS voice signature behavior. Query bytes are never re-encoded or mixed
into form fields. Leading/trailing form-value whitespace is preserved. Strict
Base64 decoding and constant-time MAC comparison verify the signature.

Only uncompressed UTF-8 `application/x-www-form-urlencoded` requests are accepted.
Bounds: 16 KiB body, 2048-byte raw query, 128 distinct form names, 100-character
ASCII names, 16 values per name and 4096 bytes per decoded value. Invalid percent
encoding, semicolons, NULs, invalid UTF-8 and ambiguous signature/content headers
are rejected before storage. Unknown signed form fields are included in the MAC
and otherwise ignored. Query must contain exactly one each of `call_id`,
`attempt_id` and `token`; the provider's `AccountSid` must match configuration and
`CallSid` must have the expected SID shape. `r.Form` is never used because it
would merge query/body identities.

## Durable owner and lifecycle fence

After signature validation, the handler invokes #11's
`Repository.ApplyTwilioCallback`. Its start ledger validates call/attempt/token
hash/account/mode/SID together and derives the owner from trusted storage. It can
bind the first verified callback SID before the outbound REST response returns;
the later REST acknowledgement must match it. Mock starts reject Twilio callbacks.
No callback can create a call or start a second outbound request.

Voice only associates the SID; it does not invent a connected transition. Status
mapping is centralized in the ledger: queued/initiated/ringing -> dialing,
in-progress -> connected, completed -> ended, canceled -> cancelled, and
busy/no-answer/failed -> failed. Older dialing callbacks cannot regress connected
or paused calls; in-progress does not resume paused calls; terminal states never
reopen. Duplicate/out-of-order callbacks acknowledge without duplicate events.

Status and `call.updated` are committed atomically before `CallbackResult` returns
or `Publish` runs. The optional publisher is a prompt best-effort fanout hint, not
a durable delivery guarantee; #23 owns replay. A database/event-insert failure
returns a generic retryable 503, rolls back state, and publishes nothing.
Unknown or conflicting associations produce generic 404s; invalid signatures 403.

## Voice and the pending media bridge

The [Stream reference](https://www.twilio.com/docs/voice/twiml/stream) defines WSS
media URLs and nested custom parameters. With media explicitly enabled and a
nonterminal owned call, the handler returns XML-escaped `<Connect><Stream>` with
the configured URL and verified `call_id` and `attempt_id`, plus
`token = media.SignStream(MediaSecret, call_id, CallSid)`. The webhook's own callback
token never appears in TwiML. The #21 bridge recomputes that HMAC and checks the
stored provider SID, so the stream credential is bound to this exact provider call.
These values alone do not authorize a browser or prove playback.

`MediaEnabled` requires a `MediaSecret` of at least 32 bytes. The API sets it only
when it also mounts the #21 bridge (see `docs/providers/twilio-media.md`). Otherwise
verified voice callbacks receive safe Hangup TwiML, and terminal calls always do.

## Verification

```sh
go test -race -timeout 90s ./packages/go/handlers/webhooks/...
node scripts/verify-fixtures/run.cjs --ci
```

Unit tests use a published Twilio signature vector and an independently signed
synthetic outbound fixture (reserved fictional destinations). They cover unsigned,
tampered, malformed, oversized and ambiguous requests; unknown/repeated fields;
host/proxy spoofing; query/body shadowing; route methods; safe failures; escaped
TwiML; media/terminal gates and publish-after-commit ordering.

With `SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL` set to an explicitly disposable
database whose name ends in `_test`, integration tests create/drop isolated
schemas and apply migrations 00001..00003. Actual database tests cover signed
callback-before-REST association, duplicate/out-of-order delivery, durable event
counts, terminal fencing, wrong association/mode, and event-failure rollback.
Without this variable, database tests report skipped; fixture tests still run.
No real Twilio endpoint, paid request, public callback tunnel or real call was used.

`apps/api/call_pipeline_test.go` additionally runs the composed session/draft/start/
callback router with actual isolated PostgreSQL storage and a process-local fake
Twilio HTTP transport. It checks create/read never dial, one explicit outbound
request across retries, signed callbacks before the REST acknowledgement,
bad signatures, duplicate/out-of-order delivery, four durable lifecycle events,
and terminal voice Hangup. Unexpected outbound destinations are rejected by the
fake transport; no real provider request occurs.

## Failure recovery (#36)

| Fault | Behavior | Evidence |
| --- | --- | --- |
| Retried or duplicate status callback | The ledger applies it idempotently and status mapping is monotonic (`repository.TwilioStatus`). A retry after a 503 storage outage applies once. | `testdata/faults.json`, `faults_test.go` |
| Out-of-order or late callback | Never regresses `connected`/`paused` and never reopens a terminal call | same |
| Invalid signature | 403 before storage; nothing is applied or published | same, plus `TestRejectsBeforeLedger` |
| Busy, no answer, provider failure | The call becomes `failed`, and a user-safe `provider.error` (`call_busy`, `call_no_answer`, `call_failed`) is recorded and published once, only by the callback that failed the call | same |
| Media socket drops without `stop` | `media.Config.OnClose` reports it once, after recognition has closed. The API ends the call's live work, hangs up the provider call and fails the call with `media_disconnected`. A clean stop or server shutdown records nothing. | `telephony/media/close_test.go`, `apps/api/media_routes_test.go` |
| Orphan dialing (no answer or callback, ambiguous start) | `telephony.Reconciler` sweeps every 30 s. A call dialing for more than 90 s is hung up (through the live registry, or the stored SID after a restart), then failed with `dial_timeout`. It never redials. Each sweep handles at most 50 calls, and every hang-up has a 5 s timeout. A start still in flight is hung up when the provider answers. | `telephony/reconcile_test.go`, `repository/starts_integration_test.go` |

**Logs.** Logs carry the route, our call id, the HTTP status, the call status and Twilio's `I-Twilio-Idempotency-Token` as the correlation id; a malformed header is dropped. They never carry form values, SIDs, tokens, phone numbers, signatures or transcript text (`TestCallbackLogsAreRedactedAndCorrelated`). `provider.error` payloads are `{provider, code, retryable}` with fixed codes, never vendor messages.
