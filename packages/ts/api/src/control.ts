// Pause, resume and end (#37, client for the #31 route). The server owns the call state;
// a control request stays "requested" until the event stream shows the new status.
import { CallRequestError, postCallAction } from "./calls.ts";
import {
  type Call,
  type CallStatus,
  type ControlAction,
  isTerminalCallStatus,
  parseCall,
  parseControlCallRequest,
} from "./types.ts";

export function allowedActions(status: CallStatus): readonly ControlAction[] {
  if (status === "connected") return ["pause", "end"];
  if (status === "paused") return ["resume", "end"];
  if (status === "draft" || status === "dialing") return ["end"];
  return [];
}

// True once the call's own status reflects the action.
export function isAcknowledged(
  action: ControlAction,
  status: CallStatus,
): boolean {
  if (action === "pause") return status === "paused";
  if (action === "resume") return status === "connected";
  return isTerminalCallStatus(status);
}

export async function controlCall(
  baseUrl: string,
  callId: string,
  action: ControlAction,
  idempotencyKey: string,
  signal?: AbortSignal,
): Promise<Call> {
  const body = parseControlCallRequest({
    action,
    idempotency_key: idempotencyKey,
  });
  const data = await postCallAction(
    baseUrl,
    callId,
    "control",
    body,
    200,
    signal,
  );
  try {
    const call = parseCall(data);
    if (call.id.toLowerCase() !== callId.toLowerCase()) throw new Error("call");
    return call;
  } catch {
    throw new CallRequestError("internal", 500);
  }
}

export type ControlProblem =
  | { readonly kind: "retry"; readonly message: string }
  | { readonly kind: "moot"; readonly message: string }
  | { readonly kind: "blocked"; readonly message: string };

const VERB: Record<ControlAction, string> = {
  pause: "Pause",
  resume: "Resume",
  end: "End call",
};

// A failed control leaves the call as the stream reports it; say so plainly.
export function controlProblem(
  action: ControlAction,
  error: unknown,
): ControlProblem {
  if (error instanceof CallRequestError) {
    if (error.code === "conflict" && error.field === "action")
      return {
        kind: "moot",
        message: `${VERB[action]} no longer applies to this call.`,
      };
    if (error.code === "unauthenticated")
      return {
        kind: "blocked",
        message: "Your session expired. Return to your calls to sign in again.",
      };
    if (error.code === "not_found")
      return {
        kind: "blocked",
        message: `${VERB[action]} is not available on this server yet.`,
      };
  }
  return {
    kind: "retry",
    message: `${VERB[action]} did not go through. The call is unchanged. Try again.`,
  };
}
