import {
  createEventState,
  type EventState,
  hydrateSnapshot,
  reduceEvent,
} from "./events.ts";

export interface DemoMeta {
  scenario: "restaurant" | "appointment";
  phase:
    | "listening"
    | "awaiting_approval"
    | "speaking"
    | "confirming"
    | "ended";
  paused: boolean;
  notice: string;
  elapsed_seconds: number;
}
export interface DemoFrame {
  snapshot: unknown;
  events: unknown[];
  demo: DemoMeta;
}
export interface DemoView {
  state: EventState;
  meta: DemoMeta;
}
export interface DemoAction {
  action: "instruction" | "approve" | "pause" | "resume" | "end";
  text?: string;
  response_id?: string;
  context_revision: number;
}

async function request(
  base: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<DemoFrame> {
  const response = await fetch(new URL(path, base), {
    method: body ? "POST" : "GET",
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
    signal,
  });
  const data = await response.json();
  if (!response.ok)
    throw new Error(
      typeof data?.error?.message === "string"
        ? data.error.message
        : "The demo server is unavailable.",
    );
  if (
    !Array.isArray(data.events) ||
    !data.demo ||
    typeof data.demo.paused !== "boolean" ||
    typeof data.demo.notice !== "string" ||
    !["restaurant", "appointment"].includes(data.demo.scenario) ||
    ![
      "listening",
      "awaiting_approval",
      "speaking",
      "confirming",
      "ended",
    ].includes(data.demo.phase) ||
    !Number.isSafeInteger(data.demo.elapsed_seconds)
  )
    throw new Error("Unexpected demo response.");
  return data as DemoFrame;
}
export const startDemo = (base: string, scenario: DemoMeta["scenario"]) =>
  request(base, "/demo/sessions", { scenario });
export const pollDemo = (
  base: string,
  id: string,
  after: number,
  signal?: AbortSignal,
) =>
  request(
    base,
    `/demo/sessions/${encodeURIComponent(id)}?after_seq=${after}`,
    undefined,
    signal,
  );
export const actOnDemo = (base: string, id: string, action: DemoAction) =>
  request(base, `/demo/sessions/${encodeURIComponent(id)}/actions`, action);

export function applyDemoFrame(
  previous: DemoView | null,
  frame: DemoFrame,
): DemoView {
  const snapshot = frame.snapshot as {
    call?: { id?: unknown };
    last_seq?: unknown;
  };
  if (
    typeof snapshot?.call?.id !== "string" ||
    typeof snapshot.last_seq !== "number"
  )
    throw new Error("Invalid demo snapshot.");
  if (previous && previous.state.callId !== snapshot.call.id) return previous;
  if (previous && snapshot.last_seq < previous.state.lastSeq) return previous;
  let state = previous?.state ?? createEventState(snapshot.call.id);
  for (const event of frame.events) state = reduceEvent(state, event);
  if (state.recovery || state.lastSeq !== snapshot.last_seq)
    state = hydrateSnapshot(state, frame.snapshot);
  return { state, meta: frame.demo };
}
