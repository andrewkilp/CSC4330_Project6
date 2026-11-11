// Event parsing and the client projection (#9). Domain types and entity parsers are the
// canonical contracts from types.ts (#1); this file adds the envelope and reducer.
import {
  type Call,
  type CallEvent,
  type CallSnapshot,
  type CallStatus,
  ContractValidationError,
  type Goal,
  type Instruction,
  isTerminalCallStatus,
  isTerminalResponseStatus,
  type ProviderError,
  parseCall,
  parseInstruction,
  parseProviderError,
  parseResponse,
  parseSummary,
  parseTurn,
  parseUsage,
  type Response,
  type ResponseStatus,
  type Summary,
  type Turn,
  type Usage,
} from "./types.ts";
import { date, integer, invalid, object, uuid } from "./validate.ts";

// Aliases kept for #9 callers. New code should import the canonical names.
export type EventGoal = Goal;
export type EventCall = Call;
export type EventTurn = Turn;
export type EventInstruction = Instruction;
export type EventResponse = Response;
export type EventSummary = Summary;
export { ContractValidationError as EventValidationError };

const readCall = parseCall,
  readTurn = parseTurn,
  readInstruction = parseInstruction,
  readResponse = parseResponse,
  readSummary = parseSummary,
  readUsage = parseUsage;

// Paused is a substate of connected, so a snapshot may move between them either way.
function lifecycleRank(status: CallStatus): number {
  return status === "draft" ? 0 : status === "dialing" ? 1 : 2;
}

// Accepts decoded JSON, validates owned fields and detaches payloads from the input.
export function parseEvent(input: unknown): CallEvent {
  const v = object(input, "event");
  const envelope = {
    id: uuid(v.id, "id"),
    call_id: uuid(v.call_id, "call_id"),
    seq: integer(v.seq, "seq", 1),
    occurred_at: date(v.occurred_at, "occurred_at", true),
    context_revision: integer(v.context_revision, "context_revision"),
  };
  let event: CallEvent;
  switch (v.type) {
    case "call.updated":
      event = { ...envelope, type: v.type, payload: readCall(v.payload) };
      break;
    case "turn.updated":
      event = { ...envelope, type: v.type, payload: readTurn(v.payload) };
      break;
    case "instruction.updated":
      event = {
        ...envelope,
        type: v.type,
        payload: readInstruction(v.payload),
      };
      break;
    case "response.updated":
      event = { ...envelope, type: v.type, payload: readResponse(v.payload) };
      break;
    case "summary.ready":
      event = { ...envelope, type: v.type, payload: readSummary(v.payload) };
      break;
    case "provider.error":
      event = {
        ...envelope,
        type: v.type,
        payload: parseProviderError(v.payload),
      };
      break;
    case "usage.updated":
      event = { ...envelope, type: v.type, payload: readUsage(v.payload) };
      break;
    default:
      return invalid("type");
  }
  const payload = event.payload;
  if (
    (event.type === "call.updated" && event.payload.id !== event.call_id) ||
    ("call_id" in payload && payload.call_id !== event.call_id)
  )
    return invalid("payload.call_id");
  if (
    "context_revision" in payload &&
    payload.context_revision !== event.context_revision
  )
    return invalid("payload.context_revision");
  return event;
}

export interface EventState {
  readonly callId: string;
  readonly lastSeq: number;
  readonly contextRevision: number;
  readonly call: EventCall | null;
  readonly turns: Readonly<Record<string, EventTurn>>;
  readonly turnOrder: readonly string[];
  readonly instructions: Readonly<Record<string, EventInstruction>>;
  readonly responses: Readonly<Record<string, EventResponse>>;
  readonly currentResponseId: string | null;
  readonly retiredResponseIds: Readonly<Record<string, true>>;
  readonly summary: EventSummary | null;
  readonly providerError: ProviderError | null;
  readonly usage: Usage | null;
  readonly recovery: {
    readonly reason: "sequence_gap" | "cursor_expired";
    readonly afterSeq: number;
  } | null;
}
export function createEventState(callId: string): EventState {
  return {
    callId: uuid(callId, "callId"),
    lastSeq: 0,
    contextRevision: 0,
    call: null,
    turns: {},
    turnOrder: [],
    instructions: {},
    responses: {},
    currentResponseId: null,
    retiredResponseIds: {},
    summary: null,
    providerError: null,
    usage: null,
    recovery: null,
  };
}
const terminal = isTerminalCallStatus,
  responseTerminal = isTerminalResponseStatus;
