import assert from "node:assert/strict";
import { test } from "node:test";
import {
  businessTag,
  callTitle,
  describeGoal,
  formatPhone,
  statusBadge,
} from "../src/screens/live-call/goal-text.ts";

const goal = {
  kind: "restaurant",
  preferred_at: "2026-11-13T19:00:00-06:00",
  timezone: "America/Chicago",
  party_size: 4,
  constraints: [],
  contact_name: "Demo Guest",
};

test("the goal reads in the business's own clock, not as a raw timestamp", () => {
  assert.deepEqual(describeGoal(goal), {
    what: "Table for 4",
    when: "Fri, Nov 13, around 7:00 PM",
  });
  const appointment = {
    ...goal,
    kind: "appointment",
    party_size: undefined,
    service: "Haircut",
    preferred_at: "2026-11-16T09:30:00-05:00",
  };
  assert.deepEqual(describeGoal(appointment), {
    what: "Haircut",
    when: "Mon, Nov 16, around 9:30 AM",
  });
  assert.equal(businessTag(appointment), "BUSINESS");
});

test("titles, tags and status badges match the reference wording", () => {
  assert.equal(
    callTitle({ goal, business_name: "Marigold" }),
    "Reservation at Marigold",
  );
  assert.equal(businessTag(goal), "RESTAURANT");
  assert.deepEqual(statusBadge("connected"), {
    label: "LIVE",
    tone: "success",
  });
  assert.equal(statusBadge("paused").label, "PAUSED");
  assert.equal(formatPhone("+13125550142"), "+1 312 555 0142");
});
