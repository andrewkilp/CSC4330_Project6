import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  createEventState,
  EventValidationError,
  expireCursor,
  hydrateSnapshot,
  parseEvent,
  reduceEvent,
} from "../src/events.ts";

const fixtures = JSON.parse(
  readFileSync(new URL("./fixtures/events.json", import.meta.url), "utf8"),
);
const callId = fixtures.call.id;
const otherCall = "99999999-9999-4999-8999-999999999999";
const secondReply = "66666666-6666-4666-8666-666666666666";
function event(seq, type, payload, revision = payload.context_revision ?? 0) {
  return {
    id: `00000000-0000-4000-8000-${String(seq).padStart(12, "0")}`,
    call_id: callId,
    seq,
    type,
    occurred_at: "2026-11-05T18:00:03Z",
    context_revision: revision,
    payload: structuredClone(payload),
  };
}
function initial() {
  return reduceEvent(
    createEventState(callId),
    event(1, "call.updated", fixtures.call),
  );
}
function freeze(value) {
  if (value && typeof value === "object") {
    Object.freeze(value);
    for (const item of Object.values(value)) freeze(item);
  }
  return value;
}
function snapshot(last_seq, overrides = {}) {
  return {
    call: fixtures.call,
    last_seq,
    turns: [],
    instructions: [],
    responses: [],
    ...overrides,
  };
}

test("partial turns replace by id and finalized turns never regress", () => {
  const before = freeze(initial());
  const partial = freeze(event(2, "turn.updated", fixtures.partial_turn));
  const next = reduceEvent(before, partial);
  assert.deepEqual(before.turnOrder, []);
  assert.equal(next.turns[fixtures.partial_turn.id].is_final, false);
  const final = reduceEvent(
    next,
    event(3, "turn.updated", fixtures.final_turn),
  );
  assert.deepEqual(final.turnOrder, [fixtures.final_turn.id]);
  assert.equal(
    final.turns[fixtures.final_turn.id].text,
    fixtures.final_turn.text,
  );
  const late = reduceEvent(
    final,
    event(4, "turn.updated", fixtures.partial_turn),
  );
  assert.equal(late.turns[fixtures.final_turn.id].is_final, true);
  assert.equal(late.lastSeq, 4);
  assert.equal(final.lastSeq, 3);
});

test("replay duplicates and older events do not change state", () => {
  const first = event(2, "turn.updated", fixtures.partial_turn);
  const state = reduceEvent(initial(), first);
  assert.equal(reduceEvent(state, first), state);
  assert.equal(
    reduceEvent(state, event(1, "call.updated", fixtures.call)),
    state,
  );
});

test("a sequence gap freezes projection until an authoritative snapshot", () => {
  let state = reduceEvent(
    initial(),
    event(2, "response.updated", fixtures.response),
  );
  state = reduceEvent(state, event(4, "turn.updated", fixtures.final_turn));
  assert.deepEqual(state.recovery, { reason: "sequence_gap", afterSeq: 2 });
  assert.equal(state.lastSeq, 2);
  assert.equal(state.currentResponseId, null);
  assert.deepEqual(state.turnOrder, []);
  assert.equal(
    reduceEvent(state, event(3, "turn.updated", fixtures.partial_turn)),
    state,
  );
  state = hydrateSnapshot(state, snapshot(4, { turns: [fixtures.final_turn] }));
  assert.equal(state.recovery, null);
  assert.equal(state.lastSeq, 4);
  assert.equal(state.turns[fixtures.final_turn.id].is_final, true);
  state = reduceEvent(state, event(5, "response.updated", fixtures.response));
  assert.equal(state.currentResponseId, fixtures.response.id);
});

test("new instructions retire suggestions and consume stale events without applying them", () => {
  let state = reduceEvent(
    initial(),
    event(2, "response.updated", fixtures.response),
  );
  state = reduceEvent(
    state,
    event(3, "instruction.updated", fixtures.instruction),
  );
  assert.equal(state.contextRevision, 1);
  assert.equal(state.call.context_revision, 1);
  assert.equal(state.currentResponseId, null);
  const stale = reduceEvent(
    state,
    event(4, "response.updated", { ...fixtures.response, status: "approved" }),
  );
  assert.equal(stale.lastSeq, 4);
  assert.equal(stale.responses[fixtures.response.id].status, "suggested");
  state = reduceEvent(
    stale,
    event(5, "response.updated", {
      ...fixtures.response,
      id: secondReply,
      context_revision: 1,
      text: "Indoor seating, please.",
    }),
  );
  const old = reduceEvent(
    state,
    event(6, "response.updated", { ...fixtures.response, context_revision: 1 }),
  );
  assert.equal(old.currentResponseId, secondReply);
  assert.equal(old.responses[secondReply].text, "Indoor seating, please.");
});