function retireCurrent(state: EventState): EventState {
  return state.currentResponseId
    ? {
        ...state,
        retiredResponseIds: {
          ...state.retiredResponseIds,
          [state.currentResponseId]: true,
        },
        currentResponseId: null,
      }
    : state;
}
function callCanAdvance(from: CallStatus, to: CallStatus): boolean {
  return (
    from === to ||
    (!terminal(from) &&
      (terminal(to) ||
        (from === "draft" && to === "dialing") ||
        (from === "dialing" && to === "connected") ||
        (from === "connected" && to === "paused") ||
        (from === "paused" && to === "connected")))
  );
}
function responseCanAdvance(from: ResponseStatus, to: ResponseStatus): boolean {
  if (responseTerminal(from)) return false;
  const next: Partial<Record<ResponseStatus, ResponseStatus>> = {
    generating: "suggested",
    suggested: "approved",
    approved: "speaking",
    speaking: "spoken",
  };
  return (
    from === to || to === "failed" || to === "cancelled" || next[from] === to
  );
}

// All valid contiguous events advance the cursor, even when their entity update is stale.
export function reduceEvent(state: EventState, input: unknown): EventState {
  const event = parseEvent(input);
  if (event.call_id !== state.callId) return invalid("call_id");
  if (state.recovery || event.seq <= state.lastSeq) return state;
  if (event.seq !== state.lastSeq + 1)
    return {
      ...retireCurrent(state),
      recovery: { reason: "sequence_gap", afterSeq: state.lastSeq },
    };
  let next: EventState = { ...state, lastSeq: event.seq };
  if (event.context_revision < state.contextRevision) return next;
  if (
    state.call &&
    terminal(state.call.status) &&
    event.type !== "summary.ready" &&
    event.type !== "provider.error" &&
    event.type !== "usage.updated"
  )
    return next;
  if (event.context_revision > state.contextRevision) {
    next = {
      ...retireCurrent(next),
      contextRevision: event.context_revision,
      call: next.call
        ? { ...next.call, context_revision: event.context_revision }
        : null,
    };
  }
  switch (event.type) {
    case "call.updated": {
      if (next.call && !callCanAdvance(next.call.status, event.payload.status))
        return next;
      next = { ...next, call: event.payload };
      // Pause and end both cancel the pending suggestion.
      return terminal(event.payload.status) || event.payload.status === "paused"
        ? retireCurrent(next)
        : next;
    }
    case "turn.updated": {
      const turn = event.payload,
        previous = next.turns[turn.id];
      if (
        previous &&
        (previous.is_final ||
          previous.speaker !== turn.speaker ||
          previous.started_at_ms !== turn.started_at_ms)
      )
        return next;
      next = {
        ...next,
        turns: { ...next.turns, [turn.id]: turn },
        turnOrder: previous ? next.turnOrder : [...next.turnOrder, turn.id],
      };
      return turn.is_final && turn.speaker === "business"
        ? retireCurrent(next)
        : next;
    }
    case "instruction.updated": {
      const instruction = event.payload,
        previous = next.instructions[instruction.id];
      if (previous && previous.status !== "pending") return next;
      return {
        ...next,
        instructions: { ...next.instructions, [instruction.id]: instruction },
      };
    }
    case "response.updated": {
      const response = event.payload,
        previous = next.responses[response.id];
      if (
        next.retiredResponseIds[response.id] ||
        (previous && !responseCanAdvance(previous.status, response.status))
      )
        return next;
      if (
        !previous &&
        next.currentResponseId &&
        next.currentResponseId !== response.id
      )
        next = retireCurrent(next);
      return {
        ...next,
        responses: { ...next.responses, [response.id]: response },
        currentResponseId: responseTerminal(response.status)
          ? null
          : response.id,
      };
    }
    case "summary.ready": {
      if (!next.call || !terminal(next.call.status)) return next;
      if (
        event.payload.outcome === "confirmed" &&
        !event.payload.evidence_turn_ids.every(
          (id) =>
            next.turns[id]?.is_final && next.turns[id]?.speaker === "business",
        )
      )
        return next;
      return { ...next, summary: event.payload };
    }
    case "provider.error":
      return { ...next, providerError: event.payload };
    case "usage.updated":
      return next.usage &&
        event.payload.elapsed_seconds < next.usage.elapsed_seconds
        ? next
        : { ...next, usage: event.payload };
  }
}

