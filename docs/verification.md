# Verification

## Combined branch verification

The PR #90 merge repair preserves the #41 mock journeys and the #47 UI evidence
below. API composition registers snapshot/SSE once, with the same hub used by
callbacks and controls, and retains the mounted durable approval route. Event
routes authenticate before reporting missing storage. Database fixtures use the
repository's object request/result contract, include the initial create event,
provide the required media secret, and simulate the terminal provider hangup.
No real provider request or call is used by these checks.

## Mock journey evidence (#41)

This page records what the automated journeys prove, how to run them, and the status of the real-call checks. Mock results and real-call results are kept separate. A passing mock journey says nothing about Twilio, Deepgram, Claude or ElevenLabs.

## End-to-end mock journeys (CI)

`packages/go/conversation/integration` runs both scenarios from `docs/fixtures` through the real pipeline components. Every provider is replaced by the network-free mocks from #6. Nothing dials, no paid request is made, and all people, businesses and numbers are fictional.

| Step | Component exercised |
| --- | --- |
| Create, then start | `telephony.StartService` (#11) with the mock telephony; acknowledged starts go into the `telephony.Calls` live registry (#31) |
| Remote speech | Mock telephony plays the business's audio; the mock transcriber (#6) produces partial and final turns |
| Suggestion | `conversation.Loop` (#19) with goal-aware prompt checks (#24) streams a reply from the scenario responder |
| Pause and resume | `control.Apply` (#31) commits `paused` and cancels the waiting suggestion; `Calls.Pause` cancels generation and clears audio; resume regenerates in the background |
| Private steering | The instruction is pending at revision N, the waiting suggestion is cancelled, the instruction is applied at N+1, and a changed suggestion follows at N+1 |
| Approval and playback | `ApproveResponse` (#29), then `SynthesizeApproved`, audio queued behind a mark, `BeginPlayback`. The mark comes back, `CompletePlayback` runs (#30), and only then is the assistant turn recorded |
| End | `control.Apply` commits `ended`; `Calls.End` stops the loop, closes the stream and hangs up the provider call |
| Summary | The scenario's scripted summary, which cites this run's finalized business turn. Summary generation itself is #39 |

The operator is scripted from each scenario's expected inputs. It approves suggestions, sends the scenario's instruction where one is expected, pauses and resumes once, and ends the call after the business's closing line.

Each run's events go to an in-memory journal in the documented envelope, so CI needs no Postgres. The transcript is strict-decoded and checked by `mock.Validate` (#5). That covers:
- contiguous sequence numbers, UTC times that never go backward, and revisions that never go backward,
- call, response and instruction transitions, including `paused` and streamed drafts,
- assistant speech only after `spoken`,
- summary evidence that is a finalized business turn,
- the scenario's operator inputs.

The test then checks the milestones:
- the business and spoken text match the scenario script,
- the instruction was applied,
- the call went draft → dialing → connected → paused → connected → ended,
- a changed suggestion followed the instruction,
- pause cancelled the waiting suggestion,
- the summary outcome matches,
- the provider call was hung up and the stream refuses audio.

Run:

```
cd packages/go/conversation/integration
go test -race -count=1 ./...                       # what CI runs (moon run integration:test)
go test -count=1 -v -run TestJourneys ./           # prints every event of both transcripts
SWITCHBOARD_JOURNEY_DIR=/tmp/journeys go test -count=1 -run TestJourneys ./   # keeps <scenario>.json
```

### Result (November 13, 2026)

`go test -race -count=1 ./...` passed. Restaurant transcript, condensed from `-v` output:

```
  1 call.updated        draft
  2 call.updated        dialing
  3 call.updated        connected
  4 turn.updated        business partial  "Bayou Lantern"
  5 turn.updated        business final    "Bayou Lantern Bistro, how can I help you?"
  6-9 response.updated  generating ... suggested "Hi, this is an AI assistant calling for Jordan Lee..."
 10 response.updated    cancelled                      (pause)
 11 call.updated        paused
 12 call.updated        connected                      (resume)
 13-16 response.updated generating ... suggested       (regenerated)
 17-19 response.updated approved, speaking, spoken     (mark acknowledged)
 20 turn.updated        assistant final
 21-23 turn.updated     business "Seven is fully booked ... I can do 6:15 or 8:45."
 24-27 response.updated generating ... suggested "Could you hold on one moment..."
 28 instruction.updated pending  "8:45 is fine. Do not accept 6:15."   rev 0
 29 response.updated    cancelled                                     rev 0
 30 instruction.updated applied                                       rev 1
 31-37 response.updated generating ... spoken "8:45 works. Please book ..."   rev 1
 38 turn.updated        assistant final
 39-49 ... confirmation with reference BLB-2048, allergy question deferred and spoken
 50 turn.updated        business final "No problem. See you Saturday."
 51 usage.updated       estimate_only, $0
 52 call.updated        ended
 53 summary.ready       confirmed, evidence = the BLB-2048 business turn
```

The appointment journey has the same shape and ends `needs_follow_up` with the callback-number question unresolved.

Related automated evidence lives with each issue:
- the provider contract suite (`packages/go/providers/providertest`, #6),
- media bridge simulations (`packages/go/telephony/media`, `apps/api/media_routes_test.go`, #21),
- control race tests (`packages/go/telephony/live_test.go`, `packages/go/handlers/control`, #31).

### Limits of the mock journeys

- Persistence is in memory. The Postgres path for the same controls is covered by `packages/go/handlers/control/integration_test.go`, which runs only when `SWITCHBOARD_REPOSITORY_TEST_DATABASE_URL` is set. CI does not set it.
- The journal is the in-process equivalent of the repository event log. Its envelopes are the same, but they are not written through `repository.Update`.
- The summary is scripted (#39 is open).
- Remote interruption mid-speech is not part of the scripted journeys. It is covered by the #26 race tests (`packages/go/telephony/interrupt_test.go`). Real STT-to-turn persistence (#25) is not exercised.
- The HTTP routes are covered by their own handler tests, not by this journey.

## Real-call checks (manual, consenting team members only)

Real calls are never run in CI or by any test. Each check needs explicit per-call action from a team member and the server settings below. The only allowed destination is the phone of a team member who has agreed to take the call. Record results here without phone numbers, transcripts, recordings or credentials.

### Preconditions

1. `ENABLE_REAL_CALLS=true`; `CALL_ALLOWED_NUMBERS` contains only the consenting team member's number.
2. Twilio settings: `TWILIO_ACCOUNT_SID`, `TWILIO_AUTH_TOKEN` and `TWILIO_FROM_NUMBER`. `PUBLIC_BASE_URL` must be a public HTTPS origin that reaches this API (see #46).
3. `MEDIA_STREAM_SECRET` (at least 32 bytes), `DATABASE_URL` and `DEEPGRAM_API_KEY`, so the media bridge mounts (#21).
4. `ANTHROPIC_API_KEY`, `CLAUDE_MODEL`, and the `ELEVENLABS_*` settings.
5. `MAX_CALL_SECONDS=180`, `MAX_CONCURRENT_CALLS=1`.

### Checklist

| # | Check | How to observe |
| --- | --- | --- |
| R1 | Start places exactly one call to the allowlisted number; a second start does not redial | Twilio console call log, API response |
| R2 | Signed voice callback returns `<Connect><Stream>`; the media stream is accepted (not closed 1008) | API logs (no payloads), Twilio debugger |
| R3 | Business speech appears as partial, then final, turns in the app | Live call screen |
| R4 | A suggestion appears; nothing is spoken before approval | Listen on the phone |
| R5 | A private instruction changes the next suggestion and is never spoken literally | App and phone |
| R6 | Approved speech plays; the assistant turn appears only after it finishes | App and phone |
| R7 | Pause stops speech within about one second and keeps the line; resume continues | Phone |
| R8 | End hangs up the phone; repeated end does nothing more | Phone, Twilio call log |
| R9 | The provider's usage matches the estimate within reason for a call of about 3 minutes | Provider dashboards against `docs/provider-matrix.md` |

### Report

| Check | Status | Date | Notes |
| --- | --- | --- | --- |
| R1 | not run | 2026-11-13 | No Twilio credentials or public HTTPS origin in this environment |
| R2 | not run | 2026-11-13 | Same |
| R3 | blocked | 2026-11-13 | Real STT updates are not yet persisted as turns or shown live (#25, #22) |
| R4 | blocked | 2026-11-13 | The real-call runtime that feeds media transcripts into the loop and sends approved audio is not wired into the API yet (mock journeys only) |
| R5 | blocked | 2026-11-13 | Also needs the instruction route (#28) |
| R6 | blocked | 2026-11-13 | Same as R4 |
| R7 | not run | 2026-11-13 | Pause is implemented (#31); needs a live call |
| R8 | not run | 2026-11-13 | End is implemented (#31); needs a live call |
| R9 | not run | 2026-11-13 | Needs R1–R8 |

Update a row to **passed** only after observing it on a real call, and add the date and who ran it.


---

## UI verification against the reference (#47)

Reference: `docs/design/switchboard-mockup.png`. Captures are in `docs/design/verification/`, taken with headless Chrome at 390 x 844 and 1440 x 900 against `moon run mobile:web`. Each capture was also checked in the page for horizontal overflow, any button or link past the viewport edge, and a React Native error overlay.

The connected call screen (`/calls/{id}?live=1`) was driven by a local stand-in for the call routes that follows `docs/contracts.md`, because the instruction (#28), control (#31) and summary (#39) routes are still open. Its states were set by the stand-in, not by local UI state.

## Route checklist

| Route | 390 px | 1440 px | Clipped controls | Notes |
| --- | --- | --- | --- | --- |
| Home (H1, `/`) | [after](design/verification/after-home-390.png) | [after](design/verification/after-home-1440.png) | none | Starter preview; history is #33. |
| Settings (S1, Settings on `/`) | [after](design/verification/after-settings-390.png) | [after](design/verification/after-settings-1440.png) | none | |
| New call (N1, `/new`) | [after](design/verification/after-new-390.png) | [after](design/verification/after-new-1440.png) | none | |
| Review (N2, `/review`), 107-character name | [after](design/verification/after-review-long-name-390.png) | [after](design/verification/after-review-long-name-1440.png) | none | Fixed an error overlay; see below. |
| Live preview (`/calls/preview`), approval / paused / ended | [approval](design/verification/after-preview-approval-390.png), [paused](design/verification/after-preview-paused-390.png), [ended](design/verification/after-preview-ended-390.png) | [approval](design/verification/after-preview-approval-1440.png), [paused](design/verification/after-preview-paused-1440.png), [ended](design/verification/after-preview-ended-1440.png) | none | #12's fixture preview, unchanged. |
| Live call, connecting | [before](design/verification/before-live-connecting-390.png) / [after](design/verification/after-live-connecting-390.png) | [before](design/verification/before-live-connecting-1440.png) / [after](design/verification/after-live-connecting-1440.png) | none | |
| Live call, awaiting approval | [before](design/verification/before-live-awaiting-approval-390.png) / [after](design/verification/after-live-awaiting-approval-390.png) | [before](design/verification/before-live-awaiting-approval-1440.png) / [after](design/verification/after-live-awaiting-approval-1440.png) | none | |
| Live call, paused | [before](design/verification/before-live-paused-390.png) / [after](design/verification/after-live-paused-390.png) | [before](design/verification/before-live-paused-1440.png) / [after](design/verification/after-live-paused-1440.png) | none | |
| Live call, ended | [before](design/verification/before-live-ended-390.png) / [after](design/verification/after-live-ended-390.png) | [before](design/verification/before-live-ended-1440.png) / [after](design/verification/after-live-ended-1440.png) | none | |
| Live call, 107-character name | [before](design/verification/before-live-long-name-390.png) / [after](design/verification/after-live-long-name-390.png) | [before](design/verification/before-live-long-name-1440.png) / [after](design/verification/after-live-long-name-1440.png) | none | |
| Summary (H2, `/calls/[id]/summary`) | not built | not built | n/a | Owned by #39, still open. |

## Compared with the mockup

| Element | Reference | Connected call screen now |
| --- | --- | --- |
| Hierarchy | Serif "Reservation at Marigold", spaced "RESTAURANT RESERVATION · MARIGOLD", status at the right | Same. Before, the title was the bare business name and the status read "MOCK · CONNECTED". |
| Status | LIVE pill | LIVE, CONNECTING, PAUSED, ENDED, FAILED or CANCELLED, with a separate MOCK CALL badge for mock calls. |
| Speakers | Filled AI (green) and RESTAURANT (vermilion) tags, monospaced `[mm:ss]` | Same; appointments show BUSINESS. The #12 preview keeps BUSINESS. Each turn is still read as one item (#42). |
| Goal | "Table for 4, Friday around 7 pm" | "Table for 4", "Fri, Nov 13, around 7:00 PM" in the business's own clock, the time zone, the booking name, "Calling Marigold at +1 312 555 0142" and constraints. Before, the goal showed the raw `2026-11-13T19:00:00-06:00` timestamp. |
| Transcript and rail | Large transcript, narrower goal and reply rail | Same at 1000 px and up; stacked on phones. |
| Bottom bar | "Guide the next response", field, Send, Pause AI, End call in one band | Same band on wide screens (#42 dock); on phones the composer sits above Pause and End, as in the phone mockup. |
| Live timer | `02:14` beside the status | Not shown: usage events (#35) are not rendered yet on this screen. |
| Icons | Phone, calendar, chat and pause glyphs | Not added: no icon set exists and #47 rules out new design-system pieces. |

## Fixes made in this change

- The title, subtitle and status follow the reference instead of the bare name and "MOCK · CONNECTED".
- The goal reads as a sentence in the business's clock instead of a raw timestamp, and names the business and number being called.
- Transcript speakers use the reference's filled tags.
- Long business names: a 107-character name used to fill a phone's first screen. The title now stops at three lines (two on wide screens) and the subtitle at two; the full name stays the accessible label and appears untruncated in the goal panel.
- Review (`/review`) showed React Native's "Unexpected text node" error overlay on every visit: `{createError && …}` rendered the empty string inside a View. That and the other string-guarded renders in `goal-entry.tsx` and `RuledPanel` now use a ternary.

## Keyboard layout

Pause and End stay docked above the keyboard, and on short screens or with large text the composer moves into the page (#42, `docs/accessibility.md`). Headless captures cannot raise a software keyboard; that check needs a device or simulator.
