// GET /v1/providers client (#40). The server reports availability booleans and fixed
// user-safe reasons only; this client never sends or receives provider keys and never
// changes a call's mode. Choosing a mock call is always an explicit operator action.
import {
  CALL_MODES,
  type ProviderAvailability,
  type ProviderKind,
  type Providers,
} from "./types.ts";
import {
  boolean,
  choice,
  invalid,
  object,
  optional,
  text,
} from "./validate.ts";

// Reasons are short, fixed sentences written by the server.
export const PROVIDER_REASON_MAX = 200;

const PROVIDER_ORDER: readonly ProviderKind[] = [
  "telephony",
  "stt",
  "llm",
  "tts",
];

function availability(value: unknown, field: string): ProviderAvailability {
  const v = object(value, field);
  const available = boolean(v.available, `${field}.available`);
  const reason = optional(v.reason, (x) =>
    text(x, `${field}.reason`, PROVIDER_REASON_MAX),
  );
  // An unavailable provider must explain itself; an available one has nothing to say.
  if (available === (reason !== undefined)) return invalid(`${field}.reason`);
  return reason === undefined ? { available } : { available, reason };
}

export function parseProviders(value: unknown): Providers {
  const v = object(value, "providers");
  const parsed = {
    mode: choice(v.mode, "providers.mode", CALL_MODES),
    telephony: availability(v.telephony, "providers.telephony"),
    stt: availability(v.stt, "providers.stt"),
    llm: availability(v.llm, "providers.llm"),
    tts: availability(v.tts, "providers.tts"),
  };
  // The server offers real calling only when every provider is available.
  if (
    parsed.mode === "real" &&
    PROVIDER_ORDER.some((kind) => !parsed[kind].available)
  )
    return invalid("providers.mode");
  return parsed;
}

// True only when the server says a real call can start now. The start route still
// enforces this; the client check only decides what to offer.
export function canStartRealCall(providers: Providers): boolean {
  return (
    providers.mode === "real" &&
    PROVIDER_ORDER.every((kind) => providers[kind].available)
  );
}

// Reasons to show beside a disabled real-call option, in a stable order.
export function realCallBlockers(
  providers: Providers,
): readonly { readonly provider: ProviderKind; readonly reason: string }[] {
  return PROVIDER_ORDER.flatMap((provider) => {
    const a = providers[provider];
    return a.available || !a.reason ? [] : [{ provider, reason: a.reason }];
  });
}

export async function getProviders(
  baseUrl: string,
  signal?: AbortSignal,
): Promise<Providers> {
  let response: Response;
  try {
    response = await fetch(new URL("/v1/providers", baseUrl), {
      signal,
      headers: { Accept: "application/json" },
    });
  } catch (error) {
    if (signal?.aborted) throw error;
    throw new Error("The server is unavailable. Try again.");
  }
  if (!response.ok)
    throw new Error("Provider status is unavailable. Try again.");
  try {
    return parseProviders(await response.json());
  } catch {
    throw new Error("The server returned an unexpected response.");
  }
}
