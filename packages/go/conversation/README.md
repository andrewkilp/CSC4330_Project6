# Pure conversation state (#4)

This module performs no network, audio, database or clock work and has no external dependencies. `New()` (or the zero value) is a draft call. All commands return a new `State` and leave their input unchanged, including on failure. Callers must retain the returned value.

Call lifecycle: draft -> dialing -> connected -> ended/failed/cancelled. A draft or dialing call can also become terminal. Connected has a separate paused flag; it is never encoded as a new call status. Terminal states cannot reopen. An identical status callback is idempotent; earlier or otherwise invalid callbacks return `ErrInvalidTransition`.

Responses go generating -> suggested -> approved -> speaking -> spoken. An active response can fail; pause, instruction, interruption and terminal end cancel any active response. Spoken/failed/cancelled response records remain available as history in the snapshot, but cannot advance. Beginning the next response allocates a new generation token. Response IDs are generated/validated by the future API boundary.

`ApplyInstruction(expectedRevision)` advances the context revision and invalidates current work. It accepts an instruction while paused without resuming. The repository/action handler owns idempotency keys; this pure function only enforces the expected revision. `Interrupt()` fences generation and queued speech without changing the goal revision. Pause fences work too; resume never revives it.

Every asynchronous completion must present its response ID and the `Token` returned by `BeginResponse`. The token carries context revision and generation. Check the call identity as well; tokens are only scoped to one machine. Never use a token as authentication. Only an approved response can start speaking, and only a speaking response can become spoken. A late approval, generation callback or playback acknowledgement cannot advance an obsolete response.

This is the decision layer, not a cancellation executor. The orchestrator must serialize access to each call and, on invalidation, cancel contexts and clear provider audio buffers. This module does not promise thread safety for a shared caller variable or stop a provider itself. Persistent state restoration, text validation, approval policy and request idempotency belong to later issues.

Run `moon run conversation:build conversation:lint conversation:test`. The table tests exercise lifecycle transitions, pause/resume, dialing cancellation, stale revisions, response approval and late callbacks.