export function expireCursor(state: EventState): EventState {
  return {
    ...retireCurrent(state),
    recovery: { reason: "cursor_expired", afterSeq: state.lastSeq },
  };
}

// This is an in-memory handoff to the future transport, not a new HTTP endpoint.
export type EventSnapshot = CallSnapshot;
export function hydrateSnapshot(state: EventState, input: unknown): EventState {
  const v = object(input, "snapshot"),
    call = readCall(v.call),
    lastSeq = integer(v.last_seq, "snapshot.last_seq");
  if (
    call.id !== state.callId ||
    lastSeq < state.lastSeq ||
    call.context_revision < state.contextRevision ||
    (state.call &&
      (terminal(state.call.status)
        ? state.call.status !== call.status
        : !terminal(call.status) &&
          lifecycleRank(call.status) < lifecycleRank(state.call.status)))
  )
    return invalid("snapshot.cursor_or_call");
  let next: EventState = {
    ...createEventState(call.id),
    call,
    contextRevision: call.context_revision,
    lastSeq,
  };
  function items<T>(
    value: unknown,
    field: string,
    read: (value: unknown) => T,
  ): T[] {
    if (!Array.isArray(value)) return invalid(field);
    return value.map(read);
  }
  const turns = items(v.turns, "snapshot.turns", readTurn),
    instructions = items(
      v.instructions,
      "snapshot.instructions",
      readInstruction,
    ),
    responses = items(v.responses, "snapshot.responses", readResponse);
  for (const turn of turns) {
    if (turn.call_id !== call.id || next.turns[turn.id])
      return invalid("snapshot.turns");
    next = {
      ...next,
      turns: { ...next.turns, [turn.id]: turn },
      turnOrder: [...next.turnOrder, turn.id],
    };
  }
  for (const instruction of instructions) {
    if (
      instruction.call_id !== call.id ||
      instruction.context_revision > call.context_revision ||
      next.instructions[instruction.id]
    )
      return invalid("snapshot.instructions");
    next = {
      ...next,
      instructions: { ...next.instructions, [instruction.id]: instruction },
    };
  }
  for (const response of responses) {
    if (
      response.call_id !== call.id ||
      response.context_revision > call.context_revision ||
      next.responses[response.id]
    )
      return invalid("snapshot.responses");
    const active =
      response.context_revision === call.context_revision &&
      !responseTerminal(response.status) &&
      !terminal(call.status) &&
      call.status !== "paused";
    if (active && next.currentResponseId)
      return invalid("snapshot.responses.active");
    next = {
      ...next,
      responses: { ...next.responses, [response.id]: response },
      currentResponseId: active ? response.id : next.currentResponseId,
      retiredResponseIds: active
        ? next.retiredResponseIds
        : { ...next.retiredResponseIds, [response.id]: true },
    };
  }
  if (v.summary !== undefined) {
    const summary = readSummary(v.summary);
    if (
      summary.call_id !== call.id ||
      !terminal(call.status) ||
      (summary.outcome === "confirmed" &&
        !summary.evidence_turn_ids.every(
          (id) =>
            next.turns[id]?.is_final && next.turns[id]?.speaker === "business",
        ))
    )
      return invalid("snapshot.summary");
    next = { ...next, summary };
  }
  if (v.usage !== undefined) next = { ...next, usage: readUsage(v.usage) };
  return next;
}
