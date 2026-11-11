// Canonical request, domain and error contracts for Switchboard (T01, #1).
// docs/contracts.md is the prose source; this file is its executable form. A contract
// change updates both, the fixtures in test/fixtures/contracts.json and the Go types.
import {
  boolean,
  ContractValidationError,
  choice,
  date,
  exact,
  integer,
  invalid,
  object,
  optional,
  phone,
  strings,
  text,
  uuid,
  zone,
} from "./validate.ts";

export { ContractValidationError };

// ---------------------------------------------------------------------------
// Enums. Each array is the closed set of wire values; the type is derived from it.

export const GOAL_KINDS = ["restaurant", "appointment"] as const;
export const CALL_MODES = ["mock", "real"] as const;
// `paused` is a substate of connected: the line stays open, the assistant is silent.
export const CALL_STATUSES = [
  "draft",
  "dialing",
  "connected",
  "paused",
  "ended",
  "failed",
  "cancelled",
] as const;
export const TERMINAL_CALL_STATUSES = ["ended", "failed", "cancelled"] as const;
export const APPROVAL_MODES = ["always", "uncertain"] as const;
export const SPEAKERS = ["assistant", "business"] as const;
export const INSTRUCTION_STATUSES = ["pending", "applied", "failed"] as const;
export const RESPONSE_STATUSES = [
  "generating",
  "suggested",
  "approved",
  "speaking",
  "spoken",
  "cancelled",
  "failed",
] as const;
export const TERMINAL_RESPONSE_STATUSES = [
  "spoken",
  "cancelled",
  "failed",
] as const;
export const SUMMARY_OUTCOMES = [
  "confirmed",
  "declined",
  "needs_follow_up",
  "failed",
  "cancelled",
] as const;
export const CONTROL_ACTIONS = ["pause", "resume", "end"] as const;
export const PROVIDER_KINDS = ["telephony", "stt", "llm", "tts"] as const;
export const EVENT_TYPES = [
  "call.updated",
  "turn.updated",
  "instruction.updated",
  "response.updated",
  "summary.ready",
  "provider.error",
  "usage.updated",
] as const;
export const ERROR_CODES = [
  "invalid_argument",
  "unauthenticated",
  "not_found",
  "conflict",
  "rate_limited",
  "provider_unavailable",
  "internal",
] as const;

export type GoalKind = (typeof GOAL_KINDS)[number];
export type CallMode = (typeof CALL_MODES)[number];
export type CallStatus = (typeof CALL_STATUSES)[number];
export type ApprovalMode = (typeof APPROVAL_MODES)[number];
export type Speaker = (typeof SPEAKERS)[number];
export type InstructionStatus = (typeof INSTRUCTION_STATUSES)[number];
export type ResponseStatus = (typeof RESPONSE_STATUSES)[number];
export type SummaryOutcome = (typeof SUMMARY_OUTCOMES)[number];
export type ControlAction = (typeof CONTROL_ACTIONS)[number];
export type ProviderKind = (typeof PROVIDER_KINDS)[number];
export type EventType = (typeof EVENT_TYPES)[number];
export type ErrorCode = (typeof ERROR_CODES)[number];

export const ERROR_HTTP_STATUS: Readonly<Record<ErrorCode, number>> = {
  invalid_argument: 400,
  unauthenticated: 401,
  not_found: 404,
  conflict: 409,
  rate_limited: 429,
  provider_unavailable: 503,
  internal: 500,
};

export function isTerminalCallStatus(status: CallStatus): boolean {
  return (TERMINAL_CALL_STATUSES as readonly string[]).includes(status);
}
export function isTerminalResponseStatus(status: ResponseStatus): boolean {
  return (TERMINAL_RESPONSE_STATUSES as readonly string[]).includes(status);
}

// ---------------------------------------------------------------------------
// Limits. Character counts are UTF-16 code units, matching JavaScript `length`.

export const LIMITS = {
  businessName: 120,
  contactName: 120,
  partySizeMin: 1,
  partySizeMax: 20,
  service: 160,
  constraintsMaxItems: 12,
  constraintText: 200,
  instructionText: 1000,
  responseText: 1500,
  responseReason: 1000,
  turnText: 10000,
  summaryListItems: 100,
  summaryListText: 1000,
  confirmationReference: 200,
  providerCode: 100,
  errorMessage: 500,
  errorField: 100,
  listDefault: 20,
  listMax: 100,
  cursor: 200,
  // Mutations reject larger JSON bodies before decoding.
  requestBodyBytes: 16384,
  // Real-call guard rails for the two-week MVP (enforced server-side in T11/T34).
  realCallMaxSeconds: 180,
  realCallConcurrentPerAccount: 1,
} as const;

