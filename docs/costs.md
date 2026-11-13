# Call limits and usage estimates (#35)

`packages/go/policy` enforces the real-call limits from `docs/contracts.md` and
produces the `usage.updated` payload. It has no provider SDKs, network code or
credentials and never dials. The call lifecycle composes it; until the start route
(#11) and event stream (#23) exist, nothing in the running API uses it.

**Every figure here is an estimate.** `usage.updated` always carries
`estimate_only: true`. Provider dashboards are the billing authority. Free credits,
plan allowances, taxes, rounding rules we have not modelled and rate changes after the
checked date all make the bill differ. Nothing in this package upgrades an account or
promises a ceiling on what a provider charges.

## Limits

| Setting | Default | Allowed | Enforcement |
| --- | --- | --- | --- |
| `MAX_CALL_SECONDS` | 180 | 1..180 | Measured from the explicit start action (`Acquire`), so ringing counts. The hang-up begins `EndMargin` early: 5 s, or a tenth of the limit if shorter, so the call is over **before** the limit rather than at it. |
| `MAX_CONCURRENT_CALLS` | 1 | 1 | One real-call permit per API instance (the deployment is a single instance). A second start while the permit is held is rejected with `ErrConcurrentCall` before any dial. |
| `TELEPHONY_TIMEOUT_MS` | 10000 | 1..60000 | Dial and hang-up. |
| `STT_TIMEOUT_MS` | 10000 | 1..60000 | Recognition setup. |
| `LLM_TIMEOUT_MS` | 30000 | 1..60000 | One generation (outer bound; the Claude adapter has its own 30 s). |
| `TTS_TIMEOUT_MS` | 30000 | 1..60000 | One synthesis (outer bound; the ElevenLabs adapter has its own 30 s). |

Out-of-range settings stop the server from building a policy. The error names the
setting, never the configured value.

### Lifecycle contract

1. **Start** (#11): `StartReal(ctx, callID, hooks, dial)` takes the permit, then runs
   the dial under the telephony timeout. If the dial fails or times out, `End` is
   attempted best-effort (a timed-out request may still have created a call) and the
   permit is released before the error returns. Map `ErrConcurrentCall` to 409
   `conflict` and `ErrProviderTimeout` to 503 `provider_unavailable`.
2. **Answered**: `Connected()` starts elapsed time and per-minute charges and
   publishes usage immediately and every 5 s through `Hooks.Usage`.
3. **Usage**: `RecordLLM(input, output)` with the token counts the model reports
   (cancelled suggestions still cost tokens); `RecordTTS(chars)` with characters
   actually sent to synthesis.
4. **Limit**: at the deadline the policy calls `Hooks.End` under the telephony
   timeout. On success it releases the permit and reports `LimitReached(nil)`. On
   failure the call may still be live, so the permit **stays held** and
   `LimitReached(err)` asks the lifecycle to end it and call `Release`.
5. **Terminal** (ended, failed, cancelled, any path): `Release()` frees the permit,
   stops timers and returns the final usage for the last `usage.updated`. It is
   idempotent.

Pass `ProviderTimeLimit()` (seconds) as Twilio's call `TimeLimit` when #11 creates the
call, so the provider also ends the call if our server cannot.

## Rates

No rates are hardcoded. The server reads dated list prices from configuration
(`.env.example` carries the values checked for `docs/provider-matrix.md`). A nonzero
rate requires `USAGE_RATES_CHECKED_ON`. A rate left unset counts as zero, which
under-estimates, so configure all six before a real call.

| Variable | Meaning | Checked 2026-11-10 |
| --- | --- | --- |
| `USAGE_RATE_TELEPHONY_USD_PER_MIN` | Twilio outbound voice, per started minute | 0.014 |
| `USAGE_RATE_MEDIA_USD_PER_MIN` | Twilio Media Streams, per started minute | 0.0044 |
| `USAGE_RATE_STT_USD_PER_MIN` | Deepgram Nova-3 streaming, prorated per second | 0.0077 |
| `USAGE_RATE_LLM_INPUT_USD_PER_MTOK` | Claude input tokens (Sonnet 5.5) | 2 |
| `USAGE_RATE_LLM_OUTPUT_USD_PER_MTOK` | Claude output tokens (Sonnet 5.5) | 10 |
| `USAGE_RATE_TTS_USD_PER_1K_CHARS` | ElevenLabs Flash/Turbo characters | 0.04 |

Sources and the economy alternative are in `docs/provider-matrix.md`. Elapsed time
starts at answer. Voice and media are charged per started minute, which is how
Twilio's US voice pricing is published. The result is rounded to $0.0001 and never
negative.

## Documented sample (estimate only)

A three-minute-cap call hung up at 175 s of connected time, using the provider
matrix's rounded-up volumes (50,000 input tokens, 3,000 output tokens across 20
generations, 1,500 synthesized characters) and the rates above:

| Line | Arithmetic | USD |
| --- | --- | --- |
| Voice | 3 started min × 0.014 | 0.0420 |
| Media Streams | 3 started min × 0.0044 | 0.0132 |
| STT | 175/60 min × 0.0077 | 0.0225 |
| Claude input | 0.05 MTok × 2 | 0.1000 |
| Claude output | 0.003 MTok × 10 | 0.0300 |
| TTS | 1.5K chars × 0.04 | 0.0600 |
| **Estimate** | | **0.2677** |

The resulting `usage.updated` payload, produced by `ExampleEstimate` in
`packages/go/policy/config_test.go` and checked on every test run:

```json
{"elapsed_seconds":175,"estimated_cost_usd":0.2677,"estimate_only":true}
```

This is a fixture, not a measured bill. Step 6 of the manual live check in
`docs/provider-matrix.md` compares a real call's billed usage with this estimate.

## Verification

```sh
moon run policy:build policy:lint policy:test
go test -race ./packages/go/policy/...
moon run :build :lint :typecheck :test
```

Tests use `policytest.Clock`, a manual clock, so the 180 s limit, provider timeouts
and usage ticks are proven without sleeping: hang-up begins at 175 s and not a
millisecond earlier; ringing counts toward the limit; a second concurrent start is
rejected (including 64 racing starts with exactly one winner); a failed or timed-out
dial releases the permit after a best-effort hang-up; a failed limit hang-up keeps the
permit; usage is monotonic, estimate-only and stops at release.

## Not in this issue

Wiring into the start route (#11), Twilio `TimeLimit` (#11), publishing through the
durable event stream (#23), token counts from the Claude adapter and a usage display
in the app.
