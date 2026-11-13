import {
  type Call,
  type CreateCallRequest,
  type ErrorCode,
  parseApiError,
  parseCall,
  parseCreateCallRequest,
} from "./types.ts";

const messages: Record<ErrorCode, string> = {
  invalid_argument: "Check the call details and try again.",
  unauthenticated:
    "Your session has expired. Return to the review screen to create a new session.",
  not_found: "This call was not found in your session.",
  conflict:
    "This request conflicts with an earlier saved draft. Review the details before trying again.",
  rate_limited: "Too many requests. Try again shortly.",
  provider_unavailable:
    "The call service is unavailable. Your goal is still here; try again when it is connected.",
  internal: "The call service returned an unexpected result.",
};
export class CallRequestError extends Error {
  readonly code: ErrorCode;
  readonly status: number;
  readonly field?: string;
  constructor(code: ErrorCode, status: number, field?: string) {
    super(messages[code]);
    this.name = "CallRequestError";
    this.code = code;
    this.status = status;
    this.field = field;
  }
}

function callURL(baseUrl: string, path: string) {
  const base = new URL(baseUrl);
  if (
    !["http:", "https:"].includes(base.protocol) ||
    base.username ||
    base.password ||
    base.search ||
    base.hash ||
    (base.pathname !== "/" && base.pathname !== "")
  )
    throw new CallRequestError("internal", 500);
  return new URL(path, base);
}
async function callFetch(url: URL, init: RequestInit): Promise<Response> {
  try {
    return await fetch(url, {
      ...init,
      credentials: "include",
      redirect: "error",
      cache: "no-store",
    });
  } catch (error) {
    if (init.signal?.aborted) throw error;
    throw new CallRequestError("provider_unavailable", 503);
  }
}
async function callJSON(response: Response): Promise<unknown> {
  try {
    if (
      !response.headers
        .get("content-type")
        ?.toLowerCase()
        .startsWith("application/json")
    )
      throw new Error("content type");
    const text = await response.text();
    if (text.length > 64 * 1024) throw new Error("body limit");
    return JSON.parse(text);
  } catch {
    throw new CallRequestError("internal", 500);
  }
}
// Reads a JSON result, turning any other status into the contract's error. A plain
// 404 or 405 without a JSON body means the route itself is missing on this server.
async function readResult(
  response: Response,
  status: number,
): Promise<unknown> {
  if (
    (response.status === 404 || response.status === 405) &&
    !response.headers
      .get("content-type")
      ?.toLowerCase()
      .startsWith("application/json")
  ) {
    void response.body?.cancel().catch(() => {});
    throw new CallRequestError("not_found", 404);
  }
  const data = await callJSON(response);
  if (response.status !== status) {
    try {
      const parsed = parseApiError(data, response.status);
      throw new CallRequestError(
        parsed.error.code,
        response.status,
        parsed.error.field,
      );
    } catch (error) {
      if (error instanceof CallRequestError) throw error;
      throw new CallRequestError("internal", 500);
    }
  }
  return data;
}
async function readCall(response: Response, status: number): Promise<Call> {
  const data = await readResult(response, status);
  try {
    return parseCall(data);
  } catch {
    throw new CallRequestError("internal", 500);
  }
}

const UUID =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

// Shared by the call action routes (instructions, approval, control). A 401 here means
// the session that owns the call is gone; a new anonymous session would not own it, so
// it is reported instead of recovered.
export async function postCallAction(
  baseUrl: string,
  callId: string,
  path: string,
  body: unknown,
  status: number,
  signal?: AbortSignal,
): Promise<unknown> {
  if (!UUID.test(callId)) throw new CallRequestError("not_found", 404);
  const response = await callFetch(
    callURL(baseUrl, `/v1/calls/${encodeURIComponent(callId)}/${path}`),
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      signal,
    },
  );
  return readResult(response, status);
}

// A stale concurrent 401 rechecks under this lock before opening a new anonymous
// session. An existing cookie survives page reloads: creation first tries the
// owned endpoint, rather than rotating a valid session on every screen visit.
const sessionQueues = new Map<string, Promise<void>>();
async function recoverSession(
  url: URL,
  post: () => Promise<Response>,
  signal?: AbortSignal,
): Promise<Response> {
  const key = url.origin;
  const previous = sessionQueues.get(key) ?? Promise.resolve();
  let release = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const queued = previous.catch(() => {}).then(() => gate);
  sessionQueues.set(key, queued);
  await previous.catch(() => {});
  try {
    signal?.throwIfAborted();
    let response = await post();
    if (response.status !== 401) return response;
    const session = await callFetch(new URL("/v1/session", url), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: "{}",
      signal,
    });
    if (session.status !== 201)
      throw new CallRequestError("unauthenticated", 401);
    const data = await callJSON(session);
    if (
      !data ||
      typeof data !== "object" ||
      !("account_id" in data) ||
      typeof data.account_id !== "string" ||
      !/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(
        data.account_id,
      )
    )
      throw new CallRequestError("internal", 500);
    response = await post();
    return response;
  } finally {
    release();
    if (sessionQueues.get(key) === queued) sessionQueues.delete(key);
  }
}

export async function createCall(
  baseUrl: string,
  request: CreateCallRequest,
  signal?: AbortSignal,
): Promise<Call> {
  const parsed = parseCreateCallRequest(request);
  const body = JSON.stringify(parsed);
  const url = callURL(baseUrl, "/v1/calls");
  const post = () =>
    callFetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body,
      signal,
    });
  let response = await post();
  if (response.status === 401) {
    await response.body?.cancel();
    response = await recoverSession(url, post, signal);
  }
  const call = await readCall(response, 201);
  const goal = (value: Call["goal"]) => ({
    ...value,
    preferred_at: new Date(value.preferred_at).toISOString(),
  });
  if (
    call.status !== "draft" ||
    call.mode !== parsed.mode ||
    call.destination !== parsed.destination ||
    call.business_name !== parsed.business_name ||
    call.approval_mode !== parsed.approval_mode ||
    JSON.stringify(goal(call.goal)) !== JSON.stringify(goal(parsed.goal))
  )
    throw new CallRequestError("internal", 500);
  return call;
}

export async function getCall(
  baseUrl: string,
  id: string,
  signal?: AbortSignal,
): Promise<Call> {
  if (
    !/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(
      id,
    )
  )
    throw new CallRequestError("not_found", 404);
  const call = await readCall(
    await callFetch(callURL(baseUrl, `/v1/calls/${encodeURIComponent(id)}`), {
      method: "GET",
      signal,
    }),
    200,
  );
  if (call.id.toLowerCase() !== id.toLowerCase())
    throw new CallRequestError("internal", 500);
  return call;
}
