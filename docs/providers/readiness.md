# Provider readiness (#40)

`GET /v1/providers` returns `{mode, telephony, stt, llm, tts}` as defined in
`docs/contracts.md`. Each provider is `{available}` or `{available: false, reason}`
with a fixed, user-safe sentence. The route reads only whether server settings are
present. It never returns, logs or compares key values, never contacts a vendor and
never reports account balances.

| Provider | Needs (presence only) |
| --- | --- |
| telephony | a real adapter in this build (#11), `ENABLE_REAL_CALLS=true`, `TWILIO_ACCOUNT_SID`, `TWILIO_AUTH_TOKEN`, `TWILIO_FROM_NUMBER`, an `https://` `PUBLIC_BASE_URL` |
| stt | `DEEPGRAM_API_KEY` (the #20 adapter) |
| llm | `ANTHROPIC_API_KEY`, `CLAUDE_MODEL` |
| tts | `ELEVENLABS_API_KEY`, `ELEVENLABS_VOICE_ID`, `ELEVENLABS_MODEL_ID` |

`mode` is `real` only when `ENABLE_REAL_CALLS=true`, `CALL_MODE=real` and all four are
available; otherwise `mock`. Adapters (or the composition that owns them) call
`ReportOutage(kind)` after a user-safe unavailable error and `ReportRecovered(kind)`
after success; an outage expires after one minute. While a provider is out, `mode` is
`mock` and `RequireReal()` returns `ErrRealUnavailable`.

## Mock fallback is explicit

Nothing changes an in-progress call's mode. The start route (#11) must call
`RequireReal()` for a real call and answer 503 `provider_unavailable` when it fails.
The app then offers the operator a **new** call in mock mode, clearly labelled, using
a fictional 555-01XX destination. `packages/ts/api/src/providers.ts` provides
`getProviders`, `parseProviders`, `canStartRealCall` and `realCallBlockers` for that
screen.

The current build includes no real telephony adapter, so the template always reports
`mode: "mock"` with telephony unavailable. The Settings screen
(`apps/mobile/src/app/settings.tsx`) waits for the shared UI primitives in #2.

## Fixtures

`packages/ts/api/test/fixtures/providers.json` is shared by the Go handler tests and
the TypeScript client tests: template defaults, missing keys, real calling switched
off, a speech recognition outage and everything ready. Configured values are fake
sentinels; both test suites assert none appears in a response.

```sh
moon run handlers:test client:test api:test
moon run :build :lint :typecheck :test
```
