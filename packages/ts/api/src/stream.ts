import {
  createEventState,
  type EventState,
  expireCursor,
  hydrateSnapshot,
  parseEvent,
  reduceEvent,
} from "./events.ts";

export type StreamStatus =
  | "connecting"
  | "live"
  | "reconnecting"
  | "resyncing"
  | "offline"
  | "unauthenticated"
  | "not_found"
  | "error";
export interface StreamUpdate {
  state: EventState;
  status: StreamStatus;
}
export interface StreamOptions {
  baseUrl: string;
  callId: string;
  onUpdate: (update: StreamUpdate) => void;
  // Expo callers inject expo/fetch; web/Node callers use standards-based fetch.
  fetch?: typeof globalThis.fetch;
  retryMs?: number;
  idleMs?: number;
}
class StreamFailure extends Error {
  readonly kind: "unauthenticated" | "not_found" | "protocol" | "retry";
  constructor(kind: StreamFailure["kind"]) {
    super("The call stream could not be loaded.");
    this.kind = kind;
  }
}

// Bounded SSE framing independent of EventSource and platform transport. Partial
// UTF-8 characters and CRLF delimiters may span arbitrary network chunks.
export class CallEventDecoder {
  private decoder = new TextDecoder("utf-8", { fatal: true });
  private pending = "";
  private lines: string[] = [];
  private size = 0;
  push(chunk: Uint8Array, emit: (event: unknown) => void) {
    if (chunk.byteLength > 256 * 1024) throw new StreamFailure("protocol");
    this.pending += this.decoder.decode(chunk, { stream: true });
    for (;;) {
      const match = /[\r\n]/.exec(this.pending);
      if (!match) break;
      const index = match.index;
      if (this.pending[index] === "\r" && index === this.pending.length - 1)
        break;
      const line = this.pending.slice(0, index);
      const length =
        this.pending[index] === "\r" && this.pending[index + 1] === "\n"
          ? 2
          : 1;
      this.pending = this.pending.slice(index + length);
      this.size += line.length;
      if (this.size > 128 * 1024) throw new StreamFailure("protocol");
      if (!line) {
        this.dispatch(emit);
        this.lines = [];
        this.size = 0;
      } else if (!line.startsWith(":")) this.lines.push(line);
    }
    if (this.pending.length > 128 * 1024) throw new StreamFailure("protocol");
  }
  private dispatch(emit: (event: unknown) => void) {
    const data: string[] = [];
    let id: string | undefined, type: string | undefined;
    for (const line of this.lines) {
      const colon = line.indexOf(":"),
        name = colon < 0 ? line : line.slice(0, colon);
      let value = colon < 0 ? "" : line.slice(colon + 1);
      if (value.startsWith(" ")) value = value.slice(1);
      if (name === "data") data.push(value);
      if (name === "id") {
        if (id !== undefined) throw new StreamFailure("protocol");
        id = value;
      }
      if (name === "event") {
        if (type !== undefined) throw new StreamFailure("protocol");
        type = value;
      }
    }
    if (!data.length) return;
    if (!id || !/^[1-9][0-9]*$/.test(id) || data.join("\n").length > 64 * 1024)
      throw new StreamFailure("protocol");
    const event = parseEvent(JSON.parse(data.join("\n")));
    if (String(event.seq) !== id || type !== event.type)
      throw new StreamFailure("protocol");
    emit(event);
  }
}

