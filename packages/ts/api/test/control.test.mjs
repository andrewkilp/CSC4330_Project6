import assert from "node:assert/strict";
import { afterEach, test } from "node:test";
import {
  allowedActions,
  controlCall,
  controlProblem,
  isAcknowledged,
} from "../src/index.ts";

const original = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = original;
});
const callId = "00000000-0000-4000-8000-000000000010";
const key = "00000000-0000-4000-8000-000000000077";
const call = (status) => ({
  id: callId,
  owner_id: "00000000-0000-4000-8000-000000000020",
  mode: "mock",
  destination: "+12255550110",
  business_name: "Bistro (mock)",
  goal: {
    kind: "restaurant",
    preferred_at: "2026-11-20T19:00:00-06:00",
    timezone: "America/Chicago",
    party_size: 4,
    constraints: [],
    contact_name: "Jordan Lee",
  },
  status,
  approval_mode: "always",
  context_revision: 0,
  created_at: "2026-11-13T12:00:00Z",
  updated_at: "2026-11-13T12:00:00Z",
});
const json = (body, status) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

test("controls follow the documented state machine", () => {
  assert.deepEqual(allowedActions("connected"), ["pause", "end"]);
  assert.deepEqual(allowedActions("paused"), ["resume", "end"]);
  assert.deepEqual(allowedActions("dialing"), ["end"]);
  for (const done of ["ended", "failed", "cancelled"])
    assert.deepEqual(allowedActions(done), []);
  assert.equal(isAcknowledged("pause", "connected"), false);
  assert.equal(isAcknowledged("pause", "paused"), true);
  assert.equal(isAcknowledged("end", "failed"), true);
});

test("a control request carries its action and idempotency key", async () => {
  const seen = [];
  globalThis.fetch = async (url, init) => {
    seen.push({ url: String(url), init });
    return json(call("paused"), 200);
  };
  const result = await controlCall("http://api.test", callId, "pause", key);
  assert.equal(result.status, "paused");
  assert.equal(seen[0].url, `http://api.test/v1/calls/${callId}/control`);
  assert.equal(seen[0].init.credentials, "include");
  assert.deepEqual(JSON.parse(seen[0].init.body), {
    action: "pause",
    idempotency_key: key,
  });
});

test("a lost answer is retried with the same key; the server applies it once", async () => {
  let applied = 0;
  const seenKeys = [];
  let drop = true;
  globalThis.fetch = async (_url, init) => {
    const { idempotency_key } = JSON.parse(init.body);
    if (!seenKeys.includes(idempotency_key)) applied += 1;
    seenKeys.push(idempotency_key);
    if (drop) {
      drop = false;
      throw new TypeError("connection reset");
    }
    return json(call("paused"), 200);
  };
  const first = await controlCall(
    "http://api.test",
    callId,
    "pause",
    key,
  ).catch((e) => e);
  assert.equal(controlProblem("pause", first).kind, "retry");
  const retried = await controlCall("http://api.test", callId, "pause", key);
  assert.equal(retried.status, "paused");
  assert.equal(applied, 1);
});

test("failures say the call is unchanged; a disallowed or missing route is not retried", async () => {
  const send = () =>
    controlCall("http://api.test", callId, "resume", key).catch((e) => e);
  globalThis.fetch = async () => {
    throw new TypeError("offline");
  };
  assert.deepEqual(controlProblem("resume", await send()), {
    kind: "retry",
    message: "Resume did not go through. The call is unchanged. Try again.",
  });
  globalThis.fetch = async () =>
    json(
      {
        error: {
          code: "conflict",
          message: "Resume is not allowed after end.",
          field: "action",
        },
      },
      409,
    );
  assert.equal(controlProblem("resume", await send()).kind, "moot");
  // A server without the #31 route answers a plain-text 404.
  globalThis.fetch = async () =>
    new Response("404 page not found", { status: 404 });
  assert.equal(controlProblem("resume", await send()).kind, "blocked");
});
