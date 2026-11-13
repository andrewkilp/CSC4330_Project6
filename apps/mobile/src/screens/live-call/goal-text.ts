// Readable goal and call labels for the live screens (#47). Pure, so Node tests run it.
import type { Call, CallStatus, Goal } from "@switchboard/api";

const WEEKDAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const MONTHS = [
  "Jan",
  "Feb",
  "Mar",
  "Apr",
  "May",
  "Jun",
  "Jul",
  "Aug",
  "Sep",
  "Oct",
  "Nov",
  "Dec",
];

// Reads the business's wall-clock time straight from preferred_at, which carries the
// business offset, so the device time zone never shifts it.
export function describeGoal(goal: Goal) {
  const [date = "", time = ""] = goal.preferred_at.split("T");
  const [year = 0, month = 1, day = 1] = date.split("-").map(Number);
  const [hour = 0, minute = 0] = time.slice(0, 5).split(":").map(Number);
  const weekday =
    WEEKDAYS[new Date(Date.UTC(year, month - 1, day)).getUTCDay()];
  const clock = `${((hour + 11) % 12) + 1}:${String(minute).padStart(2, "0")} ${hour < 12 ? "AM" : "PM"}`;
  return {
    what:
      goal.kind === "restaurant"
        ? `Table for ${goal.party_size ?? "?"}`
        : (goal.service ?? "Appointment"),
    when: `${weekday}, ${MONTHS[month - 1]} ${day}, around ${clock}`,
  };
}

export function callTitle(call: Pick<Call, "goal" | "business_name">) {
  return `${call.goal.kind === "restaurant" ? "Reservation" : "Appointment"} at ${call.business_name}`;
}

export function kindLabel(goal: Goal) {
  return goal.kind === "restaurant" ? "RESTAURANT RESERVATION" : "APPOINTMENT";
}

// The mockup's short label for the business side of the transcript.
export function businessTag(goal: Goal) {
  return goal.kind === "restaurant" ? "RESTAURANT" : "BUSINESS";
}

const STATUS: Record<
  CallStatus,
  { label: string; tone: "neutral" | "success" | "warning" | "danger" }
> = {
  draft: { label: "DRAFT", tone: "neutral" },
  dialing: { label: "CONNECTING", tone: "warning" },
  connected: { label: "LIVE", tone: "success" },
  paused: { label: "PAUSED", tone: "warning" },
  ended: { label: "ENDED", tone: "neutral" },
  failed: { label: "FAILED", tone: "danger" },
  cancelled: { label: "CANCELLED", tone: "neutral" },
};
export function statusBadge(status: CallStatus) {
  return STATUS[status];
}

// +13125550142 -> +1 312 555 0142
export function formatPhone(e164: string) {
  const m = /^\+1(\d{3})(\d{3})(\d{4})$/.exec(e164);
  return m ? `+1 ${m[1]} ${m[2]} ${m[3]}` : e164;
}
