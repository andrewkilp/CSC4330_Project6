// Event projections follow docs/contracts.md. T01 owns the final shared domain types.
export type CallStatus =
  | "draft"
  | "dialing"
  | "connected"
  | "ended"
  | "failed"
  | "cancelled";
export type ResponseStatus =
  | "generating"
  | "suggested"
  | "approved"
  | "speaking"
  | "spoken"
  | "cancelled"
  | "failed";
export interface EventGoal {
  readonly kind: "restaurant" | "appointment";
  readonly preferred_at: string;
  readonly timezone: string;
  readonly party_size?: number;
  readonly service?: string;
  readonly constraints: readonly string[];
  readonly contact_name: string;
  readonly contact_phone?: string;
}
export interface EventCall {
  readonly id: string;
  readonly owner_id: string;
  readonly mode: "mock" | "real";
  readonly destination: string;
  readonly business_name: string;
  readonly goal: EventGoal;
  readonly status: CallStatus;
  readonly approval_mode: "always" | "uncertain";
  readonly context_revision: number;
  readonly created_at: string;
  readonly updated_at: string;
}
export interface EventTurn {
  readonly id: string;
  readonly call_id: string;
  readonly speaker: "assistant" | "business";
  readonly text: string;
  readonly is_final: boolean;
  readonly started_at_ms: number;
  readonly ended_at_ms?: number;
  readonly response_id?: string;
}
export interface EventInstruction {
  readonly id: string;
  readonly call_id: string;
  readonly text: string;
  readonly context_revision: number;
  readonly status: "pending" | "applied" | "failed";
  readonly created_at: string;
}
export interface EventResponse {
  readonly id: string;
  readonly call_id: string;
  readonly text: string;
  readonly context_revision: number;
  readonly status: ResponseStatus;
  readonly needs_approval: boolean;
  readonly reason?: string;
}
export interface EventSummary {
  readonly call_id: string;
  readonly outcome:
    | "confirmed"
    | "declined"
    | "needs_follow_up"
    | "failed"
    | "cancelled";
  readonly confirmed_details: {
    readonly business_name: string;
    readonly scheduled_at?: string;
    readonly timezone?: string;
    readonly party_size?: number;
    readonly service?: string;
    readonly confirmation_reference?: string;
  };
  readonly evidence_turn_ids: readonly string[];
  readonly unresolved_questions: readonly string[];
  readonly next_steps: readonly string[];
}
export interface ProviderError {
  readonly provider: string;
  readonly code: string;
  readonly retryable: boolean;
}
export interface Usage {
  readonly elapsed_seconds: number;
  readonly estimated_cost_usd: number;
  readonly estimate_only: true;
}
interface Payloads {
  "call.updated": EventCall;
  "turn.updated": EventTurn;
  "instruction.updated": EventInstruction;
  "response.updated": EventResponse;
  "summary.ready": EventSummary;
  "provider.error": ProviderError;
  "usage.updated": Usage;
}
export type CallEvent = {
  [K in keyof Payloads]: {
    readonly id: string;
    readonly call_id: string;
    readonly seq: number;
    readonly type: K;
    readonly occurred_at: string;
    readonly context_revision: number;
    readonly payload: Payloads[K];
  };
}[keyof Payloads];

