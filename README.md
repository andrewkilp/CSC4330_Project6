# Switchboard · CSC 4330 Project 6

A base template for an AI phone concierge: restaurant reservations and appointment scheduling, with a live transcript, suggested replies and private instructions during a call.

![Switchboard design direction](docs/design/switchboard-mockup.png)

**Current behavior:** an interactive local demo with streamed transcript text, private steering instructions, editable suggestions, approval, pause/resume, and simulated outcomes for restaurant reservations and appointments. It runs through the Go API using in-memory fictional data. It does not make real calls, invoke a language model or create real bookings.

## Start

Install [proto](https://moonrepo.dev/docs/proto/install), then:

```sh
proto install
pnpm install --frozen-lockfile
moon run api:demo
```

Open **http://127.0.0.1:8080**. The command builds the Expo web app and serves it with the demo API from one local origin. No keys or Docker are required. Stop the terminal command with Ctrl+C. Follow the [demo walkthrough](docs/demo.md).

For the normal health-only API, use `moon run api:dev`. `GET /health` returns `{"status":"ok","mode":"template"}`. The demo routes are mounted only with DEMO_MODE=true and bind to loopback.

For native development use `moon run mobile:dev`, or `moon run mobile:android` / `moon run mobile:ios` with the appropriate Android SDK / Xcode installed. For a phone or Android emulator set `EXPO_PUBLIC_API_URL` as documented in `.env.example`. Expo loads the app's local environment; the Go baseline reads shell environment variables directly. Do not copy server secrets into the mobile app.

## Check

```sh
moon run :build :lint :typecheck :test
```

CI uses the same command and a frozen pnpm lockfile. The baseline needs no Docker and no API keys. The optional database lives in docker/compose.yml. Real calling comes later and requires separate provider accounts and usage billing.

## Work from an issue

Read [AGENTS.md](AGENTS.md), [contracts](docs/contracts.md), [architecture and provider research](docs/architecture.md), [design](docs/design.md), and your issue. Use one branch and PR per issue. Keep the assigned scope and include real verification output. Claude Code and Codex share the same instructions.

The [development plan](docs/development-plan.md) assigns 50 tasks to five people, two tasks per person per stage. `scripts/issues.json` is the reproducible source; the publication report records actual GitHub issue numbers and assignees.

## Layout

The Project 5 structure is retained with its food-specific code and dependencies removed:

```text
apps/         Expo mobile/web preview and Go API
packages/     Shared Go handlers and TypeScript API client
db/           Future migrations
docker/       Optional local Postgres
proto/        Contract boundary and migration note
docs/         Mockup, contracts, research and development plan
scripts/      Issue manifest and publication report
.moon/        Shared tasks and project graph
.github/      CI, issue and PR templates
```

The source reference is [csc4330_project5](https://github.com/bengutow1/csc4330_project5). Project 6 preserves its organization and issue-writing style; it does not copy its history, secrets, product data or unrelated dependencies.