test("a new final business turn hides the previous suggestion in the same revision", () => {
  let state = reduceEvent(
    initial(),
    event(2, "response.updated", fixtures.response),
  );
  state = reduceEvent(state, event(3, "turn.updated", fixtures.final_turn));
  assert.equal(state.currentResponseId, null);
  state = reduceEvent(
    state,
    event(4, "response.updated", { ...fixtures.response, status: "approved" }),
  );
  assert.equal(state.responses[fixtures.response.id].status, "suggested");
  state = reduceEvent(
    state,
    event(5, "response.updated", {
      ...fixtures.response,
      id: secondReply,
      status: "generating",
      text: "",
    }),
  );
  assert.equal(state.currentResponseId, secondReply);
});

test("a superseded response id cannot replace the current generation", () => {
  let state = reduceEvent(
    initial(),
    event(2, "response.updated", fixtures.response),
  );
  state = reduceEvent(
    state,
    event(3, "response.updated", {
      ...fixtures.response,
      id: secondReply,
      status: "generating",
      text: "",
    }),
  );
  state = reduceEvent(
    state,
    event(4, "response.updated", { ...fixtures.response, status: "approved" }),
  );
  assert.equal(state.currentResponseId, secondReply);
  assert.equal(state.responses[fixtures.response.id].status, "suggested");
});

test("response playback follows approval and terminal responses cannot reopen", () => {
  let state = reduceEvent(
    initial(),
    event(2, "response.updated", fixtures.response),
  );
  state = reduceEvent(
    state,
    event(3, "response.updated", { ...fixtures.response, status: "speaking" }),
  );
  assert.equal(state.responses[fixtures.response.id].status, "suggested");
  for (const [seq, status] of [
    [4, "approved"],
    [5, "speaking"],
    [6, "spoken"],
    [7, "suggested"],
  ])
    state = reduceEvent(
      state,
      event(seq, "response.updated", { ...fixtures.response, status }),
    );
  assert.equal(state.responses[fixtures.response.id].status, "spoken");
  assert.equal(state.currentResponseId, null);
  assert.deepEqual(state.turnOrder, []);
});

test("ended calls ignore late activity but accept summaries and usage", () => {
  let state = reduceEvent(
    initial(),
    event(2, "call.updated", { ...fixtures.call, status: "ended" }),
  );
  state = reduceEvent(state, event(3, "call.updated", fixtures.call));
  state = reduceEvent(state, event(4, "response.updated", fixtures.response));
  state = reduceEvent(state, event(5, "turn.updated", fixtures.final_turn));
  assert.equal(state.call.status, "ended");
  assert.deepEqual(state.turnOrder, []);
  assert.equal(state.currentResponseId, null);
  state = reduceEvent(state, event(6, "summary.ready", fixtures.summary));
  state = reduceEvent(
    state,
    event(7, "usage.updated", {
      elapsed_seconds: 20,
      estimated_cost_usd: 0.02,
      estimate_only: true,
    }),
  );
  assert.equal(state.summary.outcome, "needs_follow_up");
  assert.equal(state.usage.elapsed_seconds, 20);
  state = reduceEvent(
    state,
    event(8, "usage.updated", {
      elapsed_seconds: 10,
      estimated_cost_usd: 0.01,
      estimate_only: true,
    }),
  );
  assert.equal(state.usage.elapsed_seconds, 20);
});

test("confirmed summaries need final business evidence and a terminal call", () => {
  const summary = {
    ...fixtures.summary,
    outcome: "confirmed",
    evidence_turn_ids: [fixtures.final_turn.id],
  };
  let state = reduceEvent(initial(), event(2, "summary.ready", summary));
  assert.equal(state.summary, null);
  state = reduceEvent(state, event(3, "turn.updated", fixtures.final_turn));
  state = reduceEvent(
    state,
    event(4, "call.updated", { ...fixtures.call, status: "ended" }),
  );
  state = reduceEvent(
    state,
    event(5, "summary.ready", { ...summary, evidence_turn_ids: [secondReply] }),
  );
  assert.equal(state.summary, null);
  state = reduceEvent(state, event(6, "summary.ready", summary));
  assert.equal(state.summary.outcome, "confirmed");
});

test("expired cursors retain the transcript and require a snapshot before replay", () => {
  let state = reduceEvent(
    initial(),
    event(2, "turn.updated", fixtures.final_turn),
  );
  state = reduceEvent(state, event(3, "response.updated", fixtures.response));
  const expired = expireCursor(state);
  assert.equal(expired.recovery.reason, "cursor_expired");
  assert.equal(expired.lastSeq, 3);
  assert.equal(expired.currentResponseId, null);
  assert.deepEqual(expired.turns, state.turns);
  assert.equal(
    reduceEvent(
      expired,
      event(4, "provider.error", {
        provider: "stt",
        code: "offline",
        retryable: true,
      }),
    ),
    expired,
  );
  const recovered = hydrateSnapshot(
    expired,
    snapshot(9, {
      call: { ...fixtures.call, context_revision: 1 },
      turns: [fixtures.final_turn],
      instructions: [fixtures.instruction],
      responses: [
        { ...fixtures.response, id: secondReply, context_revision: 1 },
      ],
    }),
  );
  assert.equal(recovered.recovery, null);
  assert.equal(recovered.contextRevision, 1);
  assert.equal(recovered.currentResponseId, secondReply);
  assert.equal(
    reduceEvent(
      recovered,
      event(
        10,
        "provider.error",
        { provider: "stt", code: "offline", retryable: true },
        1,
      ),
    ).lastSeq,
    10,
  );
});

