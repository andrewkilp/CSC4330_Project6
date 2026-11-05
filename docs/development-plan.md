# Two-week development plan

50 scoped issues, five people, five stages. Every person owns two tasks in each stage, ten in total. Days are relative to kickoff; no deadline was assumed. Each stage has ten tasks and a shared exit goal. Dependencies may finish within the same stage: use the mock boundaries to start independent work, then integrate in dependency order. No cycle may be introduced.

The template is an existing starting point. These tasks add product behavior and hardening; they do not ask people to recreate the baseline. AI agents work from the GitHub issue, shared contracts and generated design. A human reviews and merges each scoped PR.

Arthur2457 has been invited to the repository. His tasks name him as owner and carry an owner label even if GitHub will not assign them until he accepts. The publication report records actual assignment state.

## Stage 1: Foundation (Days 1–2)

Exit: Contracts, provider choice, UI primitives, storage schema, lifecycle and mocks are ready.

| Task | Owner | Work | Needs |
| --- | --- | --- | --- |
| T01 | @bengutow1 | Freeze call contracts and provider decision | none |
| T02 | @cadenlund | Extract the mockup-based theme and UI primitives | none |
| T03 | @IvanR586 | Design call, instruction and event storage | none |
| T04 | @andrewkilp | Implement the pure call and response state machine | none |
| T05 | @Arthur2457 | Build deterministic restaurant and appointment scenarios | none |
| T06 | @bengutow1 | Define cancellable provider interfaces and mock adapters | T01, T05 |
| T07 | @cadenlund | Create goal entry and review routes (N1, N2) | T01, T02 |
| T08 | @IvanR586 | Implement minimal sessions and owner middleware | T01, T03 |
| T09 | @andrewkilp | Define event reducer and reconnect rules | T01, T04, T05 |
| T10 | @Arthur2457 | Strengthen baseline CI and repeatable mock verification | T02, T05 |

## Stage 2: Vertical slices (Days 3–5)

Exit: Draft creation, provider adapters, sessions and the live layout work independently against contracts.

| Task | Owner | Work | Needs |
| --- | --- | --- | --- |
| T11 | @bengutow1 | Implement outbound Twilio start behind the call gate | T06, T08, T18 |
| T12 | @cadenlund | Build the live call route from the generated mockup (L1) | T02, T07, T09 |
| T13 | @IvanR586 | Implement owner-scoped call repositories | T03, T08 |
| T14 | @andrewkilp | Implement the streaming Claude response adapter | T01, T06 |
| T15 | @Arthur2457 | Implement ElevenLabs streaming speech synthesis | T01, T06 |
| T16 | @bengutow1 | Implement signed Twilio callbacks and status mapping | T06, T11 |
| T17 | @cadenlund | Wire transcript rendering to the shared event reducer | T09, T12 |
| T18 | @IvanR586 | Expose call draft/create/read and the typed client | T07, T08, T13 |
| T19 | @andrewkilp | Compose the mock conversation loop | T04, T06, T14 |
| T20 | @Arthur2457 | Select and implement a streaming STT adapter | T01, T06 |

## Stage 3: Live conversation (Days 6–8)

Exit: The full mock call supports transcript streaming, mid-call instructions, approval and speech playback.

| Task | Owner | Work | Needs |
| --- | --- | --- | --- |
| T21 | @bengutow1 | Build the authenticated Twilio bidirectional media bridge | T11, T16, T20 |
| T22 | @cadenlund | Subscribe the app to the authenticated event stream | T09, T17, T23 |
| T23 | @IvanR586 | Implement durable SSE fanout and replay | T09, T13, T18 |
| T24 | @andrewkilp | Build the goal-aware prompt and response validation | T04, T14, T19 |
| T25 | @Arthur2457 | Bridge real STT updates into durable transcript turns | T13, T20, T21, T23 |
| T26 | @bengutow1 | Implement remote interruption and audio-buffer clearing | T04, T21, T25, T30 |
| T27 | @cadenlund | Wire the private mid-call instruction composer | T12, T22, T28 |
| T28 | @IvanR586 | Apply operator instructions with atomic context revisions | T04, T13, T19, T23 |
| T29 | @andrewkilp | Enforce response-specific approval and edited replies | T04, T18, T24, T28 |
| T30 | @Arthur2457 | Implement approved speech playback and acknowledgement | T15, T19, T21, T29 |

