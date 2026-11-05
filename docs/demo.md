# Switchboard demo

This is a working local simulation of the product workflow, with a Go server and the Expo browser UI. It demonstrates transcript updates, private instructions, revised suggestions, approval, pause/resume, end and an evidence-linked simulated outcome. It uses no paid APIs, no microphone and no phone network.

## Run

```sh
proto install
pnpm install --frozen-lockfile
moon run api:demo
```

Open http://127.0.0.1:8080. The demo task builds the browser assets and starts the Go server in explicit DEMO_MODE. The UI and API share this origin. Use the 127.0.0.1 address shown here; a separately hosted Metro page is not the complete demo. Ctrl+C stops it. A refresh starts a fresh browser view. Server data is temporary and disappears when the process stops.

## Two-minute presentation

1. Pick **An evening at Marigold**, then **Start simulated call**. Watch the business's words appear incrementally. The opening AI introduction is a fixed script authorized by starting the demo.
2. When the business asks about 7:30 and seating, choose **Accept 7:30; ask for indoor seating.** Click **Send instruction**. The private instruction appears below, the direction revision increases, and the suggested reply changes from patio to indoor seating.
3. Optionally edit the reply. Click **Pause AI**, then **Resume AI** to show that the previous suggestion is cancelled and regenerated. Approve the new reply with **Approve & deliver reply**.
4. The approved text enters the transcript after simulated delivery. The fictional business confirms the details, the call ends, and a clearly labelled simulated confirmation appears. No real reservation exists.
5. Choose **Try another demo**, then **A little time at Juniper**. Send **Thursday at 3 works; just a trim.** Approve the revised reply and see the trim reflected in the business transcript and outcome.

**Other useful paths:** send “Don't book. Politely decline.” to get a declined outcome; use an unsupported instruction to get a clarification rather than a fabricated booking; end while a reply is being delivered to demonstrate cancellation.

## Architecture and limits

- `packages/go/demo` owns the in-memory fictional sessions and time-delayed transcript simulation. It uses the real pure conversation state machine for response approval, cancellation tokens and terminal state.
- `packages/go/handlers/demo.go` provides `/demo/sessions` and session read/action routes only in demo mode. The host/origin guard and loopback listener keep this a local presentation tool.
- `packages/ts/api/src/demo.ts` applies contiguous events with the shared reducer and uses the consistent snapshot if an action/poll response reveals a gap. Older HTTP results cannot replace a newer cursor.
- The browser polls at 350 ms. This is a demo transport, not the production SSE implementation. No production task should be closed merely because this demo covers its visible behavior.
- Responses use explicit scripted rules for seating, haircut/trim and declining. This is not Claude reasoning. Unknown directions ask for clarification; direct editing lets the presenter choose the exact reply.
- “Delivery” is a timed acknowledgement, with no generated audio. Pause suspends the simulated conversation. The fictional number is +1 (202) 555-0123. Dates, businesses and references are example data.
- There is no account system, durable history, external booking, real provider or deployment. The server allows up to 32 demo sessions per process. Restart it to clear them.

## Verification

`moon run :build :lint :typecheck :test` includes the conversation table tests, typed reducer fixtures and demo scenario tests. The demo tests exercise both success paths, a stale approval after steering, pause/instruction/resume, end during delivery, and unsupported instructions without a false confirmation. Browser verification captures desktop and 390 px mobile views and exercises both flows.

The reference is [the generated mockup](design/switchboard-mockup.png). Captured screenshots live in `docs/demo/` when available.
