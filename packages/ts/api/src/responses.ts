// Approval of one suggested response (#32, client for the #29 route). The server decides
// whether the approval stands; the client sends the response id, the revision it saw and
// any edit, then shows the version the server confirms.
import { CallRequestError, postCallAction } from "./calls.ts";
import {
  ContractValidationError,
  parseApproveResponseRequest,
  parseResponse,
  type Response,
} from "./types.ts";

export async function approveResponse(
  baseUrl: string,
  callId: string,
  responseId: string,
  approval: {
    readonly contextRevision: number;
    readonly editedText?: string;
    readonly idempotencyKey: string;
  },
  signal?: AbortSignal,
): Promise<Response> {
  const body = parseApproveResponseRequest({
    context_revision: approval.contextRevision,
    ...(approval.editedText !== undefined
      ? { edited_text: approval.editedText }
      : {}),
    idempotency_key: approval.idempotencyKey,
  });
  const data = await postCallAction(
    baseUrl,
    callId,
    `responses/${encodeURIComponent(responseId)}/approve`,
    body,
    200,
    signal,
  );
  try {
    const response = parseResponse(data);
    if (
      response.id.toLowerCase() !== responseId.toLowerCase() ||
      response.call_id.toLowerCase() !== callId.toLowerCase()
    )
      throw new Error("response");
    return response;
  } catch {
    throw new CallRequestError("internal", 500);
  }
}

export type ApprovalProblem =
  | { readonly kind: "stale"; readonly message: string }
  | { readonly kind: "retry"; readonly message: string }
  | { readonly kind: "blocked"; readonly message: string };

export function approvalProblem(error: unknown): ApprovalProblem {
  if (error instanceof ContractValidationError)
    return {
      kind: "blocked",
      message: "A reply must be 1 to 1500 characters.",
    };
  if (!(error instanceof CallRequestError))
    return { kind: "retry", message: "Not approved. Try again." };
  if (error.code === "conflict" && error.field === "context_revision")
    return {
      kind: "stale",
      message:
        "This suggestion is out of date. Review the newest suggestion before approving.",
    };
  if (error.code === "conflict" && error.field === "status")
    return {
      kind: "stale",
      message: "This suggestion can no longer be approved.",
    };
  if (error.code === "unauthenticated")
    return {
      kind: "blocked",
      message: "Your session expired. Return to your calls to sign in again.",
    };
  if (error.code === "rate_limited")
    return {
      kind: "blocked",
      message: "This call has reached its approval limit.",
    };
  if (error.code === "not_found")
    return {
      kind: "blocked",
      message: "This suggestion was not found. Wait for the next one.",
    };
  return {
    kind: "retry",
    message: "Not approved. Check the connection and try again.",
  };
}