export function subscribeCall(options: StreamOptions) {
  const base = new URL(options.baseUrl);
  if (
    !["http:", "https:"].includes(base.protocol) ||
    base.username ||
    base.password ||
    base.search ||
    base.hash ||
    base.pathname !== "/"
  )
    throw new Error("Invalid call service URL.");
  let state = createEventState(options.callId),
    active = true,
    closed = false,
    generation = 0;
  let controller: AbortController | undefined,
    timer: ReturnType<typeof setTimeout> | undefined;
  let status: StreamStatus = "connecting";
  const transport = options.fetch ?? globalThis.fetch;
  const notify = (next: StreamStatus) => {
    status = next;
    options.onUpdate({ state, status });
  };
  const url = (path: string) =>
    new URL(`/v1/calls/${encodeURIComponent(state.callId)}/${path}`, base);
  function stop() {
    generation++;
    controller?.abort();
    if (timer) clearTimeout(timer);
    timer = undefined;
  }
  async function request(
    path: string,
    signal: AbortSignal,
    headers: Record<string, string>,
  ) {
    const response = await transport(url(path), {
      method: "GET",
      headers,
      credentials: "include",
      redirect: "error",
      cache: "no-store",
      signal,
    });
    if (response.status === 401 || response.status === 403) {
      void response.body?.cancel().catch(() => {});
      throw new StreamFailure("unauthenticated");
    }
    if (response.status === 404) {
      void response.body?.cancel().catch(() => {});
      throw new StreamFailure("not_found");
    }
    return response;
  }
  async function snapshot(signal: AbortSignal) {
    const response = await request("snapshot", signal, {
      Accept: "application/json",
    });
    if (response.status !== 200) {
      void response.body?.cancel().catch(() => {});
      throw new StreamFailure(
        response.status >= 500 || response.status === 429
          ? "retry"
          : "protocol",
      );
    }
    if (
      !response.headers.get("content-type")?.startsWith("application/json") ||
      !response.body
    )
      throw new StreamFailure("protocol");
    const reader = response.body.getReader(),
      decoder = new TextDecoder("utf-8", { fatal: true });
    let text = "";
    try {
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        text += decoder.decode(value, { stream: true });
        if (text.length > 4 * 1024 * 1024) throw new StreamFailure("protocol");
      }
      text += decoder.decode();
    } finally {
      await reader.cancel().catch(() => {});
      reader.releaseLock();
    }
    return hydrateSnapshot(state, JSON.parse(text));
  }
  async function run(resync: boolean, attempt = 0) {
    if (closed || !active) return;
    const version = ++generation,
      abort = new AbortController();
    controller = abort;
    let idle: ReturnType<typeof setTimeout> | undefined;
    const arm = () => {
      if (idle) clearTimeout(idle);
      idle = setTimeout(() => abort.abort(), options.idleMs ?? 45000);
    };
    const current = () => !closed && active && version === generation;
    try {
      arm();
      if (resync || !state.call) {
        notify(state.call ? "resyncing" : "connecting");
        const next = await snapshot(abort.signal);
        if (!current()) return;
        state = next;
      }
      const response = await request(
        `events?after_seq=${state.lastSeq}`,
        abort.signal,
        { Accept: "text/event-stream", "Last-Event-ID": String(state.lastSeq) },
      );
      if (!current()) {
        void response.body?.cancel().catch(() => {});
        return;
      }
      if (response.status === 409) {
        void response.body?.cancel().catch(() => {});
        state = expireCursor(state);
        throw new StreamFailure("retry");
      }
      if (response.status !== 200) {
        void response.body?.cancel().catch(() => {});
        throw new StreamFailure(
          response.status >= 500 || response.status === 429
            ? "retry"
            : "protocol",
        );
      }
      if (
        !response.headers
          .get("content-type")
          ?.startsWith("text/event-stream") ||
        !response.body
      )
        throw new StreamFailure("protocol");
      const reader = response.body.getReader(),
        decoder = new CallEventDecoder();
      notify("live");
      try {
        for (;;) {
          const { value, done } = await reader.read();
          if (!current()) return;
          if (done) break;
          arm();
          decoder.push(value, (event) => {
            if (current()) {
              state = reduceEvent(state, event);
              notify(state.recovery ? "resyncing" : "live");
            }
          });
          if (state.recovery) break;
        }
      } finally {
        await reader.cancel().catch(() => {});
        reader.releaseLock();
      }
      if (current()) throw new StreamFailure("retry");
    } catch (error) {
      if (!current()) return;
      if (
        error instanceof StreamFailure &&
        (error.kind === "unauthenticated" || error.kind === "not_found")
      ) {
        notify(error.kind);
        active = false;
        return;
      }
      if (
        !(error instanceof StreamFailure) &&
        !abort.signal.aborted &&
        (error instanceof SyntaxError ||
          (error instanceof TypeError && error.message.includes("encoded")) ||
          (error instanceof Error && error.name === "ContractValidationError"))
      ) {
        notify("error");
        active = false;
        return;
      }
      if (error instanceof StreamFailure && error.kind === "protocol") {
        notify("error");
        active = false;
        return;
      }
      notify(state.recovery ? "resyncing" : "reconnecting");
      timer = setTimeout(
        () => {
          timer = undefined;
          void run(!!state.recovery, Math.min(attempt + 1, 4));
        },
        Math.min((options.retryMs ?? 1000) * 2 ** attempt, 8000),
      );
    } finally {
      if (idle) clearTimeout(idle);
      abort.abort();
    }
  }
  void run(true);
  return {
    close() {
      if (closed) return;
      closed = true;
      stop();
    },
    setActive(value: boolean) {
      if (closed || value === active) return;
      stop();
      active = value;
      if (value) void run(true);
      else notify("offline");
    },
    retry() {
      if (closed) return;
      stop();
      active = true;
      void run(true);
    },
    getSnapshot: () => ({ state, status }),
  };
}
