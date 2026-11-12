import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  CALL_STATUSES,
  ContractValidationError,
  createEventState,
  ERROR_CODES,
  ERROR_HTTP_STATUS,
  hydrateSnapshot,
  LIMITS,
  parseApiError,
  parseApproveResponseRequest,
  parseCall,
  parseControlCallRequest,
  parseCreateCallRequest,
  parseCreateInstructionRequest,
  parseEvent,
  parseListCallsQuery,
  parseProviderError,
  parseResponse,
  parseStartCallRequest,
  reduceEvent,
  staleApprovalError,
} from "../src/index.ts";

const read = (path) =>
  JSON.parse(readFileSync(new URL(path, import.meta.url), "utf8"));
const fx = read("./fixtures/contracts.json");
const clone = (value) => structuredClone(value);

// Asserts a parser rejects input with exactly this field and never echoes the value.
function rejects(parse, input, field, ...rest) {
  assert.throws(
    () => parse(input, ...rest),
    (error) =>
      error instanceof ContractValidationError &&
      error.field === field &&
      !error.message.includes("secret-value"),
    `expected rejection on ${field}`,
  );
}

test("restaurant goal request round-trips through the frozen schema", () => {
  const parsed = parseCreateCallRequest(clone(fx.restaurant_create_call));
  assert.deepEqual(
    JSON.parse(JSON.stringify(parsed)),
    fx.restaurant_create_call,
  );
  assert.equal(parsed.goal.kind, "restaurant");
  assert.equal(parsed.goal.party_size, 4);
  assert.equal(parsed.goal.service, undefined);
});

test("appointment goal request round-trips through the frozen schema", () => {
  const parsed = parseCreateCallRequest(clone(fx.appointment_create_call));
  assert.deepEqual(
    JSON.parse(JSON.stringify(parsed)),
    fx.appointment_create_call,
  );
  assert.equal(parsed.goal.kind, "appointment");
  assert.equal(parsed.goal.party_size, undefined);
});

test("create-call requests reject invalid goals, numbers and consent", () => {
  const r = fx.restaurant_create_call,
    a = fx.appointment_create_call;
  const cases = [
    [{ ...r, goal: { ...r.goal, party_size: 0 } }, "goal.party_size"],
    [{ ...r, goal: { ...r.goal, party_size: 21 } }, "goal.party_size"],
    [{ ...r, goal: { ...r.goal, party_size: 2.5 } }, "goal.party_size"],
    [{ ...r, goal: { ...r.goal, party_size: undefined } }, "goal.party_size"],
    [{ ...r, goal: { ...r.goal, service: "Dinner" } }, "goal.service"],
    [{ ...a, goal: { ...a.goal, service: undefined } }, "goal.service"],
    [{ ...a, goal: { ...a.goal, service: "x".repeat(161) } }, "goal.service"],
    [{ ...a, goal: { ...a.goal, party_size: 2 } }, "goal.party_size"],
    [{ ...r, goal: { ...r.goal, kind: "payment" } }, "goal.kind"],
    [
      { ...r, goal: { ...r.goal, preferred_at: "2026-11-14T19:00:00" } },
      "goal.preferred_at",
    ],
    [
      { ...r, goal: { ...r.goal, preferred_at: "2026-02-30T19:00:00Z" } },
      "goal.preferred_at",
    ],
    [{ ...r, goal: { ...r.goal, timezone: "Mars/Olympus" } }, "goal.timezone"],
    [
      { ...r, goal: { ...r.goal, constraints: Array(13).fill("ok") } },
      "goal.constraints",
    ],
    [
      { ...r, goal: { ...r.goal, constraints: ["x".repeat(201)] } },
      "goal.constraints[0]",
    ],
    [{ ...r, goal: { ...r.goal, contact_name: "  " } }, "goal.contact_name"],
    [
      { ...a, goal: { ...a.goal, contact_phone: "225-555-0119" } },
      "goal.contact_phone",
    ],
    [{ ...r, goal: { ...r.goal, secret: "secret-value" } }, "goal.secret"],
    [{ ...r, destination: "2255550142" }, "destination"],
    // Mock calls must use the reserved fictional range.
    [{ ...r, destination: "+12255551234" }, "destination"],
    [{ ...r, business_name: "" }, "business_name"],
    [{ ...r, business_name: "x".repeat(121) }, "business_name"],
    [{ ...r, approval_mode: "never" }, "approval_mode"],
    [{ ...r, consent_acknowledged: false }, "consent_acknowledged"],
    [{ ...r, consent_acknowledged: "true" }, "consent_acknowledged"],
    [{ ...r, idempotency_key: "not-a-uuid" }, "idempotency_key"],
    [{ ...r, mode: "live" }, "mode"],
    // The browser never supplies ownership.
    [
      { ...r, owner_id: "4e3fa076-8041-4db2-8f91-5c6d7e8f90a3" },
      "request.owner_id",
    ],
  ];
  for (const [input, field] of cases)
    rejects(parseCreateCallRequest, input, field);
  // A real-mode request may use any E.164 destination; allowlisting is server-side.
  assert.equal(
    parseCreateCallRequest({ ...r, mode: "real", destination: "+12255551234" })
      .mode,
    "real",
  );
});

