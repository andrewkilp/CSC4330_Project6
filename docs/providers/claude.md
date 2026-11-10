# Claude response adapter (#14)

`packages/go/claude` uses the server-only Anthropic Messages HTTP API. No SDK or subscription credentials are used. Construct `New(Config{APIKey, Model, MaxTokens})` from server configuration; the model must be enabled for the API account. MaxTokens is required, 1..1024. The app never receives credentials. This package does not initiate calls or automatically generate responses.

`Generate` takes structured goal JSON, finalized transcript turns, ordered private instructions and the current context revision. Instructions have a separate JSON field from untrusted business transcript text. The prompt is transport-level guidance; goal-aware validation belongs to #24. Callers remain responsible for only supplying finalized turns and applied instructions.

Draft callbacks contain cumulative text and the input revision. They are synchronous and must return promptly. Any error invalidates all drafts. Only a nonempty response ending with `end_turn` and `message_stop` emits Final. Token exhaustion, incomplete or malformed streams, output over 1500 characters and stream errors never finalize. The caller must still enforce revision checks and approval before speech. Cancel the context on new instructions, interruption, pause or end. Context cancellation interrupts HTTP reads; callbacks must cooperate and never block indefinitely.

Requests have a 30-second timeout, bounded input, bounded SSE frames and a 1 MiB stream limit. Redirects are disabled to avoid forwarding keys. HTTP 429 maps to ErrRateLimited; other HTTP/network/provider failures have fixed user-safe errors. Vendor bodies and transport errors are never returned or logged. There is no automatic retry of partial output.

The #1 contract freeze and #6 shared Responder interface are still open. Input/Update are an isolated local boundary using the documented capabilities, not a replacement shared interface. #6 integration must wrap/adapt this client; it is not wired into production or the demo.

Protocol checked November 10, 2026 against [Messages streaming](https://platform.claude.com/docs/en/build-with-claude/streaming). `testdata/*.sse` are sanitized, synthetic recordings of the documented HTTP stream format, with fictional text, not claims of a paid live-provider run. Tests cover complete response, partial provider failure, HTTP rate limit, truncation, malformed data, token exhaustion, output bounds, callback cancellation and cancellation while reading. No real-call or paid-provider check was performed.

Verification: `moon run :build :lint :typecheck :test`. Targeted: from this directory's Go package, `go test ./...` and `go vet ./...`. No UI changes.

Verified November 10, 2026: full command exited 0, `Tasks: 19 completed (10 cached)`, `Time: 22s 363ms`. `claude:build`, `claude:lint` and `claude:test` passed without cache; Go reported `ok github.com/andrewkilp/CSC4330_Project6/packages/go/claude 1.095s`. Existing checkout CRLF endings were locally normalized for Biome; no unrelated content changes are included.
