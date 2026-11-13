import assert from "node:assert/strict";
import { afterEach, test } from "node:test";
import { CallRequestError, createCall, getCall } from "../src/index.ts";

const original = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = original;
});
const id = "00000000-0000-4000-8000-000000000010",
  owner = "00000000-0000-4000-8000-000000000020";
const request = () => ({
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
  approval_mode: "always",
  consent_acknowledged: true,
  idempotency_key: "00000000-0000-4000-8000-000000000001",
});
const call = () => ({
  id,
  owner_id: owner,
  ...request(),
  status: "draft",
  context_revision: 0,
  created_at: "2026-11-13T12:00:00Z",
  updated_at: "2026-11-13T12:00:00Z",
});
const json = (body, status) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

test("concurrent stale 401 responses share session recovery without rotating the new cookie", async () => {
  let ready = false,
    sessions = 0;
  globalThis.fetch = async (url) => {
    if (String(url).endsWith("/v1/session")) {
      sessions++;
      ready = true;
      return json({ account_id: owner }, 201);
    }
    return ready
      ? json(call(), 201)
      : json({ error: { code: "unauthenticated", message: "Required" } }, 401);
  };
  await Promise.all([
    createCall("http://concurrent-session.test", request()),
    createCall("http://concurrent-session.test", request()),
  ]);
  assert.equal(sessions, 1);
});
test("create uses credentials and exact reviewed body, never start", async () => {
  const seen = [];
  globalThis.fetch = async (url, init) => {
    seen.push({ url: String(url), ...init });
    return json(call(), 201);
  };
  const result = await createCall("http://localhost:8080", request());
  assert.equal(result.id, id);
  assert.equal(seen.length, 1);
  assert.equal(seen[0].credentials, "include");
  assert.equal(seen[0].redirect, "error");
  assert.equal(seen[0].url, "http://localhost:8080/v1/calls");
  assert.deepEqual(JSON.parse(seen[0].body), request());
});
test("session recovery only after 401, retries same key, preserves an existing session", async () => {
  const seen = [];
  let ready = false;
  globalThis.fetch = async (url, init) => {
    seen.push(String(url));
    if (String(url).endsWith("/v1/session")) {
      ready = true;
      return json({ account_id: owner }, 201);
    }
    assert.equal(
      JSON.parse(init.body).idempotency_key,
      request().idempotency_key,
    );
    return ready
      ? json(call(), 201)
      : json({ error: { code: "unauthenticated", message: "Required" } }, 401);
  };
  await createCall("http://session.test", request());
  await createCall("http://session.test", request());
  assert.equal(seen.filter((x) => x.endsWith("/v1/session")).length, 1);
  assert.equal(
    seen.some((x) => x.endsWith("/start")),
    false,
  );
});
test("safe server errors do not expose arbitrary bodies or retry ambiguous mutations", async () => {
  let hits = 0;
  globalThis.fetch = async () => {
    hits++;
    return json(
      {
        error: {
          code: "provider_unavailable",
          message: "private-vendor-debug-token",
        },
      },
      503,
    );
  };
  await assert.rejects(
    createCall("http://fail.test", request()),
    (e) =>
      e instanceof CallRequestError &&
      !e.message.includes("private") &&
      e.status === 503,
  );
  assert.equal(hits, 1);
  globalThis.fetch = async () => {
    throw new Error("private-network-detail");
  };
  await assert.rejects(
    createCall("http://fail.test", request()),
    (e) => e instanceof CallRequestError && !e.message.includes("private"),
  );
});
test("get validates identity and malformed success cannot become a saved call", async () => {
  globalThis.fetch = async () => json({ ...call(), id: owner }, 200);
  await assert.rejects(getCall("http://read.test", id), CallRequestError);
  globalThis.fetch = async () => json({ ...call(), status: "connected" }, 201);
  await assert.rejects(
    createCall("http://read.test", request()),
    CallRequestError,
  );
  globalThis.fetch = async () =>
    new Response("<html>private</html>", { status: 500 });
  await assert.rejects(
    getCall("http://read.test", id),
    (e) => e instanceof CallRequestError && !e.message.includes("private"),
  );
});