test("stale approval produces the documented 409 conflict body", () => {
  const { call, response, request, error, http_status } = fx.stale_approval;
  const parsedCall = parseCall(clone(call));
  const parsedResponse = parseResponse(clone(response));
  const parsedRequest = parseApproveResponseRequest(clone(request));
  const result = staleApprovalError(parsedCall, parsedResponse, parsedRequest);
  assert.deepEqual(result, error);
  assert.deepEqual(parseApiError(clone(error), http_status), error);
  assert.equal(ERROR_HTTP_STATUS[result.error.code], http_status);

  // Approval at the current revision but for an older response is still stale.
  assert.equal(
    staleApprovalError(parsedCall, parsedResponse, { context_revision: 2 })
      ?.error.field,
    "context_revision",
  );
  // Current revision, suggested response, connected call: approvable.
  const current = { ...parsedResponse, context_revision: 2 };
  assert.equal(
    staleApprovalError(parsedCall, current, { context_revision: 2 }),
    null,
  );
  // Paused or ended calls and non-suggested responses cannot be approved.
  for (const status of ["paused", "ended"])
    assert.equal(
      staleApprovalError({ ...parsedCall, status }, current, {
        context_revision: 2,
      })?.error.field,
      "status",
    );
  assert.equal(
    staleApprovalError(
      parsedCall,
      { ...current, status: "cancelled" },
      { context_revision: 2 },
    )?.error.field,
    "status",
  );
});

test("error bodies must use a known code that matches the HTTP status", () => {
  for (const code of ERROR_CODES)
    assert.equal(
      parseApiError(
        { error: { code, message: "Try again." } },
        ERROR_HTTP_STATUS[code],
      ).error.code,
      code,
    );
  const err = fx.stale_approval.error;
  rejects(parseApiError, err, "error.status", 400);
  rejects(
    parseApiError,
    { error: { ...err.error, code: "teapot" } },
    "error.code",
  );
  rejects(
    parseApiError,
    { error: { ...err.error, message: "" } },
    "error.message",
  );
  rejects(
    parseApiError,
    { error: { ...err.error, message: "x".repeat(501) } },
    "error.message",
  );
  rejects(parseApiError, { message: "flat" }, "error");
});

test("event envelope fixture validates and feeds the reducer", () => {
  const event = parseEvent(clone(fx.event_envelope));
  assert.deepEqual(JSON.parse(JSON.stringify(event)), fx.event_envelope);
  const snapshot = {
    call: { ...fx.stale_approval.call, context_revision: 1 },
    last_seq: 6,
    turns: [],
    instructions: [],
    responses: [],
  };
  const state = reduceEvent(
    hydrateSnapshot(createEventState(event.call_id), snapshot),
    fx.event_envelope,
  );
  assert.equal(state.lastSeq, 7);
  assert.equal(state.currentResponseId, fx.event_envelope.payload.id);

  const e = fx.event_envelope;
  const bad = [
    [{ ...e, seq: 0 }, "seq"],
    [{ ...e, type: "call.deleted" }, "type"],
    [{ ...e, occurred_at: "2026-11-10T12:01:09-06:00" }, "occurred_at"],
    [{ ...e, context_revision: 2 }, "payload.context_revision"],
    [
      {
        ...e,
        payload: { ...e.payload, call_id: fx.stale_approval.call.owner_id },
      },
      "payload.call_id",
    ],
    [{ ...e, payload: { ...e.payload, text: "" } }, "response.text"],
    [
      { ...e, payload: { ...e.payload, text: "x".repeat(1501) } },
      "response.text",
    ],
  ];
  for (const [input, field] of bad) rejects(parseEvent, input, field);
});