// Mock calls must target the fictional North American 555-0100..0199 range, which is
// reserved for fiction and never routes to a subscriber.
export const FICTIONAL_DESTINATION = /^\+1[2-9]\d{2}55501\d{2}$/;

// ---------------------------------------------------------------------------
// Domain types.

export interface Goal {
  readonly kind: GoalKind;
  // RFC 3339 with an explicit offset; `timezone` records the business's IANA zone.
  readonly preferred_at: string;
  readonly timezone: string;
  // Required for restaurant goals; absent for appointment goals.
  readonly party_size?: number;
  // Required for appointment goals; absent for restaurant goals.
  readonly service?: string;
  readonly constraints: readonly string[];
  readonly contact_name: string;
  // Only shared with the business if the goal includes it. No model may invent one.
  readonly contact_phone?: string;
}

export interface Call {
  readonly id: string;
  readonly owner_id: string;
  readonly mode: CallMode;
  readonly destination: string;
  readonly business_name: string;
  readonly goal: Goal;
  readonly status: CallStatus;
  readonly approval_mode: ApprovalMode;
  readonly context_revision: number;
  readonly created_at: string;
  readonly updated_at: string;
}

export interface Turn {
  readonly id: string;
  readonly call_id: string;
  readonly speaker: Speaker;
  readonly text: string;
  readonly is_final: boolean;
  readonly started_at_ms: number;
  readonly ended_at_ms?: number;
  readonly response_id?: string;
}

export interface Instruction {
  readonly id: string;
  readonly call_id: string;
  readonly text: string;
  readonly context_revision: number;
  readonly status: InstructionStatus;
  readonly created_at: string;
}

export interface Response {
  readonly id: string;
  readonly call_id: string;
  // Empty only while generating.
  readonly text: string;
  readonly context_revision: number;
  readonly status: ResponseStatus;
  readonly needs_approval: boolean;
  readonly reason?: string;
}

export interface ConfirmedDetails {
  readonly business_name: string;
  readonly scheduled_at?: string;
  readonly timezone?: string;
  readonly party_size?: number;
  readonly service?: string;
  readonly confirmation_reference?: string;
}

export interface Summary {
  readonly call_id: string;
  readonly outcome: SummaryOutcome;
  readonly confirmed_details: ConfirmedDetails;
  readonly evidence_turn_ids: readonly string[];
  readonly unresolved_questions: readonly string[];
  readonly next_steps: readonly string[];
}

export interface ProviderError {
  readonly provider: ProviderKind;
  // A stable, user-safe code. Never a vendor message or body.
  readonly code: string;
  readonly retryable: boolean;
}

export interface Usage {
  readonly elapsed_seconds: number;
  readonly estimated_cost_usd: number;
  readonly estimate_only: true;
}

export interface ProviderAvailability {
  readonly available: boolean;
  // User-safe explanation when unavailable. Never a key or vendor error.
  readonly reason?: string;
}

export interface Providers {
  readonly mode: CallMode;
  readonly telephony: ProviderAvailability;
  readonly stt: ProviderAvailability;
  readonly llm: ProviderAvailability;
  readonly tts: ProviderAvailability;
}

export interface CallPage {
  readonly calls: readonly Call[];
  readonly next_cursor: string | null;
}

export interface Session {
  readonly account_id: string;
}

// One consistent server checkpoint used for reconnect recovery (docs/events.md).
// The route that serves it is coordinated with #18 and #23.
export interface CallSnapshot {
  readonly call: Call;
  readonly last_seq: number;
  readonly turns: readonly Turn[];
  readonly instructions: readonly Instruction[];
  readonly responses: readonly Response[];
  readonly summary?: Summary;
  readonly usage?: Usage;
}

// ---------------------------------------------------------------------------
// Event envelope. Parsing and reduction live in events.ts.

export interface EventPayloads {
  "call.updated": Call;
  "turn.updated": Turn;
  "instruction.updated": Instruction;
  "response.updated": Response;
  "summary.ready": Summary;
  "provider.error": ProviderError;
  "usage.updated": Usage;
}

export type CallEvent = {
  [K in EventType]: {
    readonly id: string;
    readonly call_id: string;
    // Strictly increasing per call, starting at 1. SSE `id` is its decimal form.
    readonly seq: number;
    readonly type: K;
    readonly occurred_at: string;
    readonly context_revision: number;
    readonly payload: EventPayloads[K];
  };
}[EventType];

