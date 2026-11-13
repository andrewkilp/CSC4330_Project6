# UI verification against the reference (#47)

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
