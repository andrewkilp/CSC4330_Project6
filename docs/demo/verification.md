# Local demo verification

Verified November 5, 2026.

- `moon run :build :lint :typecheck :test`: passed, 19 tasks. Includes pure conversation tests, 13 reducer tests, demo scenario tests and HTTP guard/input tests.
- Isolated browser checks at 1440 x 1100 and 390 x 844: passed restaurant steering to indoor seating, pause/resume, approval, simulated confirmation, then appointment steering to a trim and confirmation.
- No browser page errors or horizontal overflow at the tested narrow width.
- Screenshot review caught and corrected desktop wordmark wrapping and a cramped mobile title. The mobile composer and controls remain reachable by scrolling.
- Real phone calls, Claude generation, ElevenLabs speech, microphone input and external bookings: not used or tested. These remain future production integrations.

Screenshots: [welcome](welcome-desktop.png), [desktop steering](steering-desktop.png), [outcome](outcome-desktop.png), [mobile heading](steering-mobile.png), [mobile composer](composer-mobile.png).
