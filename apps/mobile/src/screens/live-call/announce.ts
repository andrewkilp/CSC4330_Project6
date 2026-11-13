// What a screen reader is told about the live call (#42). Pure, so the rules are tested
// in Node: call status changes, a reply that needs approval, each finished business turn,
// and stream problems are announced. Partial speech never is.
import type { CallStatus, EventState, StreamStatus } from "@switchboard/api";

export interface AnnounceSnapshot {
  readonly callStatus: CallStatus | null;
  readonly awaitingApproval: string | null;
  readonly finalBusinessTurns: readonly string[];
  readonly stream: StreamStatus;
}

const CALL_STATUS: Partial<Record<CallStatus, string>> = {
  dialing: "Dialing the business.",
  connected: "Connected. The AI is on the line.",
  paused: "AI paused. Nothing will be said until you resume.",
  ended: "The call has ended.",
  failed: "The call failed.",
  cancelled: "The call was cancelled.",
};

const STREAM_PROBLEM: Partial<Record<StreamStatus, string>> = {
  reconnecting: "Connection interrupted. Reconnecting.",
  resyncing:
    "Catching up on missed updates. Approval waits until this finishes.",
  unauthenticated: "Your session expired. Call updates stopped.",
  not_found: "This call was not found.",
  error: "Call updates could not be read.",
};

export function announceSnapshot(
  state: EventState,
  stream: StreamStatus,
): AnnounceSnapshot {
  const reply = state.currentResponseId
    ? state.responses[state.currentResponseId]
    : null;
  return {
    callStatus: state.call?.status ?? null,
    awaitingApproval:
      reply?.status === "suggested" && reply.needs_approval ? reply.id : null,
    finalBusinessTurns: state.turnOrder.filter((id) => {
      const turn = state.turns[id];
      return turn?.is_final && turn.speaker === "business";
    }),
    stream,
  };
}

// The first snapshot announces nothing; the screen itself is read on arrival.
export function announcementsBetween(
  before: AnnounceSnapshot | null,
  after: AnnounceSnapshot,
  state: EventState,
): string[] {
  if (!before) return [];
  const messages: string[] = [];
  if (after.callStatus && after.callStatus !== before.callStatus) {
    const message = CALL_STATUS[after.callStatus];
    if (message) messages.push(message);
  }
  const name = state.call?.business_name ?? "The business";
  for (const id of after.finalBusinessTurns)
    if (!before.finalBusinessTurns.includes(id)) {
      const text = state.turns[id]?.text;
      if (text) messages.push(`${name} said: ${text}`);
    }
  if (
    after.awaitingApproval &&
    after.awaitingApproval !== before.awaitingApproval
  )
    messages.push("A reply is waiting for your approval.");
  if (after.stream !== before.stream) {
    const problem = STREAM_PROBLEM[after.stream];
    if (problem) messages.push(problem);
  }
  return messages;
}