test("both generated mock scenarios conform to the frozen event contract", () => {
  for (const name of [
    "restaurant-time-conflict",
    "appointment-missing-detail",
  ]) {
    const fixture = read(`../../../../docs/fixtures/${name}.json`);
    let state = createEventState(fixture.events[0].call_id);
    for (const event of fixture.events) {
      parseEvent(event);
      state = reduceEvent(state, event);
    }
    assert.equal(state.lastSeq, fixture.events.length, name);
    assert.equal(state.summary?.outcome, fixture.expected.outcome, name);
  }
});

test("operator requests reject stale shapes and unknown fields", () => {
  assert.deepEqual(
    parseCreateInstructionRequest(clone(fx.instruction_request)),
    fx.instruction_request,
  );
  assert.deepEqual(
    parseControlCallRequest(clone(fx.control_request)),
    fx.control_request,
  );
  const key = fx.control_request.idempotency_key;
  assert.deepEqual(parseStartCallRequest({ idempotency_key: key }), {
    idempotency_key: key,
  });
  const i = fx.instruction_request;
  rejects(parseCreateInstructionRequest, { ...i, text: " " }, "text");
  rejects(
    parseCreateInstructionRequest,
    { ...i, text: "x".repeat(LIMITS.instructionText + 1) },
    "text",
  );
  rejects(
    parseCreateInstructionRequest,
    { ...i, expected_revision: -1 },
    "expected_revision",
  );
  rejects(
    parseCreateInstructionRequest,
    { text: i.text, expected_revision: 1 },
    "idempotency_key",
  );
  rejects(
    parseControlCallRequest,
    { action: "hangup", idempotency_key: key },
    "action",
  );
  rejects(
    parseApproveResponseRequest,
    { context_revision: 1, edited_text: "", idempotency_key: key },
    "edited_text",
  );
  rejects(
    parseApproveResponseRequest,
    { context_revision: 1, response_id: "x", idempotency_key: key },
    "request.response_id",
  );
  rejects(parseStartCallRequest, {}, "idempotency_key");
  assert.deepEqual(parseListCallsQuery({ limit: 100 }), {
    cursor: undefined,
    limit: 100,
  });
  rejects(parseListCallsQuery, { limit: 101 }, "limit");
  rejects(parseListCallsQuery, { limit: 0 }, "limit");
});

test("paused is a connected substate that hides the active suggestion", () => {
  assert.ok(CALL_STATUSES.includes("paused"));
  const call = { ...fx.stale_approval.call, context_revision: 1 };
  let state = hydrateSnapshot(createEventState(call.id), {
    call,
    last_seq: 6,
    turns: [],
    instructions: [],
    responses: [],
  });
  state = reduceEvent(state, fx.event_envelope);
  assert.ok(state.currentResponseId);
  const update = (seq, status) => ({
    ...fx.event_envelope,
    id: `00000000-0000-4000-8000-${String(seq).padStart(12, "0")}`,
    seq,
    type: "call.updated",
    payload: { ...call, status },
  });
  state = reduceEvent(state, update(8, "paused"));
  assert.equal(state.call.status, "paused");
  assert.equal(state.currentResponseId, null);
  state = reduceEvent(state, update(9, "connected"));
  assert.equal(state.call.status, "connected");
  state = reduceEvent(state, update(10, "ended"));
  // End is terminal; a late resume cannot reopen it.
  state = reduceEvent(state, update(11, "connected"));
  assert.equal(state.call.status, "ended");
});

test("provider errors name only a known provider kind", () => {
  assert.equal(
    parseProviderError({ provider: "tts", code: "timeout", retryable: true })
      .provider,
    "tts",
  );
  rejects(
    parseProviderError,
    { provider: "elevenlabs", code: "timeout", retryable: true },
    "provider_error.provider",
  );
});
