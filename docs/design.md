# Switchboard design

![Generated desktop and mobile direction](design/switchboard-mockup.png)

Generated with the built-in imagegen tool on November 5, 2026. This image is a visual reference, not a claim of working calls. The starter implements its layout as Expo/React Native code. It labels all simulated behavior.

## Direction

An editorial telephone concierge: warm paper, ink, forest green, a restrained vermilion for the remote speaker and ending a call, brass for secondary status. Thin rules and a strong type hierarchy replace a dashboard grid. Serif headings, familiar body type, small monospaced timestamps. No purple gradients or decorative AI orbs.

Tokens live only in apps/mobile/src/theme.ts. Paper #F5F0E6, ink #202923, forest #234D3C, vermilion #C65036, brass #94733A. Fonts use platform serif/system/mono fallbacks, so the template needs no downloaded fonts. Buttons have at least a 48 px target.

## Shared primitives (#2)

Import from `apps/mobile/src/components`. These components contain no API calls
or call state. Existing preview consumers can continue using the flat color
tokens; shared additions are `font`, `type`, `space`, `layout`, `radius`, and
`target`. `layout.narrow` uses 20 px page padding/24 px section gaps;
`layout.wide` uses 40 px/32 px at the 1000 px breakpoint. The original vermilion
remains the speaker/action accent; `dangerInk` (#9A3D2A) is darker for readable
error text on paper (6.01:1 contrast). Standard ink/muted copy and white labels
on forest/vermilion preserve the reference palette.

| Export | Public props and behavior |
| --- | --- |
| `AppText` | Native `TextProps`, `variant`: display/heading/body/label/caption/mono; `tone`: ink/muted/green/danger. Serif headings, system body and mono timestamps. |
| `Button` | Native `PressableProps`, `label`, primary/secondary/destructive `variant`, `loading`, `loadingLabel`, `style`. At least 48 by 48 px; loading disables activation and announces busy state. |
| `Badge` | `label`, neutral/success/warning/danger `tone`. Text always identifies the state; badges are not interactive controls. |
| `RuledPanel` | Native `ViewProps`, optional `title`/`accessory`, and children. Thin top/bottom rules and a wrapping heading row. |
| `Field` | Native `TextInputProps` with `label`, `hint`, `error`, `required`, `containerStyle`, `inputStyle`. Labels remain visible; error/hint IDs link to the input; required/invalid state is exposed on web. |

Buttons and fields have visible keyboard outlines on web. Disabled and loading
states retain readable text instead of relying on reduced opacity. No component
animates, so reduced-motion use receives the same stable layout. Native labels,
hints and state metadata accompany the web ARIA attributes; native screen-reader
testing remains part of #42.

### Gallery and evidence

Open `/components` with `moon run mobile:web`. This local gallery demonstrates
the primitives in the reference's serif heading, ruled transcript and narrow goal
rail composition, stacking at mobile widths. Its actions only show local feedback.
No suggestion becomes a spoken transcript turn. The existing call-preview screen
is unchanged.

- [1440 px gallery](design/gallery-1440.png)
- [390 px initial viewport](design/gallery-390.png)
- [390 px complete gallery](design/gallery-full-390.png)
- [390 px focus/interaction state](design/gallery-focus-390.png)

Compared with `switchboard-mockup.png`, these captures preserve the paper/forest
palette, serif hierarchy, monospaced timestamps, named speaker badges, thin rules
and desktop rail/mobile stacking. The gallery includes additional primitive-state
examples rather than claiming the reference's call controls are connected.

Local browser checks at 390 and 1440 px verified exact accessible textbox names,
all control dimensions at least 48 px, no horizontal overflow, linked required/error
descriptions, empty-submit validation and clearing, loading/disabled announcements,
and visible focus in a reduced-motion browser context. No page errors occurred.
The browser plugin's execution tool was unavailable, so the screenshots and checks
used the existing local Playwright/Chrome fallback, without changing dependencies.

## Screens and routes

The template has one route with local preview navigation. Issue implementations introduce these routes:

| Code | Route | Purpose |
| --- | --- | --- |
| H1 | / | Recent calls, active call and New call |
| N1 | /new | Restaurant or appointment goal, destination, constraints |
| N2 | /review | Review recipient, goal, call mode and disclosure before dialing |
| L1 | /calls/[id] | Live transcript, current goal, suggested reply and steering |
| H2 | /calls/[id]/summary | Outcome, confirmed details and transcript |
| S1 | /settings | Mode, provider availability and retention, never raw keys |

L1 is the primary view. At wide widths use a slim sidebar, a large transcript, and a narrower goal/reply rail. At narrow widths stack these with a reachable instruction composer, keeping transcript reading position stable. AI and business turns have speaker names and timestamps, never color alone. Show provisional text separately from finalized turns.

The operator can type a private instruction, inspect a revised suggestion, edit it, and approve it. Suggested text must not appear in the transcript as spoken until playback is confirmed. Mid-call unknowns need a clear approval state. Display instruction pending/applied/stale/failed states. Pause AI and End call stay reachable.

## Live layout preview (#12)

`/calls/preview` renders typed fictional call/turn/response fixtures using shared
primitives. Wide screens use navigation, transcript and goal/reply rail; narrow
screens stack in reading order. Preview controls show connecting, awaiting
approval, paused and ended states. All actions are local and labelled; approving
a suggestion does not add it to the transcript. End disables approval, instruction
and pause controls. The state selector opens a fresh visual example. Existing
`/calls/{uuid}` draft reads retain their owner-gated behavior. No event subscription
or vendor request is added.

## Source prompt

Use case: ui-mockup. A mobile-first AI phone concierge named Switchboard for restaurant reservations and appointments. Show desktop and narrow mobile live-call workspaces. Editorial telephone exchange aesthetic: ivory paper, dark ink, forest green, restrained vermilion and brass; thin ruled borders; serif headings, humanist body, monospaced timestamps. Reservation at Marigold, transcript with AI and Restaurant labels, current goal table for four Friday around seven, editable suggested reply, Approve, instruction composer with “Accept 7:30, but ask for the patio,” Pause AI and End call. Avoid generic card grids, purple gradients and glowing AI orbs. Flat crisp interface reference.
