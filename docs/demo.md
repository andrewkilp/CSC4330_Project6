# MVP mock-call demo

This branch composes the issue work into one runnable product slice: a mock call
from goal to summary, through the real server. The business is scripted and
fictional (#5); everything else is the actual pipeline:

| Step | What runs |
| --- | --- |
| Pick a scenario, review, create | `POST /v1/session`, `POST /v1/calls` (#18) on the owner-scoped Postgres repository (#13, `packages/go/repository`). Creating never dials |
| Place mock call | `POST /v1/calls/{id}/start` (#11, mock only). Mock telephony rings, connects and streams synthetic audio (#6) |
| Live transcript | Mock speech recognition turns audio into partial and final turns, stored and streamed over `GET /v1/calls/{id}/events` (#23) into the shared reducer (#9) |
| Suggested reply | The conversation loop (#19) with the goal-aware prompt and validation (#24); the scripted responder supplies the text |
| Approve or edit | `POST .../responses/{id}/approve` (#29). Nothing is spoken before approval |
| Speech | Mock synthesis queued to the media stream behind a mark; the reply becomes a transcript turn only when the mark returns (#30) |
| Private instruction | `POST .../instructions` (#28): one atomic context revision; the old suggestion is cancelled and regenerated |
| Pause / resume / end | `POST .../control` (#31). Pause clears queued speech; end is terminal |
| Summary | `summary.ready` with evidence from the call's own finalized business turns (#39) |

Real calling stays off: `GET /v1/providers` (#40) reports why, and `mode: "real"` is
rejected with `provider_unavailable`.

## Run it

```sh
proto install && pnpm install --frozen-lockfile
docker compose -f docker/compose.yml --profile database up -d
moon run mobile:build                       # exports the web app to apps/mobile/dist
cd apps/api
DATABASE_URL=postgres://switchboard:switchboard@localhost:5432/switchboard?sslmode=disable \
WEB_DIR=../mobile/dist go run .
```

Open http://localhost:8080, choose **Bayou Lantern Bistro (mock)**, confirm the
disclosure, **Create call**, then **Place mock call**. Approve the greeting. When the
restaurant offers 6:15 or 8:45, the suggestion needs approval; instead send the
scripted instruction *8:45 is fine. Do not accept 6:15.*, approve the revised reply,
approve the deferral about allergies, then **End call** to see the summary.

For Expo's dev server instead of the exported build, run `moon run mobile:web` and
start the API with `WEB_ORIGIN=http://localhost:8081` (CORS for that one origin) and
`EXPO_PUBLIC_API_URL=http://localhost:8080` for the app.

`MOCK_FRAME_DELAY_MS` (default 20, real time) and `MOCK_FRAMES_PER_UPDATE` (default
40) pace the scripted business. With `DATABASE_URL` set, the API applies pending
`db/migrations` Up sections at startup (recorded in goose's `goose_db_version`) and
then composes the repository.

Tests that need Postgres follow the repository suite's convention: set
`SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL` to a disposable `*_test` database (see
`packages/go/repository/README.md`); each test uses its own schema.

## Limits

- Mock only: the scripted business answers only the scenario's lines and anticipated
  instruction. Other instructions are accepted and applied, but the script has no
  reply for them, which the screen says.
- Live sessions are in memory; restarting the API leaves active calls without a
  runtime (the durable transcript and events remain).
- Runtime writes go through the repository's idempotent `Update` with a fresh key
  each, so each transcript update or response change adds an idempotency row;
  they are removed with the call.
- Response events are published only for the call's current context revision;
  an older response's final status is stored but not re-announced, because an
  event envelope carries the call's revision.
- The event stream uses the browser `EventSource`; native transport is #22.
- Not a substitute for #2, #7, #12, #17, #22, #27 and #32's full UI work.
