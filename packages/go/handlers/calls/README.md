# Call drafts and private reads (#18)

`RegisterDrafts` exposes `POST /v1/calls` and `GET /v1/calls/{id}` through the
existing authenticated owner middleware. Both use the existing PostgreSQL
repository; browser bodies never supply an owner. A wrong owner and missing call
both return 404. Creation writes a `draft`, its goal and first durable event;
it cannot dial, generate text or invoke a provider. Start is a separate #11 action.

The handler accepts at most 16 KiB of UTF-8 JSON, rejects duplicate/unknown fields,
and validates both goal variants, fictional mock numbers, consent, UUID keys,
UTF-16 text bounds, real dates, named zones and the timestamp's actual zone offset.
Unknown or malformed data gets a field-only safe error. The repository supplies
database-level owner fences and replay of the original create result; the HTTP
layer doesn't replace that with process-local deduplication. No-store responses
preserve UTC server timestamps and the business's offset-bearing preferred time.

## Run locally

Apply the existing Goose Up migrations before using the call service. The API
does not automatically migrate or erase a database. With Goose available:

```powershell
docker compose -f docker/compose.yml --profile database up -d postgres
$env:DATABASE_URL = 'postgres://switchboard:switchboard@localhost:5432/switchboard?sslmode=disable'
goose -dir db/migrations postgres $env:DATABASE_URL up
$env:APP_ENV = 'development'
$env:APP_ORIGIN = 'http://localhost:8081'
moon run api:dev
```

In another terminal, set `EXPO_PUBLIC_API_URL=http://localhost:8080` and run
`moon run mobile:web`. Keep the same hostname on frontend and API; different
localhost ports are supported by the explicit `APP_ORIGIN`. Cookie policy and
the cross-site request guard deliberately require **same-site** hosting, not
unrelated frontend/API domains. Production requires HTTPS (including App Origin)
and should use the same-site reverse-proxy setup from deployment work. No wildcard
credentialed CORS is enabled. After changing the public API URL, restart/clear the
Expo bundler; a web export can use `pnpm exec expo export --clear --platform web`.

Without `DATABASE_URL`, baseline health/session/preview still run; draft operations
return an honest storage-unavailable error. A supplied but unavailable database
fails startup after a bounded connectivity check. No credentials or DSN are logged.

## Client and review flow

Only `packages/ts/api` performs API requests. `createCall` validates and freezes the
reviewed body, includes the HttpOnly session cookie, refuses redirects and never
retries an ambiguous mutation automatically. An unauthorized create first rechecks
under a session-recovery lock, opens a local session only when still needed, then
resubmits the exact key/body. Existing sessions survive app reloads. Reads never
silently create another owner. These are anonymous local sessions; restart/expiry
can invalidate access to old calls, as documented by #8.

Review's explicit **Save call draft** action generates its UUID with
`expo-crypto`, the SDK-57-compatible native/web dependency added solely for secure
cross-platform retry identifiers ([official API](https://docs.expo.dev/versions/v57.0.0/sdk/crypto/)).
The intent is retained in root memory before sending, so retry after a lost response
uses the same key. Saving opens the returned `/calls/{id}` and reads it from the
service; the page makes no start request. Reopening the same saved goal does not
create another draft. An expired/missing saved call clears only the cached call,
retaining the goal and key so the user can explicitly resave rather than enter a
recovery loop. Nothing private enters route query parameters or persistent browser
storage. Full live-call layout/events remain #12/#17/#22.

## Evidence

```sh
node scripts/verify-fixtures/run.cjs --ci
go test -race ./apps/api/... ./packages/go/handlers/calls/...
```

`SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL` enables actual HTTP/PostgreSQL tests in
an isolated random schema of a dedicated database ending `_test`; no user schema
is migrated or dropped. Tests cover both goals, malformed inputs, owner isolation,
idempotent creates and unchanged draft status. Client tests cover credentials,
concurrent/stale 401 recovery, exact retry bodies, safe failures and invalid reads.

The local browser rehearsal uses real API/PostgreSQL draft storage with fictional
data, and checks double-click/reopen deduplication, session revocation recovery,
and a response dropped **after commit**, followed by the exact-key retry. No
`/start` request is permitted by that check. Screenshots at 390/1440px are under
`docs/design/draft-api/`. Native build/type compatibility is checked, but no native
device or live provider call is claimed.
