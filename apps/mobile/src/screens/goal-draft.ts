import { type Goal, parseCreateCallRequest } from "@switchboard/api";

export interface GoalDraft {
  kind: "restaurant" | "appointment";
  business: string;
  destination: string;
  date: string;
  time: string;
  timezone: string;
  offset: string;
  partySize: string;
  service: string;
  constraints: string;
  contactName: string;
  contactPhone: string;
  acknowledged: boolean;
}

export const initialDraft: GoalDraft = {
  kind: "restaurant",
  business: "",
  destination: "+12255550110",
  date: "",
  time: "19:00",
  timezone: "America/Chicago",
  offset: "",
  partySize: "2",
  service: "",
  constraints: "",
  contactName: "",
  contactPhone: "",
  acknowledged: false,
};

export interface ReviewGoal {
  business_name: string;
  destination: string;
  mode: "mock";
  approval_mode: "always";
  goal: Goal;
}

interface LocalTime {
  offset: string;
  preferredAt: string;
}

// Find real instants that match the business's wall clock. Never silently move
// a nonexistent time forward or pick one occurrence of an ambiguous DST hour.
export function localTimeChoices(
  date: string,
  time: string,
  zone: string,
): LocalTime[] {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date) || !/^\d{2}:\d{2}$/.test(time))
    return [];
  const [year = 0, month = 0, day = 0] = date.split("-").map(Number);
  const [hour = 0, minute = 0] = time.split(":").map(Number);
  if (
    year < 1000 ||
    month < 1 ||
    month > 12 ||
    day < 1 ||
    day > new Date(Date.UTC(year, month, 0)).getUTCDate() ||
    hour > 23 ||
    minute > 59
  )
    return [];
  const formatter = new Intl.DateTimeFormat("en-US", {
    timeZone: zone,
    calendar: "iso8601",
    numberingSystem: "latn",
    hourCycle: "h23",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
  const wall = (instant: number) => {
    const parts = Object.fromEntries(
      formatter
        .formatToParts(instant)
        .filter((p) => p.type !== "literal")
        .map((p) => [p.type, Number(p.value)]),
    );
    return Date.UTC(
      parts.year ?? 0,
      (parts.month ?? 0) - 1,
      parts.day ?? 0,
      parts.hour ?? 0,
      parts.minute ?? 0,
      parts.second ?? 0,
    );
  };
  const target = Date.UTC(year, month - 1, day, hour, minute);
  const offsets = new Set<number>();
  for (let hours = -36; hours <= 36; hours += 6) {
    const sample = target + hours * 3_600_000;
    offsets.add((wall(sample) - sample) / 60_000);
  }
  return [...offsets]
    .filter(
      (offset) =>
        Number.isInteger(offset) && wall(target - offset * 60_000) === target,
    )
    .sort((a, b) => b - a)
    .map((value) => {
      const abs = Math.abs(value);
      const offset = `${value < 0 ? "-" : "+"}${String(Math.floor(abs / 60)).padStart(2, "0")}:${String(abs % 60).padStart(2, "0")}`;
      return { offset, preferredAt: `${date}T${time}:00${offset}` };
    });
}

export function validateDraft(draft: GoalDraft): {
  errors: Partial<Record<keyof GoalDraft, string>>;
  review?: ReviewGoal;
  choices: LocalTime[];
} {
  const errors: Partial<Record<keyof GoalDraft, string>> = {};
  const bounded = (s: string, max: number) =>
    s.trim().length > 0 && s.trim().length <= max;
  if (!bounded(draft.business, 120))
    errors.business = "Enter a business name, up to 120 characters.";
  if (!/^\+1[2-9]\d{2}55501\d{2}$/.test(draft.destination.trim()))
    errors.destination = "Use a fictional demo number such as +12255550110.";
  if (!bounded(draft.contactName, 120))
    errors.contactName =
      "Enter the name for the booking, up to 120 characters.";
  if (
    draft.contactPhone.trim() &&
    !/^\+[1-9]\d{1,14}$/.test(draft.contactPhone.trim())
  )
    errors.contactPhone = "Include the country code, for example +12255550111.";
  if (
    draft.kind === "restaurant" &&
    (!/^\d+$/.test(draft.partySize) ||
      Number(draft.partySize) < 1 ||
      Number(draft.partySize) > 20)
  )
    errors.partySize = "Choose a party size from 1 to 20.";
  if (draft.kind === "appointment" && !bounded(draft.service, 160))
    errors.service = "Describe the service, up to 160 characters.";
  const constraints = draft.constraints
    .split("\n")
    .map((s) => s.trim())
    .filter(Boolean);
  if (constraints.length > 12 || constraints.some((s) => s.length > 200))
    errors.constraints =
      "Use up to 12 preferences, one per line, with up to 200 characters each.";
  let choices: LocalTime[] = [];
  try {
    new Intl.DateTimeFormat("en", { timeZone: draft.timezone });
    if (
      draft.timezone.length > 100 ||
      (draft.timezone !== "UTC" &&
        !/^[A-Za-z0-9_+-]+(?:\/[A-Za-z0-9_+-]+)+$/.test(draft.timezone))
    )
      throw new Error("zone");
    choices = localTimeChoices(draft.date, draft.time, draft.timezone);
    if (!choices.length)
      errors.date =
        "Enter a valid date (YYYY-MM-DD) and time (HH:MM). This local time must exist in the selected time zone.";
    if (choices.length > 1 && !choices.some((c) => c.offset === draft.offset))
      errors.offset =
        "This local time occurs twice. Choose which UTC offset you mean.";
  } catch {
    errors.timezone =
      "Enter a recognized time zone, for example America/Chicago.";
  }
  if (!draft.acknowledged)
    errors.acknowledged =
      "Acknowledge the fictional simulation before reviewing.";
  if (Object.keys(errors).length) return { errors, choices };
  const choice =
    choices.length === 1
      ? choices[0]
      : choices.find((c) => c.offset === draft.offset);
  if (!choice)
    return { errors: { date: "Choose a valid local time." }, choices };
  const goal = {
    kind: draft.kind,
    preferred_at: choice.preferredAt,
    timezone: draft.timezone,
    ...(draft.kind === "restaurant"
      ? { party_size: Number(draft.partySize) }
      : { service: draft.service.trim() }),
    constraints,
    contact_name: draft.contactName.trim(),
    ...(draft.contactPhone.trim()
      ? { contact_phone: draft.contactPhone.trim() }
      : {}),
  };
  // Reuse the frozen contract. This key is only a validation placeholder; #18
  // generates a fresh idempotency key when a real create action is implemented.
  const parsed = parseCreateCallRequest({
    mode: "mock",
    destination: draft.destination.trim(),
    business_name: draft.business.trim(),
    goal,
    approval_mode: "always",
    consent_acknowledged: true,
    idempotency_key: "00000000-0000-4000-8000-000000000001",
  });
  return {
    errors,
    choices,
    review: {
      mode: "mock",
      approval_mode: "always",
      destination: parsed.destination,
      business_name: parsed.business_name,
      goal: parsed.goal,
    },
  };
}
