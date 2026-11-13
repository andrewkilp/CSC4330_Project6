import assert from "node:assert/strict";
import { afterEach, test } from "node:test";
import {
  instructionProblem,
  nextInstructionAttempt,
  sendInstruction,
} from "../src/index.ts";

const original = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = original;
});
const callId = "00000000-0000-4000-8000-000000000010";
let keys = 0;
const key = () => `00000000-0000-4000-8000-${String(++keys).padStart(12, "0")}`;
const json = (body, status) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
const pending = {
  id: "00000000-0000-4000-8000-000000000055",
  call_id: callId,
  text: "Accept 7:30, but ask for the patio.",
  context_revision: 0,
  status: "pending",
  created_at: "2026-11-13T12:00:02Z",
};

test("a retry of the same text and revision reuses its idempotency key", () => {
  const first = nextInstructionAttempt(null, "Ask for the patio.", 0, key);
  assert.equal(
    nextInstructionAttempt(first, "Ask for the patio.", 0, key),
    first,
  );
  const edited = nextInstructionAttempt(first, "Ask for indoors.", 0, key);
  const rebased = nextInstructionAttempt(first, "Ask for the patio.", 1, key);
  assert.notEqual(edited.idempotencyKey, first.idempotencyKey);
  assert.notEqual(rebased.idempotencyKey, first.idempotencyKey);
  assert.equal(rebased.expectedRevision, 1);
});

test("an instruction is posted with its revision, key and the session cookie", async () => {
  const seen = [];
  globalThis.fetch = async (url, init) => {
    seen.push({ url: String(url), init });
    return json(pending, 201);
  };
  const attempt = nextInstructionAttempt(null, pending.text, 0, key);
  const result = await sendInstruction("http://api.test", callId, attempt);
  assert.equal(result.status, "pending");
  assert.equal(seen[0].url, `http://api.test/v1/calls/${callId}/instructions`);
  assert.equal(seen[0].init.method, "POST");
  assert.equal(seen[0].init.credentials, "include");
  assert.deepEqual(JSON.parse(seen[0].init.body), {
    text: pending.text,
    expected_revision: 0,
    idempotency_key: attempt.idempotencyKey,
  });
});

test("a stale revision is recoverable and keeps the typed text", async () => {
  globalThis.fetch = async () =>
    json(
      {
        error: {
          code: "conflict",
          message: "The call context changed.",
          field: "expected_revision",
        },
      },
      409,
    );
  const error = await sendInstruction(
    "http://api.test",
    callId,
    nextInstructionAttempt(null, "Ask for the patio.", 0, key),
  ).catch((e) => e);
  assert.equal(error.field, "expected_revision");
  assert.equal(instructionProblem(error).kind, "stale");
});

test("failures say what to do and never echo the server's body", async () => {
  const send = () =>
    sendInstruction(
      "http://api.test",
      callId,
      nextInstructionAttempt(null, "Ask for the patio.", 0, key),
    ).catch((e) => e);
  globalThis.fetch = async () => {
    throw new TypeError("offline");
  };
  assert.equal(instructionProblem(await send()).kind, "retry");
  // A server without the #28 route answers a plain-text 404.
  globalThis.fetch = async () =>
    new Response("404 page not found", { status: 404 });
  const missing = await send();
  assert.equal(missing.code, "not_found");
  assert.deepEqual(instructionProblem(missing), {
    kind: "blocked",
    message: "This call cannot take instructions on this server yet.",
  });
  globalThis.fetch = async () =>
    json({ error: { code: "unauthenticated", message: "secret detail" } }, 401);
  assert.ok(!instructionProblem(await send()).message.includes("secret"));
  // An instruction for another call is not accepted as this call's answer.
  globalThis.fetch = async () =>
    json({ ...pending, call_id: "00000000-0000-4000-8000-000000000099" }, 201);
  assert.equal((await send()).code, "internal");
});

test("text over the limit is refused before any request", async () => {
  let called = false;
  globalThis.fetch = async () => {
    called = true;
    return json(pending, 201);
  };
  const error = await sendInstruction(
    "http://api.test",
    callId,
    nextInstructionAttempt(null, "x".repeat(1001), 0, key),
  ).catch((e) => e);
  assert.equal(called, false);
  assert.equal(instructionProblem(error).kind, "blocked");
});
