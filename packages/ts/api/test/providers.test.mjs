import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import {
  ContractValidationError,
  canStartRealCall,
  getProviders,
  parseProviders,
  realCallBlockers,
} from "../src/index.ts";

// Shared with packages/go/handlers/providers/readiness_test.go.
const fx = JSON.parse(
  readFileSync(new URL("./fixtures/providers.json", import.meta.url), "utf8"),
);

test("every server fixture parses to itself and leaks no secrets", () => {
  for (const c of fx.cases) {
    const parsed = parseProviders(structuredClone(c.expected));
    assert.deepEqual(parsed, c.expected, c.name);
    const wire = JSON.stringify(parsed);
    for (const secret of fx.secret_sentinels)
      assert.ok(!wire.includes(secret), c.name);
    assert.equal(canStartRealCall(parsed), c.expected.mode === "real", c.name);
  }
});

test("missing keys disable real calling with reasons in provider order", () => {
  const missing = parseProviders(fx.cases[1].expected);
  assert.equal(canStartRealCall(missing), false);
  assert.deepEqual(
    realCallBlockers(missing).map((b) => b.provider),
    ["telephony", "stt", "tts"],
  );
  const outage = parseProviders(fx.cases[3].expected);
  assert.deepEqual(realCallBlockers(outage), [
    {
      provider: "stt",
      reason: "Speech recognition is temporarily unavailable. Try again later.",
    },
  ]);
  assert.deepEqual(realCallBlockers(parseProviders(fx.cases[4].expected)), []);
});

test("malformed provider status is rejected with the field name only", () => {
  const ready = fx.cases[4].expected;
  const cases = [
    [{ ...ready, mode: "managed" }, "providers.mode"],
    [{ ...ready, stt: { available: "yes" } }, "providers.stt.available"],
    [{ ...ready, llm: { available: false } }, "providers.llm.reason"],
    [
      { ...ready, tts: { available: true, reason: "ok" } },
      "providers.tts.reason",
    ],
    [
      { ...ready, telephony: { available: false, reason: "x".repeat(201) } },
      "providers.telephony.reason",
    ],
    // Real mode with an unavailable provider is contradictory.
    [{ ...fx.cases[3].expected, mode: "real" }, "providers.mode"],
    [{ ...ready, stt: undefined }, "providers.stt"],
  ];
  for (const [value, field] of cases)
    assert.throws(
      () => parseProviders(value),
      (e) => e instanceof ContractValidationError && e.field === field,
      field,
    );
});

test("getProviders fetches without credentials and maps failures to safe messages", async () => {
  const original = globalThis.fetch;
  try {
    let seen;
    globalThis.fetch = async (url, init) => {
      seen = { url: String(url), init };
      return new Response(JSON.stringify(fx.cases[0].expected), {
        status: 200,
      });
    };
    const status = await getProviders("http://localhost:8080");
    assert.equal(seen.url, "http://localhost:8080/v1/providers");
    assert.equal(seen.init.credentials, undefined);
    assert.equal(status.mode, "mock");

    globalThis.fetch = async () => new Response("{}", { status: 503 });
    await assert.rejects(getProviders("http://localhost:8080"), {
      message: "Provider status is unavailable. Try again.",
    });
    globalThis.fetch = async () =>
      new Response(JSON.stringify({ mode: "real" }), { status: 200 });
    await assert.rejects(getProviders("http://localhost:8080"), {
      message: "The server returned an unexpected response.",
    });
    globalThis.fetch = async () => {
      throw new TypeError("connect ECONNREFUSED 127.0.0.1:8080");
    };
    await assert.rejects(getProviders("http://localhost:8080"), {
      message: "The server is unavailable. Try again.",
    });
  } finally {
    globalThis.fetch = original;
  }
});
