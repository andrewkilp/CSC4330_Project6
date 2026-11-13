# Contributing and rehearsing the offline demo

Work from a GitHub issue. Read `AGENTS.md`, `docs/contracts.md`,
`docs/architecture.md` and the issue's Read first files before editing. These
instructions apply equally to Codex and Claude Code. Start a branch from current
`main`, include the issue number in its name and PR, and keep one issue per PR.
Use an isolated worktree if another checkout has ongoing work. Review prerequisite
issues and use their documented mock boundaries while implementations are pending.

## Before a presentation

Install the pinned toolchain with `proto install` and dependencies with
`pnpm install --frozen-lockfile`. From the repository root on Windows, macOS or
Linux, run:

```sh
node scripts/verify-fixtures/run.cjs
```

This validates both committed scenario JSON files with the existing strict
`mock.Decode` and `mock.Validate`, compares their exact bytes with the seed-4330
generator, and feeds each scenario through the actual conversation loop to its
first suggested reply. It checks that the reply matches the script and that no
response advances beyond the operator approval hold. Errors identify the fixture
and, for semantic failures, the event sequence and field. Missing, extra, malformed
and stale JSON fail the command. Tests deliberately corrupt inputs to verify this.

Expect one `PASS` line for each of `appointment-missing-detail` and
`restaurant-time-conflict`, followed by `Offline mock verification passed`.
The runner exits nonzero at the first failed command. It never rewrites fixtures.
To regenerate after an intentional script change:

```sh
go run ./packages/go/mock/cmd/scenarios
```

The repository's `.gitattributes` keeps text files at LF on Windows too: existing
byte-for-byte fixture tests and formatters require it. An older checkout may still
have CRLF files until refreshed; use a fresh worktree for verification if needed.
Don't regenerate unchanged scripts merely to hide a checkout line-ending problem.

To print actual runtime response events during a backend demonstration:

```sh
go run ./apps/api --mock-scenario restaurant-time-conflict
go run ./apps/api --mock-scenario appointment-missing-detail
```

Both commands exit at the first suggestion awaiting approval. All data is
fictional and generation is scripted. Explain that the later approvals, synthetic
audio and summaries in the fixture describe the planned complete journey; this
CLI does not execute those fixture events. The web design preview remains a
separate demonstration until its API/event integration issues are implemented.
These checks establish no real-call, paid-model, audible-speech or booking result.

## CI and local checks

```sh
node scripts/verify-fixtures/run.cjs --ci
```

This adds the inherited `moon run :build :lint :typecheck :test` after the offline
checks. The workflow uses the same entry point after installing dependencies.
Every child runs with `CALL_MODE=mock`, `ENABLE_REAL_CALLS=false`, an empty call
allowlist and callback URL, and no inherited provider keys or token/secret/password
variables. Expo dotenv loading is disabled so a developer's local `.env` cannot
override the check environment. Tests cover a hostile inherited real-call shell,
including Windows mixed-case environment names, and reject arbitrary runner flags.
No provider secret is required in CI; do not add one to the workflow.

This is an offline mock execution policy, not an operating-system network sandbox.
Dependency installation/toolchain setup needs network access, and adapter unit
tests may use local HTTP test servers. Review changes to the workflow, task
commands and provider construction to preserve the policy. Don't add a live call
or paid-provider smoke test to install/build/test commands. Real-call checks remain
separate manual actions with the explicit call gate from `AGENTS.md`.

## PR evidence

Describe the concrete behavior, linked issue and prerequisites, exact verification
commands and outcomes, plus remaining integration limits. Use `Refs #10` for
partial work and reserve `Closes #10` for completed acceptance criteria. Include
narrow/wide screenshots compared with the reference for UI changes; use recorded
fixtures and failure tests for providers and label real-call evidence separately.
Never include credentials, private transcripts, real-user phone numbers or raw
audio. A passing mock check does not complete a real-provider integration issue.
