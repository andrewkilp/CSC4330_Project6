# Architecture and provider research

Research checked November 5, 2026. This is a starter and implementation plan, not a deployed calling product.

## Recommended path

Keep the familiar Expo mobile app and Go API, with a browser preview supported by the same Expo code. Ship a deterministic, zero-key simulation first. Implement a server-controlled pipeline for the real-call path:

`Twilio call audio -> streaming STT -> finalized transcript -> Claude response -> operator approval when required -> ElevenLabs TTS -> Twilio playback`

The API owns the state machine. An SSE feed carries transcripts, suggestions and instruction acknowledgements to the app. HTTP mutations send operator instructions. This replaces Project 5's “no live push” restriction, which does not fit a live transcript. Start with one API instance, one concurrent real call, a three-minute limit, and test-recipient allowlisting.

Twilio supports bidirectional audio over WebSockets. Its media protocol provides playback markers and buffer clearing, useful for interruption and pause behavior. [Media Streams](https://www.twilio.com/docs/voice/media-streams) and [WebSocket messages](https://www.twilio.com/docs/voice/media-streams/websocket-messages).

Claude accepts conversation history and supports streaming responses. Supply the authorized goal, finalized turns and ordered private operator instructions on each generation. Keep provider/model identifiers configurable; measure response latency before choosing a model. [Messages API](https://platform.claude.com/docs/en/api/messages/create) and [streaming](https://platform.claude.com/docs/en/build-with-claude/streaming).

ElevenLabs offers streaming speech output, including telephony formats. Keep its voice/model configuration server-side and test cancellation and encoding. Evaluate its realtime speech recognition alongside an alternative before freezing STT. [TTS integration](https://elevenlabs.io/blog/text-to-speech-api-integration) and [model capabilities](https://elevenlabs.io/docs/overview/models/).

## Managed alternative

ElevenLabs agents can connect to Twilio through a native integration or a register-call flow. This reduces audio orchestration work. [Twilio integration](https://elevenlabs.io/docs/eleven-agents/phone-numbers/twilio-integration/native-integration/) and [register-call flow](https://elevenlabs.io/docs/eleven-agents/phone-numbers/twilio-integration/register-call).

Their conversation WebSocket supports contextual updates that add private background information. However, a native Twilio connection does not by itself prove our operator app can attach to that same session, receive every live turn, and enforce response-by-response approval. Stage 1 must test those capabilities before adopting it. Context updates are not an interrupt/approval mechanism. [Client events](https://elevenlabs.io/docs/eleven-agents/customization/events/client-to-server-events).

For this project, the custom pipeline is the default because the operator needs control of both text display and when generated speech plays. A managed alternative may replace it only after an experiment demonstrates the same steering, cancellation and approval guarantees. Do not implement both systems in the two-week MVP.

## Cost and feasibility

- Local simulation: no provider account, no phone minutes, no paid model calls.
- Real calls: telephony, recognition, model tokens and synthesis may all incur charges. Free credits are temporary, not the operating budget.
- A Claude chat or Claude Code subscription does not supply application API usage. Configure a server API key with its own billing. [Anthropic billing explanation](https://support.anthropic.com/en/articles/9876003-i-subscribe-to-a-paid-claude-ai-plan-why-do-i-have-to-pay-separately-for-api-usage-on-console).
- Twilio trials restrict destinations to verified numbers and may restrict custom call features. A trial is suitable for a capability check with team phones, not a promise of free calls to arbitrary restaurants. [Trial voice restrictions](https://www.twilio.com/docs/usage/trials/try-out-voice).
- Stage 1 records current per-minute/token/character rates in a provider matrix, with source URLs and a measured three-minute example. No rates are hardcoded into this starter. Stage 4 adds usage estimates and hard duration/concurrency limits; provider dashboards remain the billing authority.

## Two-week scope

Reservations and ordinary appointment scheduling, one active call per person, English first, a live text transcript, editable suggested responses, mid-call private instructions, pause/end, and an evidence-backed result summary. Start all test calls with a clear AI introduction. Do not record raw audio by default. Public deployment requires working authentication, origin/CSRF checks, callback signatures, owner isolation and retention controls from the planned issues.

No payments, calendar account integrations, mass outbound calling, production contact import, voice cloning, transfers, multilingual support or multiple real-call providers in the MVP. Users verify the outcome before relying on it.

## What changed from Project 5

Preserved: apps/mobile, apps/api, packages/go, packages/ts, proto, db, docker, scripts, docs, pnpm workspace, moon tasks, pinned toolchain, agent instructions and GitHub templates/CI.

Removed: food-domain screens and assets, camera and photo processing, geolocation, maps, sharing, point scoring, recipes, leaderboards, crews, scan/plate storage, food-specific vision and places providers, generated domain contracts, old migrations, secret-manager integration and unrelated review configuration. The baseline Go API uses only the standard library. Database and provider packages arrive when their assigned tasks implement them.

Kept only Expo navigation and platform essentials, React Native, web support, TypeScript and Biome. Toolchain versions come from Project 5; the lockfile is regenerated for the reduced graph. The optional Postgres container is not started by the baseline.