test("snapshots reject stale cursor, cross-call entities and conflicting active replies", () => {
  const state = initial();
  const cases = [
    snapshot(0),
    snapshot(2, { call: { ...fixtures.call, status: "draft" } }),
    snapshot(2, { call: { ...fixtures.call, id: otherCall } }),
    snapshot(2, { turns: [{ ...fixtures.final_turn, call_id: otherCall }] }),
    snapshot(2, { turns: [fixtures.final_turn, fixtures.final_turn] }),
    snapshot(2, {
      responses: [fixtures.response, { ...fixtures.response, id: secondReply }],
    }),
    snapshot(2, { responses: [{ ...fixtures.response, context_revision: 1 }] }),
  ];
  for (const input of cases)
    assert.throws(() => hydrateSnapshot(state, input), EventValidationError);
  const ended = reduceEvent(
    state,
    event(2, "call.updated", { ...fixtures.call, status: "ended" }),
  );
  assert.throws(
    () => hydrateSnapshot(ended, snapshot(3)),
    EventValidationError,
  );
  assert.throws(
    () =>
      hydrateSnapshot(
        ended,
        snapshot(3, { call: { ...fixtures.call, status: "failed" } }),
      ),
    EventValidationError,
  );
});

test("validation covers every event type and detaches mutable input", () => {
  const samples = [
    event(1, "call.updated", fixtures.call),
    event(2, "turn.updated", fixtures.partial_turn),
    event(3, "instruction.updated", fixtures.instruction),
    event(4, "response.updated", fixtures.response),
    event(5, "summary.ready", fixtures.summary),
    event(6, "provider.error", {
      provider: "tts",
      code: "timeout",
      retryable: true,
    }),
    event(7, "usage.updated", {
      elapsed_seconds: 10,
      estimated_cost_usd: 0,
      estimate_only: true,
    }),
  ];
  for (const sample of samples)
    assert.equal(parseEvent(sample).type, sample.type);
  const mutable = event(1, "call.updated", fixtures.call);
  const parsed = parseEvent(mutable);
  mutable.payload.goal.constraints[0] = "changed";
  assert.equal(parsed.payload.goal.constraints[0], "Ask about patio seating");
  const appointment = event(1, "call.updated", {
    ...fixtures.call,
    goal: {
      ...fixtures.call.goal,
      kind: "appointment",
      service: "Haircut",
      party_size: undefined,
    },
  });
  assert.equal(parseEvent(appointment).payload.goal.kind, "appointment");
});

test("invalid envelopes and payloads cannot enter state or leak their values", () => {
  const base = event(2, "response.updated", fixtures.response);
  const cases = [
    null,
    [],
    { ...base, id: "not-a-uuid" },
    { ...base, seq: 0 },
    { ...base, seq: 1.5 },
    { ...base, seq: Number.MAX_SAFE_INTEGER + 1 },
    { ...base, context_revision: -1 },
    { ...base, occurred_at: "yesterday" },
    { ...base, occurred_at: "2026-02-30T18:00:00Z" },
    { ...base, type: "arbitrary" },
    { ...base, payload: { ...base.payload, context_revision: 1 } },
    { ...base, payload: { ...base.payload, call_id: otherCall } },
    { ...base, payload: { ...base.payload, needs_approval: "true" } },
    { ...base, payload: { ...base.payload, text: "x".repeat(1501) } },
    event(2, "call.updated", {
      ...fixtures.call,
      goal: { ...fixtures.call.goal, timezone: "not/a/zone" },
    }),
    event(2, "call.updated", {
      ...fixtures.call,
      goal: { ...fixtures.call.goal, party_size: 0 },
    }),
    event(2, "turn.updated", { ...fixtures.final_turn, ended_at_ms: 100 }),
    event(2, "usage.updated", {
      elapsed_seconds: 2,
      estimated_cost_usd: -1,
      estimate_only: true,
    }),
    event(2, "summary.ready", { ...fixtures.summary, outcome: "confirmed" }),
  ];
  const state = freeze(initial());
  for (const input of cases)
    assert.throws(() => reduceEvent(state, input), EventValidationError);
  assert.equal(state.lastSeq, 1);
  assert.throws(
    () =>
      reduceEvent(state, {
        ...base,
        call_id: otherCall,
        payload: { ...base.payload, call_id: otherCall },
      }),
    EventValidationError,
  );
  assert.throws(
    () =>
      parseEvent({
        ...base,
        payload: { ...base.payload, text: { secret: "do not print this" } },
      }),
    (error) =>
      error instanceof EventValidationError &&
      !error.message.includes("do not print this"),
  );
});