// ---------------------------------------------------------------------------
// Requests. Every mutation carries a caller-generated UUID idempotency key scoped by
// owner, action and call; reusing a key with different input is a conflict.

export interface CreateCallRequest {
  readonly mode: CallMode;
  readonly destination: string;
  readonly business_name: string;
  readonly goal: Goal;
  readonly approval_mode: ApprovalMode;
  // The operator confirmed the recipient consents to an AI-assisted call.
  readonly consent_acknowledged: true;
  readonly idempotency_key: string;
}

export interface StartCallRequest {
  readonly idempotency_key: string;
}

export interface CreateInstructionRequest {
  readonly text: string;
  // Must equal the call's current context_revision, otherwise 409 conflict.
  readonly expected_revision: number;
  readonly idempotency_key: string;
}

export interface ApproveResponseRequest {
  // Must equal both the response's and the call's context_revision, otherwise 409.
  readonly context_revision: number;
  // Optional operator edit; replaces the suggested text before speech.
  readonly edited_text?: string;
  readonly idempotency_key: string;
}

export interface ControlCallRequest {
  readonly action: ControlAction;
  readonly idempotency_key: string;
}

export interface ListCallsQuery {
  readonly cursor?: string;
  readonly limit?: number;
}

// ---------------------------------------------------------------------------
// Errors.

export interface ApiErrorBody {
  readonly error: {
    readonly code: ErrorCode;
    readonly message: string;
    readonly field?: string;
  };
}

// ---------------------------------------------------------------------------
// Parsers. Each accepts decoded JSON and returns a detached, validated copy, or throws
// ContractValidationError naming only the offending field. Request parsers reject
// unknown fields; entity parsers drop them so additive server fields stay compatible.

export function parseGoal(value: unknown, field = "goal"): Goal {
  const v = object(value, field);
  const kind = choice(v.kind, `${field}.kind`, GOAL_KINDS);
  const party_size = optional(v.party_size, (x) =>
    integer(x, `${field}.party_size`, LIMITS.partySizeMin, LIMITS.partySizeMax),
  );
  const service = optional(v.service, (x) =>
    text(x, `${field}.service`, LIMITS.service),
  );
  if (kind === "restaurant" && party_size === undefined)
    return invalid(`${field}.party_size`);
  if (kind === "appointment" && service === undefined)
    return invalid(`${field}.service`);
  return {
    kind,
    preferred_at: date(v.preferred_at, `${field}.preferred_at`),
    timezone: zone(v.timezone, `${field}.timezone`),
    party_size,
    service,
    constraints: strings(
      v.constraints,
      `${field}.constraints`,
      LIMITS.constraintsMaxItems,
      LIMITS.constraintText,
    ),
    contact_name: text(
      v.contact_name,
      `${field}.contact_name`,
      LIMITS.contactName,
    ),
    contact_phone: optional(v.contact_phone, (x) =>
      phone(x, `${field}.contact_phone`),
    ),
  };
}

export function parseCall(value: unknown): Call {
  const v = object(value, "call");
  return {
    id: uuid(v.id, "call.id"),
    owner_id: uuid(v.owner_id, "call.owner_id"),
    mode: choice(v.mode, "call.mode", CALL_MODES),
    destination: phone(v.destination, "call.destination"),
    business_name: text(
      v.business_name,
      "call.business_name",
      LIMITS.businessName,
    ),
    goal: parseGoal(v.goal),
    status: choice(v.status, "call.status", CALL_STATUSES),
    approval_mode: choice(
      v.approval_mode,
      "call.approval_mode",
      APPROVAL_MODES,
    ),
    context_revision: integer(v.context_revision, "call.context_revision"),
    created_at: date(v.created_at, "call.created_at", true),
    updated_at: date(v.updated_at, "call.updated_at", true),
  };
}

export function parseTurn(value: unknown): Turn {
  const v = object(value, "turn"),
    started = integer(v.started_at_ms, "turn.started_at_ms");
  return {
    id: uuid(v.id, "turn.id"),
    call_id: uuid(v.call_id, "turn.call_id"),
    speaker: choice(v.speaker, "turn.speaker", SPEAKERS),
    text: text(v.text, "turn.text", LIMITS.turnText, true),
    is_final: boolean(v.is_final, "turn.is_final"),
    started_at_ms: started,
    ended_at_ms: optional(v.ended_at_ms, (x) =>
      integer(x, "turn.ended_at_ms", started),
    ),
    response_id: optional(v.response_id, (x) => uuid(x, "turn.response_id")),
  };
}

