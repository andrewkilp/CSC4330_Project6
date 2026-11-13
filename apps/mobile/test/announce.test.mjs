import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { createEventState, reduceEvent } from "@switchboard/api";
import {
  announcementsBetween,
  announceSnapshot,
} from "../src/screens/live-call/announce.ts";

const fixtures = JSON.parse(
  readFileSync(
    new URL(
      "../../../packages/ts/api/test/fixtures/events.json",
      import.meta.url,
    ),
    "utf8",
  ),
);
const callId = fixtures.call.id;
const event = (
  seq,
  type,
  payload,
  revision = payload.context_revision ?? 0,
) => ({
  id: `00000000-0000-4000-8000-${String(seq).padStart(12, "0")}`,
  call_id: callId,
  seq,
  type,
  occurred_at: "2026-11-05T18:00:03Z",
  context_revision: revision,
  payload,
});

test("screen readers hear status, finished business turns and approval, never partials", () => {
  const steps = [
    event(1, "call.updated", { ...fixtures.call, status: "dialing" }),
    event(2, "call.updated", fixtures.call),
    event(3, "turn.updated", fixtures.partial_turn),
    event(4, "turn.updated", fixtures.final_turn),
    event(5, "response.updated", fixtures.response),
    event(6, "call.updated", { ...fixtures.call, status: "paused" }),
  ];
  let state = createEventState(callId);
  let before = null;
  const heard = [];
  for (const step of steps) {
    state = reduceEvent(state, step);
    const after = announceSnapshot(state, "live");
    heard.push(...announcementsBetween(before, after, state));
    before = after;
  }
  assert.deepEqual(heard, [
    "Connected. The AI is on the line.",
    "Example Restaurant said: Would seven thirty work?",
    "A reply is waiting for your approval.",
    "AI paused. Nothing will be said until you resume.",
  ]);
  assert.ok(!heard.some((message) => message.endsWith("Would seven")));
  assert.deepEqual(announcementsBetween(before, before, state), []);
  assert.deepEqual(
    announcementsBetween(
      before,
      announceSnapshot(state, "reconnecting"),
      state,
    ),
    ["Connection interrupted. Reconnecting."],
  );
});
