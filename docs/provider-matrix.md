# Provider matrix and decision (#1)

Checked November 10, 2026. Prices are public pay-as-you-go list prices in USD on that date and change often; provider dashboards remain the billing authority. No rates are hardcoded in code. Usage estimates (#34) read them from server configuration.

**Decision: custom pipeline.** Twilio Programmable Voice + Media Streams, a streaming STT adapter, the Claude Messages API, and ElevenLabs streaming TTS, orchestrated by our Go API. Managed ElevenLabs Agents is rejected for the MVP. It cannot hold a generated reply until the operator approves it, and that hold is the product's core guarantee. We will not build both.

## Requirements the choice must meet

| # | Requirement | Source |
| --- | --- | --- |
| R1 | The operator app shows every live turn (partial and final) for the active call | AGENTS.md, contracts `turn.updated` |
| R2 | A generated reply is held until approved for one response id and context revision; nothing stale is spoken | contracts `Response`, approval route |
| R3 | Private mid-call instructions change later replies and invalidate pending ones | contracts `Instruction` |
| R4 | Pause, a new instruction, remote speech and end cancel queued generation and audio | contracts "State and events" |
| R5 | Keys stay on the server; the app talks only to our API | AGENTS.md |
| R6 | A three-minute call costs well under $1 and needs no enterprise contract | architecture.md |

## Feasibility

Evidence column: **Doc** = the vendor's published documentation states it (link). **Measured** = we checked it ourselves in this repository. **Pending live** = needs the manual real-call check below; not yet run.

| Requirement | Custom pipeline | Evidence | Managed ElevenLabs Agents + Twilio | Evidence |
| --- | --- | --- | --- | --- |
| R1 live transcript | STT partial/final results come back to our server, which emits `turn.updated` | Doc (both STT candidates sell streaming/realtime products, see prices below); interim-result behavior verified by #20; Pending live | Only through the conversation monitoring WebSocket, which is **enterprise-only**. Events arrive asynchronously and may be out of order | Doc ([Real-time monitoring](https://elevenlabs.io/docs/eleven-agents/guides/realtime-monitoring)) |
| R2 approval hold | Reply text is produced by our server; TTS is not called until the approve route passes the revision check | Measured: `staleApprovalError` and reducer tests in `packages/ts/api/test`, Go state machine `Approve` in `packages/go/conversation` | **No.** Client events are `contextual_update`, `user_message` and `user_activity`; monitoring commands are `end_call`, `transfer_to_number`, `contextual_update` and human takeover (documented for chat). None holds or approves a reply before speech | Doc ([Client events](https://elevenlabs.io/docs/eleven-agents/customization/events/client-to-server-events), [Real-time monitoring](https://elevenlabs.io/docs/eleven-agents/guides/realtime-monitoring)) |
| R3 private instructions | Sent as a separate JSON field to Claude on every generation | Measured: `packages/go/claude` tests (#14) | `contextual_update` adds background context; it does not interrupt or invalidate a reply in flight | Doc (same) |
| R4 cancellation | Twilio `clear` drops buffered audio; `mark` reports playback progress; Claude and TTS requests are cancelled by context | Doc ([Media Streams messages](https://www.twilio.com/docs/voice/media-streams/websocket-messages)); Measured: cancellation tests in `packages/go/claude`; Pending live | Agent handles barge-in internally; operator can only end or transfer | Doc |
| R5 keys server-side | Yes. All vendors are called from Go | Measured: `packages/ts/api` has no vendor calls | Yes | — |
| R6 cost / access | About $0.13–$0.27 per 3 minutes (below); all pay-as-you-go | Doc (prices below) | About $0.28 + LLM pass-through per 3 minutes, and R1 needs an enterprise plan | Doc |
| Audio format | Twilio sends and accepts base64 8 kHz mono mu-law; ElevenLabs TTS supports `output_format=ulaw_8000`, so no resampling on output | Doc ([Media Streams](https://www.twilio.com/docs/voice/media-streams), [ElevenLabs TTS API](https://elevenlabs.io/docs/api-reference/text-to-speech)); Pending live | Handled by the integration | Doc ([Twilio native integration](https://elevenlabs.io/docs/eleven-agents/phone-numbers/twilio-integration/native-integration/)) |

A hybrid was considered but not tested: ElevenLabs Agents with a [custom LLM](https://elevenlabs.io/docs/eleven-agents/phone-numbers/twilio-integration/custom-llm-integration) pointed at our server, which would withhold its reply until approval. It is rejected for the MVP for three reasons. The agent's own turn-taking and timeout behavior during a multi-second approval wait is undocumented and outside our control. R1 would still need the enterprise monitoring feature. Cancellation of audio already queued in the agent is not exposed.

### What was not measured

No real call has been placed for this issue. "Measured" above means a repository test or fixture, not a live provider run. The live check below is a separate, explicit manual step for the owners of #11, #14, #15, #20 and #21. It is not CI.

**Manual live check (not yet run):** use a Twilio trial or paid account, a verified consenting team member's phone as the only destination, and server-side keys in `.env` that are never committed. Record each item in the PR that performs it, without phone numbers, transcripts or audio:

1. Outbound call connects and a bidirectional Media Stream opens (#11, #21).
2. Time from the business's end of speech to a final STT turn (#20).
3. Time from a final turn to a complete Claude suggestion, using the configured model (#14).
4. Time from approval to the first audible TTS audio, `ulaw_8000` (#15).
5. `clear` stops playback mid-sentence within one second; `mark` arrives after playback (#26).
6. Actual billed minutes, characters and tokens for one three-minute call, compared with the estimate below.

## Prices (checked November 10, 2026)

| Component | Option | List price | Source |
| --- | --- | --- | --- |
| Telephony | Twilio outbound call, US local/mobile | $0.0140 / min | [Twilio US voice pricing](https://www.twilio.com/en-us/voice/pricing/us) |
| Telephony | Twilio Media Streams | $0.0044 / min | same |
| Telephony | Twilio local number | $1.15 / month | same |
| STT | Deepgram Nova-3 streaming, monolingual | $0.0077 / min | [Deepgram pricing](https://deepgram.com/pricing) |
| STT | ElevenLabs Scribe v2 Realtime | $0.22 / hour (about $0.0037 / min) | [ElevenLabs API pricing](https://elevenlabs.io/pricing/api) |
| LLM | Claude Sonnet 5.5 | $2 / MTok input, $10 / MTok output | [Claude pricing](https://platform.claude.com/docs/en/about-claude/pricing) |
| LLM | Claude Haiku 5.5 (prompts up to 100k tokens) | $0.10 / MTok input, $0.50 / MTok output | same |
| TTS | ElevenLabs Flash / Turbo | $0.04 / 1K characters | [ElevenLabs API pricing](https://elevenlabs.io/pricing/api) |
| TTS | ElevenLabs Multilingual v2 | $0.08 / 1K characters | same |
| Managed | ElevenLabs Agents | $0.08 / min ($0.16 burst above concurrency), LLM billed separately | same |

Subscription tiers include some free characters, hours and minutes. The examples ignore them because free credits are not an operating budget. A Claude chat or Claude Code subscription does not cover API usage.

## Three-minute cost example

**Volumes.** Measured in the #5 mock scenarios (`docs/fixtures/*.json`): each roughly 40-second conversation has about 270 characters of spoken assistant text and 4 generations. Scaled to 180 seconds, that is about 1,220 characters and 18 generations. The example rounds up to **1,500 TTS characters and 20 generations**, to allow for cancelled or regenerated suggestions, which still cost LLM tokens but not TTS. Assumed, not measured: **2,500 input tokens and 150 output tokens per generation**, covering the system prompt, goal, transcript so far and instructions. Step 3 of the live check measures this.

| Line | Arithmetic | Custom, quality (Sonnet + Deepgram) | Custom, economy (Haiku + Scribe) | Managed Agents |
| --- | --- | --- | --- | --- |
| Twilio voice | 3 min × $0.0140 | $0.0420 | $0.0420 | $0.0420 |
| Media Streams | 3 min × $0.0044 | $0.0132 | $0.0132 | included in integration |
| STT | 3 min × rate | $0.0231 | $0.0110 | included |
| Claude input | 50,000 tok × rate | $0.1000 | $0.0050 | LLM pass-through, not included |
| Claude output | 3,000 tok × rate | $0.0300 | $0.0015 | — |
| TTS | 1.5K chars × $0.04 | $0.0600 | $0.0600 | included |
| Agents | 3 min × $0.08 | — | — | $0.2400 |
| **Total per call** | | **≈ $0.27** | **≈ $0.13** | **≈ $0.28 + LLM** |

Plus $1.15 per month for the number. Claude input dominates the quality configuration and grows with transcript length, so #14 should keep the prompt bounded (finalized turns only) and may use prompt caching for the fixed system prompt and goal. The MVP's 180-second cap and single concurrent call bound the worst case per call to roughly these figures.

## Consequences for later issues

- #6 defines Telephony, Transcriber, Responder and Synthesizer interfaces for this pipeline only.
- #20 chooses between Deepgram Nova-3 and ElevenLabs Scribe Realtime. Both fit the budget. #20 must confirm the chosen one accepts 8 kHz mu-law input, or document and test the conversion, with a recorded fixture.
- #14 keeps the model configurable; the default is decided from step 3 of the live check, not from this table.
- #15 requests `ulaw_8000` from ElevenLabs and forwards it to Twilio without resampling.
- #34 configures these rates as server settings for `usage.updated`, which is always `estimate_only: true`.