export function parseInstruction(value: unknown): Instruction {
  const v = object(value, "instruction");
  return {
    id: uuid(v.id, "instruction.id"),
    call_id: uuid(v.call_id, "instruction.call_id"),
    text: text(v.text, "instruction.text", LIMITS.instructionText),
    context_revision: integer(
      v.context_revision,
      "instruction.context_revision",
    ),
    status: choice(v.status, "instruction.status", INSTRUCTION_STATUSES),
    created_at: date(v.created_at, "instruction.created_at", true),
  };
}

export function parseResponse(value: unknown): Response {
  const v = object(value, "response");
  const status = choice(v.status, "response.status", RESPONSE_STATUSES);
  const body = text(v.text, "response.text", LIMITS.responseText, true);
  // Only a generating (or abandoned) response may still be empty.
  if (
    !body.trim() &&
    status !== "generating" &&
    status !== "cancelled" &&
    status !== "failed"
  )
    return invalid("response.text");
  return {
    id: uuid(v.id, "response.id"),
    call_id: uuid(v.call_id, "response.call_id"),
    text: body,
    context_revision: integer(v.context_revision, "response.context_revision"),
    status,
    needs_approval: boolean(v.needs_approval, "response.needs_approval"),
    reason: optional(v.reason, (x) =>
      text(x, "response.reason", LIMITS.responseReason),
    ),
  };
}

export function parseSummary(value: unknown): Summary {
  const v = object(value, "summary"),
    details = object(v.confirmed_details, "summary.confirmed_details");
  const evidence = strings(
    v.evidence_turn_ids,
    "summary.evidence_turn_ids",
    LIMITS.summaryListItems,
    36,
  ).map((id) => uuid(id, "summary.evidence_turn_ids"));
  const outcome = choice(v.outcome, "summary.outcome", SUMMARY_OUTCOMES);
  if (outcome === "confirmed" && evidence.length === 0)
    return invalid("summary.evidence_turn_ids");
  const list = (x: unknown, field: string) =>
    strings(x, field, LIMITS.summaryListItems, LIMITS.summaryListText);
  return {
    call_id: uuid(v.call_id, "summary.call_id"),
    outcome,
    confirmed_details: {
      business_name: text(
        details.business_name,
        "summary.business_name",
        LIMITS.businessName,
      ),
      scheduled_at: optional(details.scheduled_at, (x) =>
        date(x, "summary.scheduled_at"),
      ),
      timezone: optional(details.timezone, (x) => zone(x, "summary.timezone")),
      party_size: optional(details.party_size, (x) =>
        integer(
          x,
          "summary.party_size",
          LIMITS.partySizeMin,
          LIMITS.partySizeMax,
        ),
      ),
      service: optional(details.service, (x) =>
        text(x, "summary.service", LIMITS.service),
      ),
      confirmation_reference: optional(details.confirmation_reference, (x) =>
        text(x, "summary.confirmation_reference", LIMITS.confirmationReference),
      ),
    },
    evidence_turn_ids: evidence,
    unresolved_questions: list(
      v.unresolved_questions,
      "summary.unresolved_questions",
    ),
    next_steps: list(v.next_steps, "summary.next_steps"),
  };
}

export function parseProviderError(value: unknown): ProviderError {
  const v = object(value, "provider_error");
  return {
    provider: choice(v.provider, "provider_error.provider", PROVIDER_KINDS),
    code: text(v.code, "provider_error.code", LIMITS.providerCode),
    retryable: boolean(v.retryable, "provider_error.retryable"),
  };
}

export function parseUsage(value: unknown): Usage {
  const v = object(value, "usage");
  if (
    typeof v.estimated_cost_usd !== "number" ||
    !Number.isFinite(v.estimated_cost_usd) ||
    v.estimated_cost_usd < 0
  )
    return invalid("usage.estimated_cost_usd");
  if (v.estimate_only !== true) return invalid("usage.estimate_only");
  return {
    elapsed_seconds: integer(v.elapsed_seconds, "usage.elapsed_seconds"),
    estimated_cost_usd: v.estimated_cost_usd,
    estimate_only: true,
  };
}

