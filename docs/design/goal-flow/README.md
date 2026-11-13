# Goal entry and review (#7)

The New call action now opens `/new` and a valid goal opens `/review`. Both
screens use #2's shared components and preserve its paper, forest, serif and
thin-rule direction. The 1440px layout uses the reference's slim side rail and
two ruled columns; at 390px the content stacks without horizontal overflow.

Draft details live in the root React context only, so moving between entry,
review and Calls preserves the form. A reload deliberately clears them. No
private values are put in the URL, browser storage, logs or an API request.
Direct access to `/review` without a valid draft offers entry instead of
inventing details. Editing explicitly returns to `/new`, independent of history.

Restaurant and appointment details remain available when switching kinds, but
only the active kind's party size or service enters the reviewed contract.
Destination is restricted to fictional `+1NXX55501XX` numbers. The business's
named time zone is explicit; valid local time is converted to the documented
offset-bearing timestamp. Skipped local times fail validation and repeated DST
hours require an explicit offset selection. Neither phone nor timezone gaps are
silently filled. Optional callback details remain absent when not provided.

Mock mode and approval-always are fixed for this step. The acknowledgement
describes a fictional simulation, and review clearly says nothing is saved or
dialed. #18 owns creating a call draft; #11 owns the later explicit start action.
The review screen supplies no substitute create/dial action.

## Verification

```sh
moon run mobile:test mobile:lint mobile:typecheck mobile:build
node scripts/verify-fixtures/run.cjs --ci
```

The three model tests cover both contract projections, inactive-kind fields,
UTF-16 limits, fictional numbers, missing information, invalid zones/dates,
nonexistent and repeated local times, and quarter-hour zone offsets. Moon uses
the pinned Node 24 toolchain for direct TypeScript test imports.

Browser evidence in this directory includes entry/review screenshots at 390 and
1440px and `goal-entry-review-flow.webm`. The recording shows both goal types,
inline validation, review/edit with retained input, ambiguous-time selection,
and rejection of a non-fictional destination and invalid zone. Additional checks
confirm Space operates the native web checkbox, error summary receives focus,
fields have visible focus/labels and 48px targets, and no horizontal overflow,
page exceptions or API mutations occur. The original mockup was inspected before
implementation and screenshots reviewed against its hierarchy and palette.

All names and numbers shown in these artifacts are fictional. No real call,
provider, persistence or mobile-device interaction is claimed by this evidence.
