# Switchboard

A CSC 4330 Project 6 starter for a phone concierge: restaurant reservations and appointment scheduling, a live transcript, suggested responses, and instructions entered during the call.

## Shared source of truth

This root `AGENTS.md` is the authoritative project guide for both humans and AI coding agents. `CLAUDE.md` points here so Claude Code and Codex follow the same rules. Keep this file current when project-wide behavior or conventions change.

Detailed wire contracts live in `docs/contracts.md`, event/reconnect semantics in `docs/events.md` when implemented, visual direction in `docs/design.md`, and the two-week scope in `docs/development-plan.md`. These documents refine this guide. GitHub issues define the scope and acceptance evidence for individual changes. If those sources conflict, resolve the conflict explicitly in the PR rather than silently inventing behavior. A direct user instruction takes precedence over repository guidance.

## Read first

Read this file, `docs/contracts.md`, `docs/architecture.md`, and the issue's Read first files. For UI work also read `docs/design.md` and inspect `docs/design/switchboard-mockup.png`. Claude Code reads `CLAUDE.md`, which points here.

## Scope and boundaries

- Build one issue per branch from main. Include the issue number in the branch and PR. Respect Build and Not in this issue. Coordinate contract changes with their owner.
- This repository preserves Project 5's apps, packages, proto, db, docker and moon layout. Its food app, vendor clients, native camera/location/sharing modules, old migrations, generated types and credentials are intentionally absent.
- The starter includes `/health`, the pure conversation state machine, event projection code, and an explicitly enabled local demo. `moon run api:demo` serves the mockup-based UI and fictional conversations on loopback. The 50 issues implement the production product; do not mark those issues done because the demo simulates their behavior.
- Thin deployables in apps; reusable code in packages/go and packages/ts. Keep SQL in packages/go/repository, provider HTTP/audio integrations in separate Go packages, transport in handlers.
- Only packages/ts/api calls our API. Never call vendors or ship secrets from the app. Never store a provider key in an EXPO_PUBLIC variable.
- The server owns call state, goal revisions, response versions, approval checks, idempotency and billing limits. Instructions are context, not literal speech. Approval binds to one response and context revision.
- Use mock provider interfaces while another issue owns their implementation. Mock and real events share the documented shape, but mock data is always visibly labelled.
- No automatic dialing in CI, tests, install scripts or the template. Real calls require explicit per-call user action, enabled server settings, and an allowed destination. Start with consenting team members as recipients.
- Pause cancels queued speech and pending generation; an end action is terminal. Never replay obsolete generated speech after reconnect, a newer instruction, or a remote interruption.
- Match the generated mockup's composition and tokens. Use accessible labels, visible keyboard focus on web, a readable transcript and reduced-motion behavior. Keep product copy free of implementation detail except the starter's honest preview labels.
- Keep the scope to reservations and ordinary scheduling. Payments, sensitive medical conversations, bulk campaigns, impersonation and autonomous commitments outside the goal are excluded.
- Any dependency must have a documented purpose. Prefer the standard library and installed packages. Check official compatibility docs before changing the inherited Expo SDK versions.

## Commands

Install proto, run `proto install`, `pnpm install --frozen-lockfile`, then use moon:

| Purpose | Command |
| --- | --- |
| Check everything | `moon run :build :lint :typecheck :test` |
| Run the complete local demo | `moon run api:demo`, then open `http://127.0.0.1:8080` |
| API on port 8080 | `moon run api:dev` |
| Expo app | `moon run mobile:dev` |
| Browser preview | `moon run mobile:web` |
| Native builds | `moon run mobile:android` or `moon run mobile:ios` |
| Format | `moon run :format` |

No Docker or provider keys are needed for the baseline. See docker/README.md when implementing persistence.

The user-requested demo lives in `packages/go/demo` and the `/demo/` transport. It uses in-memory fictional data, the Go state machine, and the TypeScript reducer. Its polling transport and scripted response rules are demonstration adapters. Production sessions, provider clients, SSE transport and persistence remain governed by their assigned issues. See `docs/demo.md` for the walkthrough and limits. Keep DEMO_MODE disabled for the normal API; the demo listener must remain loopback-only.

## Done means

Meet each acceptance checkbox with command output and meaningful behavior evidence. UI PRs include screenshots compared with the mockup, both narrow and wide. Provider changes include recorded test fixtures and mocked failure tests; real-call evidence is a separate explicit manual check. Link prerequisites and document remaining limits. Use conventional commits. Never include keys, phone numbers from real users, private transcripts, or raw audio in logs or PRs.
