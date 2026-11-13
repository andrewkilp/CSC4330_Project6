// Browser event-stream subscription (#22, web only in the MVP). Frames are validated by
// the reducer in events.ts. On any error the caller closes the stream, hydrates a fresh
// snapshot and resubscribes after its last_seq, so a gap or expired cursor never
// silently skips an event. Native platforms need a fetch-based transport (#22).
export interface StreamHandle {
  close(): void;
}

export function subscribeCallEvents(
  baseUrl: string,
  callId: string,
  afterSeq: number,
  onEvent: (decoded: unknown) => void,
  onError: () => void,
): StreamHandle {
  const Source = (globalThis as { EventSource?: typeof EventSource })
    .EventSource;
  if (!Source) {
    queueMicrotask(onError);
    return { close() {} };
  }
  const url = new URL(
    `/v1/calls/${encodeURIComponent(callId)}/events`,
    baseUrl,
  );
  url.searchParams.set("after_seq", String(afterSeq));
  const source = new Source(url.toString(), { withCredentials: true });
  let closed = false;
  const handle = (message: MessageEvent<string>) => {
    if (closed) return;
    try {
      onEvent(JSON.parse(message.data));
    } catch {
      closed = true;
      source.close();
      onError();
    }
  };
  for (const type of [
    "call.updated",
    "turn.updated",
    "instruction.updated",
    "response.updated",
    "summary.ready",
    "provider.error",
    "usage.updated",
  ])
    source.addEventListener(type, handle as EventListener);
  source.onerror = () => {
    if (closed) return;
    closed = true;
    source.close();
    onError();
  };
  return {
    close() {
      closed = true;
      source.close();
    },
  };
}
