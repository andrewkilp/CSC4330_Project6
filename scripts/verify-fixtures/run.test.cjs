const assert = require("node:assert/strict");
const { test } = require("node:test");
const { offlineEnv } = require("./run.cjs");

test("a real-call shell cannot pass credentials or enable dialing in a child", () => {
  const source = {
    PATH: "toolchain-path",
    HOME: "home",
    PROTO_HOME: "proto-cache",
    CALL_MODE: "real",
    call_mode: "real",
    Enable_Real_Calls: "true",
    CALL_ALLOWED_NUMBERS: "+12255550100",
    PUBLIC_BASE_URL: "https://example.invalid",
    TWILIO_AUTH_TOKEN: "test-only",
    anthropic_api_key: "test-only",
    ELEVENLABS_API_KEY: "test-only",
    DEEPGRAM_API_KEY: "test-only",
    GITHUB_TOKEN: "test-only",
    UNRELATED_API_KEY: "test-only",
    NODE_OPTIONS: "--require arbitrary.cjs",
    GOFLAGS: "-toolexec=arbitrary",
    GOENV: "custom-go-env",
    GOTOOLCHAIN: "auto",
    EXPO_NO_DOTENV: "0",
    EXPO_OFFLINE: "0",
  };
  const before = { ...source };
  const env = offlineEnv(source);
  assert.deepEqual(source, before);
  assert.equal(env.CALL_MODE, "mock");
  assert.equal(env.ENABLE_REAL_CALLS, "false");
  assert.equal(env.CALL_ALLOWED_NUMBERS, "");
  assert.equal(env.PUBLIC_BASE_URL, "");
  assert.equal(env.EXPO_NO_DOTENV, "1");
  assert.equal(env.EXPO_OFFLINE, "1");
  assert.equal(env.GOENV, "off");
  assert.equal(env.GOTOOLCHAIN, "local");
  for (const key of Object.keys(source)) {
    if (
      [
        "PATH",
        "HOME",
        "PROTO_HOME",
        "CALL_MODE",
        "CALL_ALLOWED_NUMBERS",
        "PUBLIC_BASE_URL",
        "EXPO_NO_DOTENV",
        "EXPO_OFFLINE",
        "GOENV",
        "GOTOOLCHAIN",
      ].includes(key)
    )
      continue;
    assert.equal(Object.hasOwn(env, key), false, key);
  }
  assert.equal(env.PATH, source.PATH);
  assert.equal(env.PROTO_HOME, source.PROTO_HOME);
});

test("unsupported arguments cannot select a live command", () => {
  const { spawnSync } = require("node:child_process");
  const result = spawnSync(
    process.execPath,
    [require.resolve("./run.cjs"), "--real"],
    { encoding: "utf8" },
  );
  assert.equal(result.status, 1);
  assert.match(result.stderr, /Usage:/);
  assert.doesNotMatch(result.stdout, /Running:/);
});
