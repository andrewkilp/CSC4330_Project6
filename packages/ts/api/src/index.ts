export * from "./events.ts";
export * from "./types.ts";

export interface Health {
  status: "ok";
  mode: "template";
}

// No provider secrets belong in this client package.
export async function getHealth(
  baseUrl: string,
  signal?: AbortSignal,
): Promise<Health> {
  const response = await fetch(new URL("/health", baseUrl), { signal });
  if (!response.ok) throw new Error("The server is unavailable. Try again.");
  const data: unknown = await response.json();
  if (
    !data ||
    typeof data !== "object" ||
    !("status" in data) ||
    data.status !== "ok" ||
    !("mode" in data) ||
    data.mode !== "template"
  )
    throw new Error("The server returned an unexpected response.");
  return { status: "ok", mode: "template" };
}
