# Accessibility and mobile usability (#42)

Scope: the connected call screen (`/calls/[id]?live=1`), its transcript, the reply, instruction and control panels from #27, #32 and #37, and the shared primitives in `apps/mobile/src/components`. The goal entry and review routes from #7 were checked in `docs/design.md`.

## What the screen does

| Area | Behavior |
| --- | --- |
| Labels | Every control has a visible text label that is also its accessible name: Approve reply, Approve my edit, Undo edit, Send instruction, Send with newest context, Try again, Pause AI, Resume AI, End call, End call now, Keep calling, Reconnect call updates, Jump to latest. Fields keep visible labels ("Reply to approve", "Private instruction"). Each transcript turn is one element, read as "AI, at 1 minute 3 seconds: words" or "Business, still being transcribed, at …". |
| Status without color | The call state is a text badge (MOCK · CONNECTED, PAUSED, ENDED), the stream state is a sentence, speakers are named (AI, BUSINESS), provisional turns say PROVISIONAL, reply states are text badges (DRAFTING, NEEDS YOUR APPROVAL, APPROVED, WAITING TO PLAY, SPEAKING NOW) and instructions say PENDING, APPLIED or FAILED. Color only repeats the text. |
| Announcements | `apps/mobile/src/screens/live-call/announce.ts` decides what screen readers hear through `AccessibilityInfo.announceForAccessibility`: call status changes, a reply that needs approval, each finished business turn ("Marigold said: …") and stream problems. Partial speech is never announced, so live updates do not flood the reader. Local errors next to a control use live regions. |
| Focus order | Calls, the transcript box, the reply editor and approval, Reconnect, then the docked instruction field, Pause and End. Transcript rows are not tab stops; the transcript box is focusable for arrow-key scrolling. Buttons and fields show a 2 px ink outline on keyboard focus (web). |
| Keyboard on phones | The composer and Pause/End sit in a dock below the scrolling workspace inside a `KeyboardAvoidingView`, so they stay above the on-screen keyboard. The docked composer scrolls within 28% of the screen height and Pause/End stay last, always on screen. The instruction history moved to the rail. |
| Large text | From a 1.3 text scale, transcript timestamps move above the words. From 1.5, or on screens shorter than 640 pt, only Pause and End stay docked and the composer moves into the page, so the transcript is never squeezed out. Text scaling is never capped. |
| Reduced motion | Nothing on the screen animates; following the transcript and "Jump to latest" scroll without animation. |
| Targets | All buttons and links are at least 48 by 48 px. |

## Contrast

Measured with the WCAG relative-luminance formula against paper (#F5F0E6): ink 13.18:1, muted 4.65:1, green 8.41:1, dangerInk 6.01:1, white on green 9.55:1, white on vermilion 4.56:1. Brass is 3.87:1, which fails for text, so it is used only for borders and rules. Fixed here: the home screen's "DESIGN PREVIEW" tag was brass text and is now muted ink. Badges now wrap inside narrow screens instead of running off the edge.

## Checks recorded

Run with `moon run mobile:web` against a local stand-in for the call routes (the instruction and control routes, #28 and #31, are still open).

- **Keyboard only, web, 390 x 844.** Tab order: Calls, Call transcript, Reply to approve, Approve reply, Reconnect call updates, Private instruction, Pause AI, End call (Send instruction joins once text is typed). No target under 44 px, no horizontal overflow, Pause AI and End call on screen without scrolling.
- **Reflow, 320 x 568.** No horizontal overflow; the composer moved into the page and Pause AI and End call stayed docked on screen.
- **Announcement rules.** `apps/mobile/test/announce.test.mjs` replays contract events and asserts exactly what is announced: connected, the finished business turn, the approval prompt and the pause, and nothing for the partial turn or repeated state.
- **Reduced motion.** No `Animated`, `LayoutAnimation` or animated scroll on these screens (checked with grep).

## Not yet checked

- **Native screen readers.** VoiceOver (iOS) and TalkBack (Android) have not been run. To check: enable VoiceOver, open a live call, swipe through the page and confirm each turn reads as one item with speaker and time, status changes and "… said: …" are announced as turns finish, partial speech is not announced, and Pause/End stay reachable with the keyboard open. Record the result here.
- **Native large text.** The 1.3 and 1.5 thresholds were exercised in the browser only by viewport size, because the web build always reports a font scale of 1. Check at the largest iOS Dynamic Type and Android font size.
