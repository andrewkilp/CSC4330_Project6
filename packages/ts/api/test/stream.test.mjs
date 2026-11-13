import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { CallEventDecoder, subscribeCall } from "../src/stream.ts";

const fixture = JSON.parse(
  readFileSync(new URL("./fixtures/events.json", import.meta.url), "utf8"),
);
const snapshot = {
  call: fixture.call,
  last_seq: 0,
  turns: [],
  instructions: [],
  responses: [],
};
function event(seq, payload = fixture.partial_turn) {
  return {
    id: `00000000-0000-4000-8000-${String(seq).padStart(12, "0")}`,
    call_id: fixture.call.id,
    seq,
    type: "turn.updated",
    occurred_at: "2026-11-13T18:00:00Z",
    context_revision: 0,
    payload,
  };
}
function frame(e) {
  return `id: ${e.seq}\r\nevent: ${e.type}\r\ndata: ${JSON.stringify(e)}\r\n\r\n`;
}
const json = (value) =>
  new Response(JSON.stringify(value), {
    headers: { "Content-Type": "application/json" },
  });
function sse(text, signal, close = false) {
  let cancelled = false;
  return new Response(
    new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode(text));
        if (close) controller.close();
        else
          signal.addEventListener(
            "abort",
            () => {
              if (!cancelled) {
                cancelled = true;
                controller.close();
              }
            },
            {
              once: true,
            },
          );
      },
      cancel() {
        cancelled = true;
      },
    }),
    { headers: { "Content-Type": "text/event-stream" } },
  );
}
async function until(fn) {
  const end = Date.now() + 2500;
  while (!fn()) {
    if (Date.now() > end) throw Error("test condition timed out");
    await new Promise((r) => setTimeout(r, 5));
  }
}
test("bounded SSE framing handles split UTF8 and CRLF, heartbeats and rejects ambiguous frames", () => {
  const e = event(1, { ...fixture.partial_turn, text: "Café 😊" }),
    bytes = new TextEncoder().encode(`: heartbeat\r\n\r\n${frame(e)}`),
    got = [],
    parser = new CallEventDecoder();
  for (const byte of bytes)
    parser.push(new Uint8Array([byte]), (v) => got.push(v));
  assert.equal(got.length, 1);
  assert.equal(got[0].payload.text, "Café 😊");
  for (const text of [
    frame(e).replace("id: 1", "id: 2"),
    frame(e).replace("event: turn.updated", "event: call.updated"),
    frame(e).replace("id: 1", "id: 1\nid: 1"),
    `data: ${"x".repeat(140000)}`,
  ])
    assert.throws(() =>
      new CallEventDecoder().push(new TextEncoder().encode(text), () => {}),
    );
});
test("credentials, explicit cursor, duplicate-free reconnect and close fencing", async () => {
  const requests = [],
    updates = [];
  let streams = 0,
    closed = 0;
  const client = subscribeCall({
    baseUrl: "https://api.example.invalid",
    callId: fixture.call.id,
    retryMs: 1,
    idleMs: 2000,
    onUpdate: (u) => updates.push(u),
    fetch: async (url, init) => {
      requests.push({ url: String(url), init });
      assert.equal(init.credentials, "include");
      assert.equal(init.redirect, "error");
      if (String(url).endsWith("snapshot")) return json(snapshot);
      streams++;
      if (streams === 1) return sse(frame(event(1)), init.signal, true);
      init.signal.addEventListener("abort", () => closed++);
      assert.equal(init.headers["Last-Event-ID"], "1");
      return sse(
        frame(event(1)) + frame(event(2, fixture.final_turn)),
        init.signal,
      );
    },
  });
  await until(() => client.getSnapshot().state.lastSeq === 2);
  assert.equal(client.getSnapshot().state.turnOrder.length, 1);
  assert.equal(
    client.getSnapshot().state.turns[fixture.final_turn.id].is_final,
    true,
  );
  client.close();
  const count = updates.length;
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(updates.length, count);
  assert.equal(closed, 1);
  assert.equal(streams, 2);
  assert.equal(requests.length, 3);
});
test("expired cursor and sequence gap recover through a consistent snapshot", async () => {
  for (const gap of [false, true]) {
    let snaps = 0,
      streams = 0;
    const client = subscribeCall({
      baseUrl: "https://api.example.invalid",
      callId: fixture.call.id,
      retryMs: 1,
      onUpdate: () => {},
      fetch: async (url, init) => {
        if (String(url).endsWith("snapshot")) {
          snaps++;
          return json(
            snaps === 1
              ? snapshot
              : { ...snapshot, last_seq: 3, turns: [fixture.final_turn] },
          );
        }
        streams++;
        if (streams === 1)
          return gap
            ? sse(frame(event(3)), init.signal)
            : new Response("", { status: 409 });
        assert.equal(init.headers["Last-Event-ID"], "3");
        return sse(": connected\n\n", init.signal);
      },
    });
    await until(
      () =>
        client.getSnapshot().state.lastSeq === 3 &&
        client.getSnapshot().status === "live",
    );
    assert.equal(snaps, 2);
    assert.equal(client.getSnapshot().state.recovery, null);
    client.close();
  }
});
test("unauthorized disconnect stops without creating sessions or retrying", async () => {
  let requests = 0;
  const client = subscribeCall({
    baseUrl: "https://api.example.invalid",
    callId: fixture.call.id,
    retryMs: 1,
    onUpdate: () => {},
    fetch: async (_url, init) => {
      requests++;
      if (requests === 1) return json(snapshot);
      if (requests === 2) return sse("", init.signal, true);
      return new Response("secret", { status: 401 });
    },
  });
  await until(() => client.getSnapshot().status === "unauthenticated");
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(requests, 3);
  client.close();
});
test("focus suspension cancels reader; resume refreshes snapshot and cleanup cancels retries", async () => {
  let requests = 0,
    aborts = 0;
  const client = subscribeCall({
    baseUrl: "https://api.example.invalid",
    callId: fixture.call.id,
    retryMs: 5,
    onUpdate: () => {},
    fetch: async (url, init) => {
      requests++;
      if (String(url).endsWith("snapshot")) return json(snapshot);
      init.signal.addEventListener("abort", () => aborts++);
      return sse("", init.signal);
    },
  });
  await until(() => client.getSnapshot().status === "live");
  client.setActive(false);
  assert.equal(client.getSnapshot().status, "offline");
  await until(() => aborts === 1);
  const n = requests;
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(requests, n);
  client.setActive(true);
  await until(() => requests === 4 && client.getSnapshot().status === "live");
  client.close();
  await until(() => aborts === 2);
});
test("malformed or cross-call stream never advances state and requires explicit recovery", async () => {
  for (const bad of [
    frame({ ...event(1), call_id: "99999999-9999-4999-8999-999999999999" }),
    frame(event(1)).replace('"seq":1', '"seq":"private"'),
  ]) {
    const client = subscribeCall({
      baseUrl: "https://api.example.invalid",
      callId: fixture.call.id,
      retryMs: 1,
      onUpdate: () => {},
      fetch: async (url, init) =>
        String(url).endsWith("snapshot")
          ? json(snapshot)
          : sse(bad, init.signal),
    });
    await until(() => client.getSnapshot().status === "error");
    assert.equal(client.getSnapshot().state.lastSeq, 0);
    client.close();
  }
});
