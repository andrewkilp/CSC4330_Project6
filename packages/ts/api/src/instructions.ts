// Private mid-call instructions (#27). The server applies them under an atomic context
// revision (#28); the client only sends them and reports what the server answers.
import { CallRequestError, postCallAction } from "./calls.ts";
import {
  ContractValidationError,
  type Instruction,
  parseCreateInstructionRequest,
  parseInstruction,
} from "./types.ts";

export interface InstructionAttempt {
  readonly text: string;
  readonly expectedRevision: number;
  readonly idempotencyKey: string;
}

// A retry of the same text at the same revision reuses its key, so a request that did
// reach the server cannot apply twice. Edited text or a newer revision is a new request.
export function nextInstructionAttempt(
  previous: InstructionAttempt | null,
  text: string,
  revision: number,
  makeKey: () => string,
): InstructionAttempt {
  if (
    previous &&
    previous.text === text &&
    previous.expectedRevision === revision
  )
    return previous;
  return { text, expectedRevision: revision, idempotencyKey: makeKey() };
}

export async function sendInstruction(
  baseUrl: string,
  callId: string,
  attempt: InstructionAttempt,
  signal?: AbortSignal,
): Promise<Instruction> {
  const body = parseCreateInstructionRequest({
    text: attempt.text,
    expected_revision: attempt.expectedRevision,
    idempotency_key: attempt.idempotencyKey,
  });
  const data = await postCallAction(
    baseUrl,
    callId,
    "instructions",
    body,
    201,
    signal,
  );
  try {
    const instruction = parseInstruction(data);
    if (instruction.call_id.toLowerCase() !== callId.toLowerCase())
      throw new Error("call");
    return instruction;
  } catch {
    throw new CallRequestError("internal", 500);
  }
}

export type InstructionProblem =
  | { readonly kind: "stale"; readonly message: string }
  | { readonly kind: "retry"; readonly message: string }
  | { readonly kind: "blocked"; readonly message: string };

// Turns a failed send into what the composer should offer. The typed text is always kept.
export function instructionProblem(error: unknown): InstructionProblem {
  if (error instanceof ContractValidationError)
    return {
      kind: "blocked",
      message: "Instructions must be 1 to 1000 characters.",
    };
  if (!(error instanceof CallRequestError))
    return { kind: "retry", message: "Not sent. Try again." };
  if (error.code === "conflict" && error.field === "expected_revision")
    return {
      kind: "stale",
      message:
        "The call moved on while you were typing. Your instruction is kept. Send it against the newest context.",
    };
  if (error.code === "conflict")
    return {
      kind: "blocked",
      message: "Instructions can be sent only while the call is connected.",
    };
  if (error.code === "unauthenticated")
    return {
      kind: "blocked",
      message: "Your session expired. Return to your calls to sign in again.",
    };
  if (error.code === "not_found")
    return {
      kind: "blocked",
      message: "This call cannot take instructions on this server yet.",
    };
  return {
    kind: "retry",
    message: "Not sent. Check the connection and try again.",
  };
}
