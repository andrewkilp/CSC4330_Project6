# Call storage

The PostgreSQL 17 migration at `db/migrations/00001_calls.sql` uses Goose Up and Down sections. Run it against a disposable database for verification.

```sh
goose -dir db/migrations postgres "$DATABASE_URL" up
goose -dir db/migrations postgres "$DATABASE_URL" down
```

`session_owners` stores the stable owner identity. Calls reference that identity, and the owner, creation time and call ID index supports paged history. Session credentials and authorization belong to the session and repository implementations.

Create a call and its initial goal at revision zero in one transaction. Goal rows preserve snapshots and can skip revisions when instructions change without changing the goal. Read the latest goal at or below the call revision. Lock the call or compare its expected revision when applying an instruction, and commit the revision change, instruction and event together. The database allows one applied instruction per call revision. State transitions, approval and stale response checks belong to the service.

Insert durable events in the same transaction as their corresponding state changes. Serialize sequence allocation on the call row to keep per-call sequence numbers strictly increasing under concurrent writes. The unique call and sequence index also supports ordered replay. Events reject updates. Deletes are reserved for retention and call erasure.

Idempotency records use the owner, action, optional call scope and caller UUID. Call creation uses a null scope and stores the created call ID in the result column. Later actions use the target call ID for both. Compare a deterministic request hash before replaying a result. Create the record in the mutation transaction, and roll back and read the winning record if a concurrent transaction claims the same key.

## Deletion order

Authorize against the call owner and enforce the terminal-state requirement in the deletion transaction. Delete the call to cascade through its idempotency records, events, turns, responses, instructions and goal snapshots. The deferred turn-to-response reference lets both rows disappear in the same transaction while keeping response references scoped to the same call. For manual child cleanup, delete idempotency records and events first, then turns, responses, instructions and goals, followed by the call. Delete the owner only after all owned calls are gone. Down drops the schema in reverse dependency order and destroys all stored data.
