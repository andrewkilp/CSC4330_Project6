// Fixed offline verification commands. No caller-supplied command is executed.
const { spawnSync } = require("node:child_process");
const path = require("node:path");

function offlineEnv(source) {
  const env = {};
  for (const [key, value] of Object.entries(source)) {
    // Case-insensitive for Windows, including duplicate mixed-case settings.
    if (
      /^(ANTHROPIC|CLAUDE|TWILIO|ELEVENLABS|DEEPGRAM|OPENAI|AWS|AZURE|GOOGLE|GITHUB|GH)_/i.test(
        key,
      ) ||
      /(?:^|_)(API_KEY|TOKEN|SECRET|PASSWORD)(?:_|$)/i.test(key) ||
      /^(CALL_MODE|ENABLE_REAL_CALLS|CALL_ALLOWED_NUMBERS|PUBLIC_BASE_URL|EXPO_NO_DOTENV|EXPO_OFFLINE|NODE_OPTIONS|NODE_PATH|GOFLAGS|GOENV|GOTOOLCHAIN)$/i.test(
        key,
      )
    )
      continue;
    env[key] = value;
  }
  return {
    ...env,
    CALL_MODE: "mock",
    ENABLE_REAL_CALLS: "false",
    CALL_ALLOWED_NUMBERS: "",
    PUBLIC_BASE_URL: "",
    EXPO_NO_DOTENV: "1",
    EXPO_OFFLINE: "1",
    GOENV: "off",
    GOTOOLCHAIN: "local",
  };
}

function main(args) {
  if (args.length > 1 || (args.length === 1 && args[0] !== "--ci")) {
    throw new Error("Usage: node scripts/verify-fixtures/run.cjs [--ci]");
  }
  const root = path.resolve(__dirname, "../..");
  const steps = [
    ["node", ["--test", "scripts/verify-fixtures/run.test.cjs"]],
    [
      "go",
      [
        "test",
        "scripts/verify-fixtures/main.go",
        "scripts/verify-fixtures/main_test.go",
      ],
    ],
    ["go", ["run", "scripts/verify-fixtures/main.go"]],
  ];
  if (args[0] === "--ci") {
    steps.push(["moon", ["run", ":build", ":lint", ":typecheck", ":test"]]);
  }
  console.log(
    "Verifying fictional mocks with real calls disabled and provider credentials removed.",
  );
  for (const [command, argv] of steps) {
    console.log(`Running: ${command} ${argv.join(" ")}`);
    const result = spawnSync(command, argv, {
      cwd: root,
      env: offlineEnv(process.env),
      stdio: "inherit",
      shell: false,
      timeout: 600_000,
    });
    if (result.error) throw result.error;
    if (result.status !== 0)
      throw new Error(`${command} failed (${result.status ?? result.signal})`);
  }
}

module.exports = { offlineEnv };
if (require.main === module) {
  try {
    main(process.argv.slice(2));
  } catch (err) {
    console.error(`offline verification: ${err.message}`);
    process.exitCode = 1;
  }
}
