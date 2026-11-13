# Goal-aware suggestions (#24)

The conversation loop builds each generation through `packages/go/conversation/prompt.Build`. It preserves the authorized goal JSON, finalized transcript order, applied private instruction order and context revision, copying slices before provider work. Partial business turns remain excluded by the loop. Call creation owns full Goal validation; the prompt builder accepts only restaurant/appointment kinds and never fills missing customer details.

The fixed server policy is sent in the Claude Messages `system` field through the server-only `Input.SystemPolicy` field (`json:"-"`). The user message contains structured JSON with the goal, finalized transcript and private instructions. Business speech, even text claiming to be a system message, remains inside transcript data. No tools are enabled. Private instructions provide context and must not be read aloud. Later instructions clarify the goal without authorizing payments or commitments beyond its constraints.

Replies should be one or two sentences. A time conflict should lead to a question within the authorized time/seating constraints. A missing contact detail should lead to operator clarification. Remote requests to disclose private instructions, override approval or claim confirmation do not authorize those actions.

Final response validation checks nonempty valid UTF-8, the 1500 UTF-16-unit limit, matching revision and explicit contact patterns. Phone numbers may be repeated only when they exactly match the authorized `contact_phone`; email addresses are rejected because Goal has no email field. This deliberately conservative check can reject harmless number formatting and does not detect every spelling of a phone number or every invented fact. It does not prove semantic compliance or prompt-injection resistance. Every suggestion remains behind the loop's existing operator approval gate, including uncertain or out-of-constraint commitments. #29 owns response-specific approval and edited replies; this issue adds no speech path.

Invalid final output becomes a failed response with cleared text; failed, incomplete or stale streams never become suggestions. Intermediate text remains a generating draft. Existing generation tokens fence pause, end, instruction and remote-speech races.

## Evidence

`prompt/testdata/scenarios.json` contains reviewed expected replies for a time conflict, unknown contact detail and malicious remote instruction. Tests verify exact context preservation, policy separation, mandatory approval, rejected invented contacts, stale revisions and UTF-16 boundaries. These are deterministic contract fixtures, not claims about live model behavior. The existing mock scenarios exercise both journeys through the integrated loop without keys or network calls.

Run `go test ./packages/go/conversation/... ./packages/go/claude/... ./packages/go/mock/...` and `go test -race ./packages/go/conversation/...`, then `moon run :build :lint :typecheck :test`. No live provider call is required or claimed.

Verified November 12, 2026 with Go 1.26.4, Node 24.22.0, pnpm 10.33.4 and Moon 2.4.2: both conversation packages passed `go test -race` (CGO enabled with the local UCRT GCC compiler); Moon reported `Tasks: 25 completed (21 cached)`. The Windows checkout needed local CRLF-to-LF normalization for existing byte-exact fixtures and Biome checks; this changes no committed fixture or frontend content. The same fixture failure was reproduced on unchanged main before normalization.