export function parseCreateCallRequest(value: unknown): CreateCallRequest {
  const v = object(value, "request");
  exact(v, "request", [
    "mode",
    "destination",
    "business_name",
    "goal",
    "approval_mode",
    "consent_acknowledged",
    "idempotency_key",
  ]);
  const mode = choice(v.mode, "mode", CALL_MODES);
  const destination = phone(v.destination, "destination");
  if (mode === "mock" && !FICTIONAL_DESTINATION.test(destination))
    return invalid("destination");
  if (v.consent_acknowledged !== true) return invalid("consent_acknowledged");
  const goal = object(v.goal, "goal");
  exact(goal, "goal", [
    "kind",
    "preferred_at",
    "timezone",
    "party_size",
    "service",
    "constraints",
    "contact_name",
    "contact_phone",
  ]);
  const parsed = parseGoal(goal);
  // A goal carries exactly one kind-specific detail.
  if (parsed.kind === "restaurant" && parsed.service !== undefined)
    return invalid("goal.service");
  if (parsed.kind === "appointment" && parsed.party_size !== undefined)
    return invalid("goal.party_size");
  return {
    mode,
    destination,
    business_name: text(v.business_name, "business_name", LIMITS.businessName),
    goal: parsed,
    approval_mode: choice(v.approval_mode, "approval_mode", APPROVAL_MODES),
    consent_acknowledged: true,
    idempotency_key: uuid(v.idempotency_key, "idempotency_key"),
  };
}

export function parseStartCallRequest(value: unknown): StartCallRequest {
  const v = object(value, "request");
  exact(v, "request", ["idempotency_key"]);
  return { idempotency_key: uuid(v.idempotency_key, "idempotency_key") };
}

export function parseCreateInstructionRequest(
  value: unknown,
): CreateInstructionRequest {
  const v = object(value, "request");
  exact(v, "request", ["text", "expected_revision", "idempotency_key"]);
  return {
    text: text(v.text, "text", LIMITS.instructionText),
    expected_revision: integer(v.expected_revision, "expected_revision"),
    idempotency_key: uuid(v.idempotency_key, "idempotency_key"),
  };
}

export function parseApproveResponseRequest(
  value: unknown,
): ApproveResponseRequest {
  const v = object(value, "request");
  exact(v, "request", ["context_revision", "edited_text", "idempotency_key"]);
  return {
    context_revision: integer(v.context_revision, "context_revision"),
    edited_text: optional(v.edited_text, (x) =>
      text(x, "edited_text", LIMITS.responseText),
    ),
    idempotency_key: uuid(v.idempotency_key, "idempotency_key"),
  };
}

export function parseControlCallRequest(value: unknown): ControlCallRequest {
  const v = object(value, "request");
  exact(v, "request", ["action", "idempotency_key"]);
  return {
    action: choice(v.action, "action", CONTROL_ACTIONS),
    idempotency_key: uuid(v.idempotency_key, "idempotency_key"),
  };
}

export function parseListCallsQuery(value: unknown): ListCallsQuery {
  const v = object(value, "query");
  exact(v, "query", ["cursor", "limit"]);
  return {
    cursor: optional(v.cursor, (x) => text(x, "cursor", LIMITS.cursor)),
    limit: optional(v.limit, (x) => integer(x, "limit", 1, LIMITS.listMax)),
  };
}

// Validates an error body. When `status` is given it must match the code's mapping,
// so a client never trusts an error whose HTTP status disagrees with its code.
export function parseApiError(value: unknown, status?: number): ApiErrorBody {
  const v = object(object(value, "body").error, "error");
  const code = choice(v.code, "error.code", ERROR_CODES);
  if (status !== undefined && ERROR_HTTP_STATUS[code] !== status)
    return invalid("error.status");
  return {
    error: {
      code,
      message: text(v.message, "error.message", LIMITS.errorMessage),
      field: optional(v.field, (x) =>
        text(x, "error.field", LIMITS.errorField),
      ),
    },
  };
}

// Server-side approval check, shared so clients and tests agree on what "stale" means:
// an approval binds to one response id and the call's current context revision.
export function staleApprovalError(
  call: Pick<Call, "context_revision" | "status">,
  response: Pick<Response, "context_revision" | "status">,
  request: Pick<ApproveResponseRequest, "context_revision">,
): ApiErrorBody | null {
  if (
    request.context_revision !== call.context_revision ||
    response.context_revision !== call.context_revision
  )
    return {
      error: {
        code: "conflict",
        message:
          "This suggestion is out of date. Review the newest suggestion before approving.",
        field: "context_revision",
      },
    };
  if (response.status !== "suggested" || call.status !== "connected")
    return {
      error: {
        code: "conflict",
        message: "This suggestion can no longer be approved.",
        field: "status",
      },
    };
  return null;
}
