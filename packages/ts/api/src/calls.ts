// Typed client for the call routes (#18 create/read, #11 start, #28 instructions,
// #29 approval, #31 control, #39 summary, #23 snapshot) and the mock scenario presets.
// Every request sends the session cookie. Errors surface the server's user-safe
// message and field, never a raw body.
import {
  type ApproveResponseRequest,
  type Call,
  type CallSnapshot,
  type ControlCallRequest,
  type CreateCallRequest,
  type CreateInstructionRequest,
  type Goal,
  type Instruction,
  parseApiError,
  parseCall,
  parseGoal,
  parseInstruction,
  parseResponse,
  parseSummary,
  type Response,
  type Summary,
} from "./types.ts";
import { object, text } from "./validate.ts";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly field?: string;
  constructor(status: number, code: string, message: string, field?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.field = field;
  }
}

// A caller-generated idempotency key. Keep it for retries of the same action.
export function newIdempotencyKey(): string {
  return globalThis.crypto.randomUUID();
}

async function call<T>(
  baseUrl: string,
  path: string,
  init: { method?: string; body?: unknown; signal?: AbortSignal },
  parse: (value: unknown) => T,
): Promise<T> {
  let response: globalThis.Response;
  try {
    response = await fetch(new URL(path, baseUrl), {
      method: init.method ?? "GET",
      credentials: "include",
      signal: init.signal,
      headers:
        init.body === undefined
          ? { Accept: "application/json" }
          : { Accept: "application/json", "Content-Type": "application/json" },
      body: init.body === undefined ? undefined : JSON.stringify(init.body),
    });
  } catch (error) {
    if (init.signal?.aborted) throw error;
    throw new ApiError(
      0,
      "unavailable",
      "The server is unavailable. Try again.",
    );
  }
  let data: unknown = null;
  try {
    data = response.status === 204 ? null : await response.json();
  } catch {
    data = null;
  }
  if (!response.ok) {
    try {
      const { error } = parseApiError(data, response.status);
      throw new ApiError(
        response.status,
        error.code,
        error.message,
        error.field,
      );
    } catch (error) {
      if (error instanceof ApiError) throw error;
      throw new ApiError(
        response.status,
        "internal",
        "The server returned an unexpected response.",
      );
    }
  }
  try {
    return parse(data);
  } catch {
    throw new ApiError(
      response.status,
      "internal",
      "The server returned an unexpected response.",
    );
  }
}

// Starts a session (replacing any current one). Call only when a request returned 401.
export async function startSession(baseUrl: string): Promise<void> {
  await call(baseUrl, "/v1/session", { method: "POST", body: {} }, () => null);
}

export interface MockScenario {
  readonly name: string;
  readonly description: string;
  readonly business_name: string;
  readonly destination: string;
  readonly approval_mode: "always" | "uncertain";
  readonly goal: Goal;
  // Instructions the scripted business anticipates.
  readonly instructions: readonly string[];
}

export async function listMockScenarios(
  baseUrl: string,
  signal?: AbortSignal,
): Promise<readonly MockScenario[]> {
  return call(baseUrl, "/v1/mock/scenarios", { signal }, (value) => {
    const v = object(value, "body");
    if (!Array.isArray(v.scenarios)) throw new Error("scenarios");
    return v.scenarios.map((raw: unknown) => {
      const s = object(raw, "scenario");
      const approval = s.approval_mode === "uncertain" ? "uncertain" : "always";
      return {
        name: text(s.name, "scenario.name", 100),
        description: text(s.description, "scenario.description", 500, true),
        business_name: text(s.business_name, "scenario.business_name", 120),
        destination: text(s.destination, "scenario.destination", 16),
        approval_mode: approval,
        goal: parseGoal(s.goal),
        instructions: Array.isArray(s.instructions)
          ? s.instructions.map((x: unknown) => text(x, "instruction", 1000))
          : [],
      } satisfies MockScenario;
    });
  });
}

export const createCall = (baseUrl: string, req: CreateCallRequest) =>
  call(baseUrl, "/v1/calls", { method: "POST", body: req }, parseCall);

export const getCall = (baseUrl: string, id: string, signal?: AbortSignal) =>
  call(baseUrl, `/v1/calls/${encodeURIComponent(id)}`, { signal }, parseCall);

export const startCall = (
  baseUrl: string,
  id: string,
  idempotency_key: string,
) =>
  call(
    baseUrl,
    `/v1/calls/${encodeURIComponent(id)}/start`,
    { method: "POST", body: { idempotency_key } },
    parseCall,
  );

export const sendInstruction = (
  baseUrl: string,
  id: string,
  req: CreateInstructionRequest,
): Promise<Instruction> =>
  call(
    baseUrl,
    `/v1/calls/${encodeURIComponent(id)}/instructions`,
    { method: "POST", body: req },
    parseInstruction,
  );

export const approveResponse = (
  baseUrl: string,
  id: string,
  responseId: string,
  req: ApproveResponseRequest,
): Promise<Response> =>
  call(
    baseUrl,
    `/v1/calls/${encodeURIComponent(id)}/responses/${encodeURIComponent(responseId)}/approve`,
    { method: "POST", body: req },
    parseResponse,
  );

export const controlCall = (
  baseUrl: string,
  id: string,
  req: ControlCallRequest,
): Promise<Call> =>
  call(
    baseUrl,
    `/v1/calls/${encodeURIComponent(id)}/control`,
    { method: "POST", body: req },
    parseCall,
  );

export const getSummary = (
  baseUrl: string,
  id: string,
  signal?: AbortSignal,
): Promise<Summary> =>
  call(
    baseUrl,
    `/v1/calls/${encodeURIComponent(id)}/summary`,
    { signal },
    parseSummary,
  );

// The raw snapshot is validated by hydrateSnapshot in events.ts.
export const getSnapshot = (
  baseUrl: string,
  id: string,
  signal?: AbortSignal,
): Promise<unknown> =>
  call(
    baseUrl,
    `/v1/calls/${encodeURIComponent(id)}/snapshot`,
    { signal },
    (value) => value as CallSnapshot as unknown,
  );