export class EventValidationError extends Error {
  readonly field: string;
  constructor(field: string) {
    super(`Invalid call event field: ${field}`);
    this.field = field;
    this.name = "EventValidationError";
  }
}
const invalid = (field: string): never => {
  throw new EventValidationError(field);
};
function object(value: unknown, field: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    return invalid(field);
  return value as Record<string, unknown>;
}
function text(
  value: unknown,
  field: string,
  max = 1500,
  empty = false,
): string {
  if (
    typeof value !== "string" ||
    value.length > max ||
    (!empty && !value.trim())
  )
    return invalid(field);
  return value;
}
function integer(
  value: unknown,
  field: string,
  min = 0,
  max = Number.MAX_SAFE_INTEGER,
): number {
  if (
    typeof value !== "number" ||
    !Number.isSafeInteger(value) ||
    value < min ||
    value > max
  )
    return invalid(field);
  return value;
}
function boolean(value: unknown, field: string): boolean {
  if (typeof value !== "boolean") return invalid(field);
  return value;
}
function choice<const T extends readonly string[]>(
  value: unknown,
  field: string,
  choices: T,
): T[number] {
  if (typeof value !== "string" || !choices.includes(value))
    return invalid(field);
  return value as T[number];
}
function uuid(value: unknown, field: string): string {
  const result = text(value, field, 36);
  if (
    !/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(
      result,
    )
  )
    return invalid(field);
  return result;
}
function date(value: unknown, field: string, utc = false): string {
  const result = text(value, field, 40);
  const match =
    /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$/.exec(
      result,
    );
  if (
    !match ||
    !Number.isFinite(Date.parse(result)) ||
    (utc && !result.endsWith("Z"))
  )
    return invalid(field);
  const year = Number(match[1]),
    month = Number(match[2]),
    day = Number(match[3]);
  const days = new Date(Date.UTC(year, month, 0)).getUTCDate();
  if (
    month < 1 ||
    month > 12 ||
    day < 1 ||
    day > days ||
    Number(match[4]) > 23 ||
    Number(match[5]) > 59 ||
    Number(match[6]) > 59
  )
    return invalid(field);
  return result;
}
function zone(value: unknown, field: string): string {
  const result = text(value, field, 100);
  try {
    new Intl.DateTimeFormat("en", { timeZone: result });
  } catch {
    return invalid(field);
  }
  return result;
}
function phone(value: unknown, field: string): string {
  const result = text(value, field, 16);
  if (!/^\+[1-9]\d{1,14}$/.test(result)) return invalid(field);
  return result;
}
function strings(
  value: unknown,
  field: string,
  maxItems = 100,
  maxText = 1000,
): string[] {
  if (!Array.isArray(value) || value.length > maxItems) return invalid(field);
  return value.map((item, i) => text(item, `${field}[${i}]`, maxText));
}
function optional<T>(
  value: unknown,
  read: (value: unknown) => T,
): T | undefined {
  return value === undefined ? undefined : read(value);
}
const callStatuses = [
  "draft",
  "dialing",
  "connected",
  "ended",
  "failed",
  "cancelled",
] as const;
const responseStatuses = [
  "generating",
  "suggested",
  "approved",
  "speaking",
  "spoken",
  "cancelled",
  "failed",
] as const;
function readGoal(value: unknown): EventGoal {
  const v = object(value, "goal");
  const kind = choice(v.kind, "goal.kind", ["restaurant", "appointment"]);
  const party_size = optional(v.party_size, (x) =>
    integer(x, "goal.party_size", 1, 20),
  );
  const service = optional(v.service, (x) => text(x, "goal.service", 160));
  if (
    (kind === "restaurant" && party_size === undefined) ||
    (kind === "appointment" && service === undefined)
  )
    return invalid("goal.kind_details");
  return {
    kind,
    preferred_at: date(v.preferred_at, "goal.preferred_at"),
    timezone: zone(v.timezone, "goal.timezone"),
    party_size,
    service,
    constraints: strings(v.constraints, "goal.constraints", 12),
    contact_name: text(v.contact_name, "goal.contact_name", 120),
    contact_phone: optional(v.contact_phone, (x) =>
      phone(x, "goal.contact_phone"),
    ),
  };
}
function readCall(value: unknown): EventCall {
  const v = object(value, "call");
  return {
    id: uuid(v.id, "call.id"),
    owner_id: uuid(v.owner_id, "call.owner_id"),
    mode: choice(v.mode, "call.mode", ["mock", "real"]),
    destination: phone(v.destination, "call.destination"),
    business_name: text(v.business_name, "call.business_name", 120),
    goal: readGoal(v.goal),
    status: choice(v.status, "call.status", callStatuses),
    approval_mode: choice(v.approval_mode, "call.approval_mode", [
      "always",
      "uncertain",
    ]),
    context_revision: integer(v.context_revision, "call.context_revision"),
    created_at: date(v.created_at, "call.created_at", true),
    updated_at: date(v.updated_at, "call.updated_at", true),
  };
}
function readTurn(value: unknown): EventTurn {
  const v = object(value, "turn"),
    started = integer(v.started_at_ms, "turn.started_at_ms");
  const ended = optional(v.ended_at_ms, (x) =>
    integer(x, "turn.ended_at_ms", started),
  );
  return {
    id: uuid(v.id, "turn.id"),
    call_id: uuid(v.call_id, "turn.call_id"),
    speaker: choice(v.speaker, "turn.speaker", ["assistant", "business"]),
    text: text(v.text, "turn.text", 10000, true),
    is_final: boolean(v.is_final, "turn.is_final"),
    started_at_ms: started,
    ended_at_ms: ended,
    response_id: optional(v.response_id, (x) => uuid(x, "turn.response_id")),
  };
}
function readInstruction(value: unknown): EventInstruction {
  const v = object(value, "instruction");
  return {
    id: uuid(v.id, "instruction.id"),
    call_id: uuid(v.call_id, "instruction.call_id"),
    text: text(v.text, "instruction.text", 1000),
    context_revision: integer(
      v.context_revision,
      "instruction.context_revision",
    ),
    status: choice(v.status, "instruction.status", [
      "pending",
      "applied",
      "failed",
    ]),
    created_at: date(v.created_at, "instruction.created_at", true),
  };
}
function readResponse(value: unknown): EventResponse {
  const v = object(value, "response");
  return {
    id: uuid(v.id, "response.id"),
    call_id: uuid(v.call_id, "response.call_id"),
    text: text(v.text, "response.text", 1500, true),
    context_revision: integer(v.context_revision, "response.context_revision"),
    status: choice(v.status, "response.status", responseStatuses),
    needs_approval: boolean(v.needs_approval, "response.needs_approval"),
    reason: optional(v.reason, (x) => text(x, "response.reason", 1000)),
  };
}
function readSummary(value: unknown): EventSummary {
  const v = object(value, "summary"),
    details = object(v.confirmed_details, "summary.confirmed_details");
  const evidence = strings(
    v.evidence_turn_ids,
    "summary.evidence_turn_ids",
  ).map((id) => uuid(id, "summary.evidence_turn_ids"));
  const outcome = choice(v.outcome, "summary.outcome", [
    "confirmed",
    "declined",
    "needs_follow_up",
    "failed",
    "cancelled",
  ]);
  if (outcome === "confirmed" && evidence.length === 0)
    return invalid("summary.evidence_turn_ids");
  return {
    call_id: uuid(v.call_id, "summary.call_id"),
    outcome,
    confirmed_details: {
      business_name: text(details.business_name, "summary.business_name", 120),
      scheduled_at: optional(details.scheduled_at, (x) =>
        date(x, "summary.scheduled_at"),
      ),
      timezone: optional(details.timezone, (x) => zone(x, "summary.timezone")),
      party_size: optional(details.party_size, (x) =>
        integer(x, "summary.party_size", 1, 20),
      ),
      service: optional(details.service, (x) =>
        text(x, "summary.service", 160),
      ),
      confirmation_reference: optional(details.confirmation_reference, (x) =>
        text(x, "summary.confirmation_reference", 200),
      ),
    },
    evidence_turn_ids: evidence,
    unresolved_questions: strings(
      v.unresolved_questions,
      "summary.unresolved_questions",
    ),
    next_steps: strings(v.next_steps, "summary.next_steps"),
  };
}
function readProviderError(value: unknown): ProviderError {
  const v = object(value, "provider.error");
  return {
    provider: text(v.provider, "provider", 100),
    code: text(v.code, "provider.code", 100),
    retryable: boolean(v.retryable, "provider.retryable"),
  };
}
function readUsage(value: unknown): Usage {
  const v = object(value, "usage");
  if (
    typeof v.estimated_cost_usd !== "number" ||
    !Number.isFinite(v.estimated_cost_usd) ||
    v.estimated_cost_usd < 0 ||
    v.estimate_only !== true
  )
    return invalid("usage.estimate");
  return {
    elapsed_seconds: integer(v.elapsed_seconds, "usage.elapsed_seconds"),
    estimated_cost_usd: v.estimated_cost_usd,
    estimate_only: true,
  };
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
        payload: readProviderError(v.payload),
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
function terminal(status: CallStatus): boolean {
  return status === "ended" || status === "failed" || status === "cancelled";
}
function responseTerminal(status: ResponseStatus): boolean {
  return status === "spoken" || status === "failed" || status === "cancelled";
}
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
        (from === "dialing" && to === "connected")))
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
      return terminal(event.payload.status) ? retireCurrent(next) : next;
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
export interface EventSnapshot {
  readonly call: EventCall;
  readonly last_seq: number;
  readonly turns: readonly EventTurn[];
  readonly instructions: readonly EventInstruction[];
  readonly responses: readonly EventResponse[];
  readonly summary?: EventSummary;
  readonly usage?: Usage;
}
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
          ["draft", "dialing", "connected"].indexOf(call.status) <
            ["draft", "dialing", "connected"].indexOf(state.call.status)))
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
      !terminal(call.status);
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