## Stage 4: Complete workflows (Days 9–11)

Exit: Both use cases have controls, history, summaries, failure behavior and cost/retention limits.

| Task | Owner | Work | Needs |
| --- | --- | --- | --- |
| T31 | @bengutow1 | Implement pause, resume and terminal end controls | T04, T11, T21, T28, T30 |
| T32 | @cadenlund | Build suggested reply editing and approval UI | T12, T22, T27, T29 |
| T33 | @IvanR586 | Implement paged call history and detail navigation (H1) | T13, T18, T23 |
| T34 | @andrewkilp | Handle reservations and appointment clarification end to end | T07, T24, T25, T28, T29 |
| T35 | @Arthur2457 | Enforce duration, concurrency and usage limits | T06, T11, T15, T20, T30 |
| T36 | @bengutow1 | Harden webhook delivery and provider failure recovery | T16, T21, T26, T31 |
| T37 | @cadenlund | Wire live pause/end controls and recovery states | T22, T31, T32 |
| T38 | @IvanR586 | Implement transcript retention and deletion | T13, T31, T33 |
| T39 | @andrewkilp | Generate evidence-backed post-call summaries (H2) | T24, T25, T31, T34 |
| T40 | @Arthur2457 | Expose provider readiness and explicit mock fallback | T06, T14, T15, T20, T35 |

## Stage 5: Hardening and delivery (Days 12–14)

Exit: Tests, accessibility, restart handling, release evidence and a reproducible demo are complete.

| Task | Owner | Work | Needs |
| --- | --- | --- | --- |
| T41 | @bengutow1 | Run full restaurant and appointment integration journeys | T25, T26, T30, T31, T34 |
| T42 | @cadenlund | Complete keyboard, screen reader and mobile usability | T17, T27, T32, T37 |
| T43 | @IvanR586 | Close session, CSRF, owner and rate-limit gaps | T08, T18, T23, T28, T29, T31, T38 |
| T44 | @andrewkilp | Verify conversation races and stale generation cancellation | T26, T28, T29, T30, T31 |
| T45 | @Arthur2457 | Measure latency and tune bounded pipeline performance | T14, T15, T20, T25, T30, T35 |
| T46 | @bengutow1 | Package a single-instance demo deployment | T35, T36, T38, T40, T43 |
| T47 | @cadenlund | Validate the final UI against the generated reference | T33, T37, T39, T40, T42 |
| T48 | @IvanR586 | Verify persistence, restart recovery and deletion | T13, T23, T33, T38, T43 |
| T49 | @andrewkilp | Finish failure messaging and release acceptance | T34, T36, T39, T41, T44 |
| T50 | @Arthur2457 | Prepare the two-week handoff and recorded demo | T41, T45, T46, T47, T48, T49 |

## Per-person balance

| Person | Stage 1 | Stage 2 | Stage 3 | Stage 4 | Stage 5 | Total |
| --- | --- | --- | --- | --- | --- | --- |
| @bengutow1 | T01, T06 | T11, T16 | T21, T26 | T31, T36 | T41, T46 | 10 |
| @cadenlund | T02, T07 | T12, T17 | T22, T27 | T32, T37 | T42, T47 | 10 |
| @IvanR586 | T03, T08 | T13, T18 | T23, T28 | T33, T38 | T43, T48 | 10 |
| @andrewkilp | T04, T09 | T14, T19 | T24, T29 | T34, T39 | T44, T49 | 10 |
| @Arthur2457 | T05, T10 | T15, T20 | T25, T30 | T35, T40 | T45, T50 | 10 |

## Working in parallel

Start from main, name a branch after its issue, and keep changes within the listed paths. Shared contract/state owners review cross-boundary edits. Do not run separate agents in one checkout for unrelated issues. Use isolated branches/worktrees and merge green PRs. Dependency issues own the interfaces; consumer issues use the mocks until integration is available.

Stage 2 orders T13 before T18 before T11 before T16; Stage 3 orders T23 before T22, T28 before T29 before T30 before T26. These sequences are deliberate within a stage; other tasks can proceed from fixtures. T50 is the final joint handoff.

If a paid-provider capability is unavailable, preserve the fully working mock demo and record the real-call path as blocked rather than substituting fake success. Scope excludes calendar integrations, transfers, payments and bulk dialing.
