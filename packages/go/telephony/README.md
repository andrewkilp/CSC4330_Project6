# Explicit outbound call gate (#11)

`Client` implements the shared `providers.Telephony` REST Start/End boundary.
Constructing it never contacts Twilio. Media remains unavailable until #21;
there is no audio bridge, speaking of drafts or silent provider fallback here.
Twilio REST requests use server-configured Account SID/Auth Token HTTP Basic
authentication over a fixed HTTPS endpoint. The client sends one POST, follows no
redirects and performs no application retries. It validates response SID/account,
bounds responses to 32 KiB and uses a 15-second HTTP timeout. End only accepts a
syntactically valid Call SID and sends `Status=completed`.

Start requests specify the configured caller number, stored destination, POST
voice/status callbacks, initiated/ringing/answered/completed notifications,
`Record=false`, `Timeout=30` and `TimeLimit=180`. The provider-side time limit
implements the existing three-minute start contract; this adds no account usage,
readiness endpoint, billing estimator or concurrent-account controls (#35/#40).
Protocol references: [Twilio Call resource](https://www.twilio.com/docs/voice/api/call-resource)
and [Twilio transport/authentication security](https://www.twilio.com/docs/usage/security).

## Durable boundary around an irreversible POST

`NewStartService(store, realProvider, mockProvider, StartConfig)` selects the
adapter from the **stored call mode**. Mock mode uses the explicitly supplied
existing scenario adapter, validates the fictional destination range and never
touches the real adapter. Real mode requires enabled server settings, an exact
E.164 allowlist match, a configured HTTPS public origin and configured account.
The repository checks the stored consent acknowledgement while locking the owned
call. A browser supplies only its call ID and idempotency UUID, never owner,
destination, provider, consent override or callback URL.

`repository.ReserveCallStart` commits a `call_starts` intent and the durable
draft-to-dialing event **before** any provider request. The call ID is the primary
key: there can be only one intent for that call, independent of start keys,
process memory or restarts. Only the transaction that creates that intent gets
permission to contact the selected provider. The transaction ends before HTTP.
An unacknowledged/ambiguous intent can never acquire fresh dialing permission.
Another key always conflicts; the same key only replays a stored acknowledgement.

After an acknowledged SID, the service commits its association/result using a
bounded independent context so browser disconnection does not lose the known
SID. A valid callback can win that association race; REST must then return the
same SID. If HTTP times out, returns any error/malformed response, or SID persistence
fails, no second dial is attempted. A best-effort `ambiguous` marker is useful for
inspection, but safety rests on the already committed permanent intent even when
that marker cannot be saved. A crash between intent commit and HTTP can therefore
result in **zero** calls. A crash/network failure after acceptance can leave one
unconfirmed call. This is at-most-one application dial attempt, not an exactly-once
phone call or a claim that every request succeeds.

Uncertain attempts require manual reconciliation using provider records and the
recipient's current call state. There is no automatic retry or reset endpoint.
The fence is removed only as part of terminal call erasure; that call ID then no
longer exists and the application never reuses it. A repeated acknowledged key
returns the original stored call result even if subsequent callbacks advanced the
current call; use the read-call route to refresh current state.

## Correlation shared with #16

Each intent has a server-generated attempt UUID and 32-byte random callback token.
Only SHA-256 of the token is persisted. Fixed voice/status paths carry
`call_id`, `attempt_id` and `token` query values on the configured HTTPS origin.
Never log these full callback URLs, request forms, credentials or real numbers.
The media path is reserved for #21 and contains no query values.

`repository.ApplyTwilioCallback` is an internal method for #16 **after** HTTP
signature validation. It checks the intent's call/attempt/token hash, real mode,
account and SID. A valid callback may bind the SID before the REST response; a
different SID is rejected. Voice may bind identity with empty status without
inventing a connected event. Status mapping changes the call and appends its event
in one transaction. Duplicates, stale dialing updates and terminal regressions
produce no new events. Returned events can be published only after that commit.
The token/SID are correlation data and never replace Twilio signature validation.

## HTTP/API composition

`calls.NewStartHandler(service, sessions)` and `calls.RegisterStart(mux, handler)`
mount authenticated `POST /v1/calls/{id}/start`. Bodies must be JSON objects at
most 16 KiB, containing only `idempotency_key`. The handler resolves the owner from
the shared HttpOnly session, returns contract errors and never returns provider
bodies. Pending/conflicting attempts return 409; an unconfirmed provider attempt
returns a fixed 503 message without implying a safe redial. Parent API composition
must apply the shared browser Origin/CORS policy before mounting this mutation.

Parent #18 owns API pool/session composition and the create/read routes. It
constructs this service explicitly, with real provider disabled by default:

| Server setting | Meaning |
| --- | --- |
| `ENABLE_REAL_CALLS` | Must be explicitly enabled for real starts |
| `CALL_ALLOWED_NUMBERS` | Exact comma-separated E.164 destinations, no wildcard |
| `PUBLIC_BASE_URL` | HTTPS origin, no path/query/fragment/credentials |
| `TWILIO_ACCOUNT_SID` / `TWILIO_AUTH_TOKEN` | Server-only REST/callback configuration |
| `TWILIO_FROM_NUMBER` | Server-owned E.164 caller number |

A nil store fails closed with a safe unavailable result; it never substitutes
in-memory persistence. No startup, constructor, install script, build or test
dials a phone. Creating a draft is a separate operation and never starts it.

## Verification

Fixtures use a local HTTP server and fictional numbers/SIDs. DB tests require
`SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL` pointing to a disposable database whose
name ends in `_test`; otherwise they explicitly skip. Each test uses its own
random schema and drops only that schema. Apply all three Goose migrations for
application use; the test helper does so inside its isolated schema.

```sh
go test ./packages/go/telephony/... ./packages/go/handlers/calls/... ./packages/go/repository/...
go test -race ./packages/go/telephony/... ./packages/go/handlers/calls/... ./packages/go/repository/... -count=3
moon run :build :lint :typecheck :test
```

Actual PostgreSQL 17.12 plus local HTTP tests prove: committed intent exists when
the provider receives the request; 20 simultaneous identical keys produce one
HTTP dial; 20 simultaneous distinct keys produce one dial; fresh repository/pool
instances do not redial after restart; HTTP errors and post-dial DB failures never
rearm an attempt; blocked/disabled/wrong-owner/consentless calls cannot start;
mock mode never touches the real adapter; callbacks can bind before REST returns;
wrong token/account/attempt/SID/mode and callback regressions are rejected. HTTP
handler fixtures also cover authentication, strict input and safe failure mapping.
No Twilio endpoint, paid account, real phone, microphone or audio stream was used.

Verified November 13, 2026 with Go 1.26.4 on Windows: the real Postgres/local HTTP
race suites passed three repetitions; inherited moon checks completed **41 tasks**
including web export (initial full run 72.178 seconds, latest run 35 cached tasks
and six rechecked tasks in 6.580 seconds). The database was an isolated PostgreSQL
17.12 container with no persistent volume. JSON/provider fixtures are synthetic
documented protocol examples, not a live Twilio recording or real-call evidence.

Only standard-library HTTP/crypto is added for the provider. Existing repository
and pgx dependencies serve durable ledger tests; the existing mock module serves
mode-separation tests. There is no new vendor SDK or network retry dependency.
