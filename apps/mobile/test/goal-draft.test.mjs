import assert from "node:assert/strict";
import { test } from "node:test";
import {
  initialDraft,
  localTimeChoices,
  validateDraft,
} from "../src/screens/goal-draft.ts";

const valid = () => ({
  ...initialDraft,
  business: "Bayou Lantern Bistro (mock)",
  date: "2026-11-20",
  contactName: "Jordan Lee",
  acknowledged: true,
});

test("restaurant and appointment reviews use frozen contract and exclude opposite-kind detail", () => {
  const restaurant = validateDraft(valid());
  assert.deepEqual(restaurant.errors, {});
  assert.equal(restaurant.review.mode, "mock");
  assert.equal(restaurant.review.approval_mode, "always");
  assert.equal(restaurant.review.goal.party_size, 2);
  assert.equal(
    restaurant.review.goal.preferred_at,
    "2026-11-20T19:00:00-06:00",
  );
  assert.equal(restaurant.review.goal.service, undefined);
  const appointment = validateDraft({
    ...valid(),
    kind: "appointment",
    service: "Haircut",
    partySize: "bad-but-inactive",
    constraints: "No dye\n\nAfter 5 PM",
  });
  assert.equal(appointment.review.goal.service, "Haircut");
  assert.equal(appointment.review.goal.party_size, undefined);
  assert.deepEqual(appointment.review.goal.constraints, [
    "No dye",
    "After 5 PM",
  ]);
  assert.equal(appointment.review.goal.contact_phone, undefined);
});

test("local times preserve named zone and handle DST gaps and repeated hours explicitly", () => {
  assert.deepEqual(
    localTimeChoices("2026-03-08", "02:30", "America/Chicago"),
    [],
  );
  const repeated = localTimeChoices("2026-11-01", "01:30", "America/Chicago");
  assert.deepEqual(
    repeated.map((c) => c.offset),
    ["-05:00", "-06:00"],
  );
  assert.ok(
    validateDraft({ ...valid(), date: "2026-11-01", time: "01:30" }).errors
      .offset,
  );
  const chosen = validateDraft({
    ...valid(),
    date: "2026-11-01",
    time: "01:30",
    offset: "-06:00",
  });
  assert.equal(chosen.review.goal.preferred_at, "2026-11-01T01:30:00-06:00");
  assert.equal(
    localTimeChoices("2026-11-20", "19:00", "Asia/Kathmandu")[0].offset,
    "+05:45",
  );
  assert.deepEqual(localTimeChoices("2026-02-30", "19:00", "UTC"), []);
  assert.deepEqual(localTimeChoices("2026-11-20", "24:01", "UTC"), []);
});

test("invalid or unsafe drafts cannot reach review and errors name the field", () => {
  for (const [change, field] of [
    [{ business: " " }, "business"],
    [{ destination: "+12255551234" }, "destination"],
    [{ contactName: "" }, "contactName"],
    [{ partySize: "0" }, "partySize"],
    [{ partySize: "1.5" }, "partySize"],
    [{ kind: "appointment", service: "" }, "service"],
    [{ contactPhone: "555-1234" }, "contactPhone"],
    [{ constraints: Array(13).fill("preference").join("\n") }, "constraints"],
    [{ constraints: "😀".repeat(101) }, "constraints"],
    [{ timezone: "Mars/Olympus" }, "timezone"],
    [{ timezone: "+05:00" }, "timezone"],
    [{ date: "" }, "date"],
    [{ acknowledged: false }, "acknowledged"],
  ]) {
    const result = validateDraft({ ...valid(), ...change });
    assert.ok(result.errors[field], field);
    assert.equal(result.review, undefined, field);
  }
});
