# Owner-scoped Postgres repositories (#13)

`Repository` uses the caller's `pgxpool.Pool`; constructing it does not connect,
read environment variables or apply migrations. Apply the Goose migrations in
`db/migrations` before composing the repository with the API. `pgx/v5 v5.13.0`
provides the PostgreSQL wire driver, typed parameters, pooling and transactions;
its transitive pool/config/text dependencies are recorded in `go.mod`/`go.sum`.
No database or provider account is required by the default build/test command.

`Create(ctx, owner, key, Draft)` registers the server-derived session owner and
inserts a draft call, revision-zero goal, first `call.updated` event and
idempotency result in one transaction. It validates the stored goal, explicit
consent and fictional destination restriction in mock mode. It never starts a
call. The authenticated session layer supplies `owner`; never accept it from a
browser body. Registering an owner does not authenticate that identity.

`Update(ctx, owner, callID, action, key, request, fn)` locks the owned call row
with `SELECT ... FOR UPDATE` before reading an idempotency record or invoking
`fn`. Wrong-owner/missing calls both return `ErrNotFound`, without invoking `fn`.
The transaction exposes `Call`, `SetCall`, immutable `PutGoal`, entity
`PutTurn`/`PutResponse`/`PutInstruction`, child reads/collections/deletes and
`AppendEvent`. Entities always belong to that one owned call. Upserts cannot move
an existing child from another call. Finalized turns cannot be rewritten or
reopened; goal snapshots cannot change after insertion. Current goal selection
tracks either ordering of `SetCall` and `PutGoal` within the transaction.

The callback must execute synchronously and only use this transaction for
database work. It must not retain `Tx`, use it from other goroutines, call a
provider, publish an event, or perform irreversible side effects. Returned `Tx`
objects reject database use after the callback. Call snapshots detach pointer and
slice fields. Lifecycle transitions, approval/current-response checks,
cancellation and instruction policy remain service responsibilities (#28/#29).
The service appends corresponding durable events inside the same callback, then
publishes only after `Update` commits. Repository methods do not guess policy or
automatically synthesize events for every low-level row operation.

`AppendEvent` increments `calls.last_event_seq` while the call is locked, and
inserts the event in the same transaction. Rollback undoes both the counter and
entity/event changes. The database's unique `(call_id, seq)` constraint remains a
second boundary. The counter migration backfills existing maximum sequences and
does not reuse sequences after event deletion. `clock_timestamp()` records append
time rather than a waiting transaction's older start time. Payloads are bounded
JSON objects; event domain shape belongs to the service's contract implementation.

Idempotency is scoped by `(owner, action, call-or-null, UUID key)`. Actions are
server constants. JSON object keys/whitespace are normalized before SHA-256
hashing; string values and number literals retain their meaning. The original
JSON action result is stored atomically, so replay returns that result even after
the call changes. A different request with the same scope/key returns
`ErrConflict`. Existing calls serialize on their row. Creation has no call row
yet, so a transaction-scoped advisory lock on the owner/create/key scope serializes
claims. This deliberately avoids the unique-constraint loser/rollback/re-read
algorithm described in the original storage sketch; the same database uniqueness
constraint is retained. Advisory hash collisions only cause extra serialization,
never authorization or result sharing.

`Snapshot` reads call, selected goal, event checkpoint and child entities under one
repeatable-read transaction. Child collections stop at 4096 rows and fail
explicitly when larger, instead of silently truncating. `GetCall` reads only the
call/goal, and `EventsAfter` reads at most 100 ordered durable events with the same
owner fence. This is not call-history pagination, SSE fanout or cursor-expiry
policy (#23/#33). `DeleteCall` separately locks the owner-scoped call and requires
a terminal status, then relies on schema cascades. No retention job is added.

All SQL values use parameters; dynamically chosen entity operations use fixed
statement strings. The database connection is trusted server infrastructure;
these owner predicates are not a claim of row-level security for arbitrary SQL
clients. Errors are stable sentinels, and SQL/callback details are not returned.
`ErrConflict` includes stale/reused keys, immutable rows and serialization/deadlock
failures. The caller may retry the **same key and request**; callbacks are never
automatically retried around external work. Migration 00002 adds the event counter
and stored idempotency result, with explicit Goose Down statements.

## Database verification

Default `go test ./packages/go/repository/...` runs pure validation tests and
honestly skips integration tests unless `SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL`
is set. Integration tests require a database whose name ends in `_test`, create
a random `repository_test_<UUID>` schema, apply both migrations there, and drop
only that schema afterward. Never point tests at a user database. No public
provider endpoints or real calls are contacted.

For a disposable database with no persistent volume:

```sh
docker compose -p switchboard-repository-test -f docker/compose.yml --profile repository-test up -d repository-test-db
docker compose -p switchboard-repository-test -f docker/compose.yml port repository-test-db 5432
```

Use the reported loopback port in the test URL. In PowerShell:

```powershell
$env:SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL = 'postgres://switchboard_test:test-only-local@127.0.0.1:PORT/switchboard_repository_test?sslmode=disable'
go test ./packages/go/repository/... -v
go test -race ./packages/go/repository/... -count=3
go vet ./packages/go/repository/...
moon run :build :lint :typecheck :test
docker compose -p switchboard-repository-test -f docker/compose.yml --profile repository-test down
```

On POSIX shells, export the same environment variable. Windows race tests need
CGO plus a working C compiler; verification used MSYS2 UCRT64 GCC on PATH.
The integration suite checks cross-owner reads/writes/deletion and foreign-child
access, 24 concurrent appenders, 24 concurrent identical mutations, 12 concurrent
create retries, original-result replay, CRUD and erasure cascades, finalized-turn
immutability, revision/goal ordering, rollback after callbacks and deferred FK
commit failures, counter backfill and migration Down paths. The schema isolates
each test; multiple test processes can use the disposable database independently.

Verified November 13, 2026 on Windows with Go 1.26.4 and an isolated
`postgres:17-alpine` container reporting PostgreSQL 17.12: all eight database
integration tests passed, the database suite passed race detection with three
repetitions, and inherited `moon run :build :lint :typecheck :test` completed
**37 tasks** including web export. The temporary container used no persistent
volume. This is real database evidence, not a mocked SQL claim.

Official references: [pgxpool](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool),
[PostgreSQL row locks](https://www.postgresql.org/docs/17/explicit-locking.html),
[PostgreSQL transaction isolation](https://www.postgresql.org/docs/17/transaction-iso.html).
