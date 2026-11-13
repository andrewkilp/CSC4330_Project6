// Live call (L1): transcript, current goal, suggested reply with edit/approve, private
// instructions, pause/resume and end, then the evidence-backed summary (H2). All
// state comes from the server's durable event stream through the shared reducer
// (#9); the screen never invents transcript text or treats a suggestion as spoken.
import {
  ApiError,
  approveResponse,
  type Call,
  controlCall,
  createEventState,
  type EventState,
  getSnapshot,
  hydrateSnapshot,
  listMockScenarios,
  newIdempotencyKey,
  reduceEvent,
  type StreamHandle,
  sendInstruction,
  startCall,
  subscribeCallEvents,
  type Turn,
} from "@switchboard/api";
import { Link, useLocalSearchParams } from "expo-router";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import {
  Badge,
  Button,
  mono,
  Shell,
  serif,
  ui,
  useWide,
} from "../components/ui";
import { apiBase, describeGoal } from "../config";
import { theme as t } from "../theme";

function clock(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  return `${String(Math.floor(total / 60)).padStart(2, "0")}:${String(total % 60).padStart(2, "0")}`;
}

const STATUS: Record<
  Call["status"],
  { label: string; tone: "green" | "vermilion" | "brass" | "muted" }
> = {
  draft: { label: "READY", tone: "muted" },
  dialing: { label: "RINGING", tone: "brass" },
  connected: { label: "LIVE", tone: "green" },
  paused: { label: "AI PAUSED", tone: "brass" },
  ended: { label: "ENDED", tone: "muted" },
  failed: { label: "FAILED", tone: "vermilion" },
  cancelled: { label: "CANCELLED", tone: "muted" },
};

export default function LiveCall() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const wide = useWide();
  const [state, setState] = useState<EventState | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [instruction, setInstruction] = useState("");
  const [draft, setDraft] = useState<{ id: string; text: string } | null>(null);
  const [hints, setHints] = useState<readonly string[]>([]);
  const [busy, setBusy] = useState("");
  const [now, setNow] = useState(Date.now());
  const connectedAt = useRef<number | null>(null);
  const stream = useRef<StreamHandle | null>(null);
  const keys = useRef(new Map<string, string>());
  const transcriptRef = useRef<ScrollView>(null);

  // One idempotency key per distinct action, reused on retry.
  const keyFor = (action: string) => {
    let k = keys.current.get(action);
    if (!k) {
      k = newIdempotencyKey();
      keys.current.set(action, k);
    }
    return k;
  };

  const connect = useCallback(() => {
    if (!id) return;
    let cancelled = false;
    stream.current?.close();
    getSnapshot(apiBase(), id)
      .then((snapshot) => {
        if (cancelled) return;
        const hydrated = hydrateSnapshot(createEventState(id), snapshot);
        setState(hydrated);
        stream.current = subscribeCallEvents(
          apiBase(),
          id,
          hydrated.lastSeq,
          (event) => {
            setState((prev) => {
              if (!prev) return prev;
              try {
                const next = reduceEvent(prev, event);
                if (next.recovery) setTimeout(connect, 300);
                return next;
              } catch {
                // An event the contract rejects never enters the projection;
                // resynchronize from a fresh snapshot instead.
                setTimeout(connect, 300);
                return prev;
              }
            });
          },
          () => {
            if (!cancelled) setTimeout(connect, 1000);
          },
        );
      })
      .catch((e) => {
        if (cancelled) return;
        if (e instanceof ApiError && (e.status === 401 || e.status === 404)) {
          setError("This call is not available in your session.");
          return;
        }
        setTimeout(connect, 1500);
      });
    return () => {
      cancelled = true;
    };
  }, [id]);

  useEffect(() => {
    const stop = connect();
    const timer = setInterval(() => setNow(Date.now()), 500);
    return () => {
      stop?.();
      stream.current?.close();
      clearInterval(timer);
    };
  }, [connect]);

  const call = state?.call ?? null;
  const businessName = call?.business_name;
  useEffect(() => {
    if (!businessName) return;
    listMockScenarios(apiBase())
      .then((list) =>
        setHints(
          list.find((s) => s.business_name === businessName)?.instructions ??
            [],
        ),
      )
      .catch(() => {});
  }, [businessName]);

  if (call && connectedAt.current === null && call.status === "connected")
    connectedAt.current = Date.parse(call.updated_at);
  const terminal =
    call?.status === "ended" ||
    call?.status === "failed" ||
    call?.status === "cancelled";
  // After a reload the connect time is unknown; the transcript still bounds it.
  const lastHeard = state
    ? Math.max(
        0,
        ...Object.values(state.turns).map(
          (turn) => turn.ended_at_ms ?? turn.started_at_ms,
        ),
      )
    : 0;
  const elapsed =
    connectedAt.current === null
      ? lastHeard
      : (terminal ? Date.parse(call?.updated_at ?? "") : now) -
        connectedAt.current;

  const response = state?.currentResponseId
    ? state.responses[state.currentResponseId]
    : undefined;
  // Load a new suggestion into the editor once; later edits stay the operator's.
  const suggestionId = response?.status === "suggested" ? response.id : null;
  const suggestionText = response?.status === "suggested" ? response.text : "";
  useEffect(() => {
    if (suggestionId)
      setDraft((prev) =>
        prev?.id === suggestionId
          ? prev
          : { id: suggestionId, text: suggestionText },
      );
  }, [suggestionId, suggestionText]);

  const turns: Turn[] = state
    ? state.turnOrder
        .map((tid) => state.turns[tid])
        .filter((x): x is Turn => !!x)
    : [];
  const businessLabel =
    call?.goal.kind === "appointment" ? "BUSINESS" : "RESTAURANT";
  const lastFailed = state
    ? Object.values(state.responses).some(
        (r) =>
          r.status === "failed" && r.context_revision === state.contextRevision,
      ) && !response
    : false;

  async function run(label: string, action: () => Promise<unknown>) {
    setBusy(label);
    setError("");
    try {
      await action();
      return true;
    } catch (e) {
      setError(
        e instanceof Error ? e.message : "That did not work. Try again.",
      );
      return false;
    } finally {
      setBusy("");
    }
  }

  const place = () =>
    run("start", () => startCall(apiBase(), id, keyFor("start")));
  const control = (action: "pause" | "resume" | "end") =>
    run(action, async () => {
      await controlCall(apiBase(), id, {
        action,
        idempotency_key: keyFor(`${action}:${state?.lastSeq}`),
      });
      setNotice(
        action === "pause"
          ? "AI paused. Queued speech was cleared; the line stays open."
          : action === "resume"
            ? "AI resumed with the latest transcript and instructions."
            : "Call ended.",
      );
    });
  const instruct = async () => {
    const text = instruction.trim();
    if (!text || !state) return;
    const ok = await run("instruction", () =>
      sendInstruction(apiBase(), id, {
        text,
        expected_revision: state.contextRevision,
        idempotency_key: keyFor(`instruction:${state.contextRevision}:${text}`),
      }),
    );
    if (ok) {
      setInstruction("");
      setNotice("Instruction applied. The suggestion is being revised.");
    }
  };
  const approve = () => {
    if (!response || !draft || !state) return;
    const edited =
      draft.text.trim() !== response.text.trim()
        ? draft.text.trim()
        : undefined;
    return run("approve", () =>
      approveResponse(apiBase(), id, response.id, {
        context_revision: state.contextRevision,
        ...(edited ? { edited_text: edited } : {}),
        idempotency_key: keyFor(`approve:${response.id}:${edited ?? ""}`),
      }),
    );
  };

  if (!call) {
    return (
      <Shell>
        <Text style={ui.help}>{error || "Loading call…"}</Text>
        <Link href="/" style={s.link}>
          Back to calls
        </Link>
      </Shell>
    );
  }

  const status = STATUS[call.status];
  const kind = call.goal.kind === "restaurant" ? "Reservation" : "Appointment";
  return (
    <Shell scroll={!wide}>
      <View style={s.header}>
        <View style={{ flex: 1, minWidth: 260 }}>
          <Text accessibilityRole="header" style={s.title}>
            {kind} at {call.business_name}
          </Text>
          <Text style={ui.label}>
            {call.goal.kind === "restaurant"
              ? "RESTAURANT RESERVATION"
              : "APPOINTMENT"}{" "}
            · MOCK CALL · FICTIONAL BUSINESS
          </Text>
        </View>
        <View style={ui.row}>
          <Text
            accessibilityLabel={`Call status ${status.label}`}
            style={[
              s.status,
              {
                backgroundColor:
                  t[status.tone === "muted" ? "muted" : status.tone],
              },
            ]}
          >
            ● {status.label}
          </Text>
          <Text
            style={s.clock}
            accessibilityLabel={`Elapsed ${clock(elapsed)}`}
          >
            {clock(elapsed)}
          </Text>
        </View>
      </View>
      {(!!error || !!notice) && (
        <Text
          accessibilityLiveRegion="polite"
          style={error ? ui.error : ui.help}
        >
          {error || notice}
        </Text>
      )}
      <View style={[s.workspace, wide && { flexDirection: "row", flex: 1 }]}>
        <ScrollView
          ref={transcriptRef}
          style={[s.transcript, wide && { flex: 1 }]}
          contentContainerStyle={{
            gap: 18,
            paddingVertical: 18,
            paddingRight: 16,
          }}
          onContentSizeChange={() =>
            transcriptRef.current?.scrollToEnd({ animated: true })
          }
          accessibilityLabel="Transcript"
        >
          {call.status === "draft" && (
            <View style={[ui.panel, { gap: 12 }]}>
              <Text style={ui.subheading}>Ready to call</Text>
              <Text style={ui.body}>
                Placing this mock call connects to a scripted, fictional
                business. Nothing is dialed. You approve every reply before the
                assistant speaks.
              </Text>
              <View style={ui.row}>
                <Button
                  label={busy === "start" ? "Placing…" : "Place mock call"}
                  disabled={!!busy}
                  onPress={() => void place()}
                />
              </View>
            </View>
          )}
          {call.status === "dialing" && (
            <Text style={ui.help}>Ringing {call.business_name}…</Text>
          )}
          {turns.map((turn) => {
            const ai = turn.speaker === "assistant";
            return (
              <View key={turn.id} style={s.turn}>
                <Text style={s.time}>[{clock(turn.started_at_ms)}]</Text>
                <View
                  style={[
                    s.turnBody,
                    { borderLeftColor: ai ? t.green : t.vermilion },
                  ]}
                >
                  <Badge
                    label={ai ? "AI" : businessLabel}
                    tone={ai ? "green" : "vermilion"}
                  />
                  <Text
                    style={[ui.body, !turn.is_final && s.partial]}
                    accessibilityLabel={`${ai ? "AI" : businessLabel}${turn.is_final ? "" : ", still speaking"}: ${turn.text}`}
                  >
                    {turn.text}
                    {!turn.is_final && " …"}
                  </Text>
                </View>
              </View>
            );
          })}
          {response &&
            (response.status === "approved" ||
              response.status === "speaking") && (
              <View style={s.turn}>
                <Text style={s.time}>[{clock(elapsed)}]</Text>
                <View style={[s.turnBody, { borderLeftColor: t.line }]}>
                  <Badge label="AI · SPEAKING" tone="muted" />
                  <Text style={[ui.body, s.partial]}>{response.text}</Text>
                </View>
              </View>
            )}
        </ScrollView>
        <ScrollView
          style={[
            s.rail,
            wide && {
              width: 360,
              flexGrow: 0,
              borderLeftWidth: 1,
              borderColor: t.line,
            },
          ]}
          contentContainerStyle={{
            gap: 16,
            padding: wide ? 18 : 0,
            paddingVertical: 18,
          }}
        >
          <Text style={ui.subheading}>Current goal</Text>
          <View style={[ui.panel, { borderColor: t.green }]}>
            <Text style={ui.body}>{describeGoal(call.goal)}</Text>
            <Text style={ui.help}>
              For {call.goal.contact_name}
              {call.goal.constraints.length
                ? ` · ${call.goal.constraints.join(" · ")}`
                : ""}
            </Text>
          </View>
          {terminal ? (
            <SummaryPanel state={state as EventState} />
          ) : (
            <>
              <Text style={ui.subheading}>Suggested reply</Text>
              {response?.status === "suggested" && draft?.id === response.id ? (
                <View style={{ gap: 10 }}>
                  {response.needs_approval && response.reason && (
                    <Text style={s.reason}>
                      Needs your approval: {response.reason}
                    </Text>
                  )}
                  <TextInput
                    accessibilityLabel="Suggested reply, editable before approval"
                    multiline
                    value={draft.text}
                    onChangeText={(text) => setDraft({ id: response.id, text })}
                    style={[ui.input, s.reply]}
                  />
                  <View style={ui.row}>
                    <Button
                      label={
                        busy === "approve"
                          ? "Approving…"
                          : draft.text.trim() !== response.text.trim()
                            ? "Approve edited reply"
                            : "Approve"
                      }
                      disabled={
                        !!busy ||
                        !draft.text.trim() ||
                        call.status !== "connected"
                      }
                      onPress={() => void approve()}
                    />
                  </View>
                  <Text style={ui.help}>
                    Nothing is said until you approve.
                  </Text>
                </View>
              ) : response?.status === "generating" ? (
                <Text style={ui.help}>Drafting a reply…</Text>
              ) : response?.status === "approved" ||
                response?.status === "speaking" ? (
                <Text style={ui.help}>Approved. Speaking on the call…</Text>
              ) : call.status === "paused" ? (
                <Text style={ui.help}>
                  AI paused. Resume to get a fresh suggestion.
                </Text>
              ) : lastFailed ? (
                <Text style={ui.help}>
                  No scripted reply for this point in the mock call. Send an
                  instruction or end the call.
                </Text>
              ) : (
                <Text style={ui.help}>
                  {call.status === "connected"
                    ? "Listening to the business…"
                    : "Waiting for the call to connect."}
                </Text>
              )}
              {Object.values(state?.instructions ?? {}).length > 0 && (
                <>
                  <Text style={ui.label}>YOUR PRIVATE INSTRUCTIONS</Text>
                  {Object.values(state?.instructions ?? {}).map((ins) => (
                    <Text key={ins.id} style={ui.help}>
                      ✓ {ins.text} ({ins.status})
                    </Text>
                  ))}
                </>
              )}
            </>
          )}
        </ScrollView>
      </View>
      {!terminal && call.status !== "draft" && (
        <View style={s.composer}>
          <View style={ui.row}>
            <Text style={ui.subheading}>Guide the next response</Text>
            <Text style={ui.label}>
              ● PRIVATE · THE AI WAITS FOR YOUR APPROVAL
            </Text>
          </View>
          <View style={[ui.row, { alignItems: "stretch" }]}>
            <TextInput
              accessibilityLabel="Private instruction for the AI"
              placeholder="Tell the AI what to do next"
              placeholderTextColor={t.muted}
              value={instruction}
              onChangeText={setInstruction}
              onSubmitEditing={() => void instruct()}
              style={[ui.input, { flex: 1, minWidth: 240 }]}
            />
            <Button
              label={busy === "instruction" ? "Sending…" : "Send instruction"}
              disabled={
                !!busy ||
                !instruction.trim() ||
                (call.status !== "connected" && call.status !== "paused")
              }
              onPress={() => void instruct()}
            />
            <Button
              kind="secondary"
              label={call.status === "paused" ? "Resume AI" : "Pause AI"}
              disabled={
                !!busy ||
                (call.status !== "connected" && call.status !== "paused")
              }
              onPress={() =>
                void control(call.status === "paused" ? "resume" : "pause")
              }
            />
            <Button
              kind="danger"
              label="End call"
              disabled={!!busy}
              onPress={() => void control("end")}
            />
          </View>
          {hints.length > 0 && (
            <View style={ui.row}>
              <Text style={ui.help}>Scripted for this mock:</Text>
              {hints.map((h) => (
                <Pressable
                  key={h}
                  accessibilityRole="button"
                  accessibilityLabel={`Use instruction: ${h}`}
                  onPress={() => setInstruction(h)}
                  style={s.hint}
                >
                  <Text style={{ color: t.green, fontSize: 13 }}>{h}</Text>
                </Pressable>
              ))}
            </View>
          )}
        </View>
      )}
      {terminal && (
        <Link href="/" style={s.link}>
          Back to calls
        </Link>
      )}
    </Shell>
  );
}

function SummaryPanel({ state }: { state: EventState }) {
  const summary = state.summary;
  if (!summary) return <Text style={ui.help}>Preparing the call summary…</Text>;
  const d = summary.confirmed_details;
  const outcome = {
    confirmed: "Confirmed",
    declined: "Declined",
    needs_follow_up: "Needs follow-up",
    failed: "Failed",
    cancelled: "Cancelled",
  }[summary.outcome];
  const when = d.scheduled_at
    ? new Date(d.scheduled_at).toLocaleString("en-US", {
        weekday: "long",
        hour: "numeric",
        minute: "2-digit",
        month: "short",
        day: "numeric",
        timeZone: d.timezone,
      })
    : undefined;
  return (
    <View style={{ gap: 12 }}>
      <Text style={ui.subheading}>Summary</Text>
      <Badge
        label={outcome.toUpperCase()}
        tone={summary.outcome === "confirmed" ? "green" : "brass"}
      />
      {when && <Text style={ui.body}>{when}</Text>}
      {d.party_size !== undefined && (
        <Text style={ui.body}>Party of {d.party_size}</Text>
      )}
      {d.service && <Text style={ui.body}>{d.service}</Text>}
      {d.confirmation_reference && (
        <Text style={ui.body}>Confirmation {d.confirmation_reference}</Text>
      )}
      {summary.evidence_turn_ids.map((tid) => (
        <Text key={tid} style={s.evidence}>
          “{state.turns[tid]?.text}”
        </Text>
      ))}
      {summary.unresolved_questions.length > 0 && (
        <Text style={ui.label}>STILL OPEN</Text>
      )}
      {summary.unresolved_questions.map((q) => (
        <Text key={q} style={ui.help}>
          · {q}
        </Text>
      ))}
      {summary.next_steps.length > 0 && (
        <Text style={ui.label}>NEXT STEPS</Text>
      )}
      {summary.next_steps.map((q) => (
        <Text key={q} style={ui.help}>
          · {q}
        </Text>
      ))}
      <Text style={ui.help}>
        Mock summary from the scripted scenario. Verify before relying on it.
      </Text>
    </View>
  );
}

const s = StyleSheet.create({
  header: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 16,
    alignItems: "center",
  },
  title: {
    fontFamily: serif,
    fontSize: 34,
    lineHeight: 41,
    fontWeight: "700",
    color: t.ink,
  },
  status: {
    color: t.white,
    fontWeight: "700",
    letterSpacing: 1.2,
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderRadius: 20,
    overflow: "hidden",
    fontSize: 13,
  },
  clock: { fontFamily: mono, fontSize: 22, color: t.ink },
  workspace: {
    borderTopWidth: 1,
    borderBottomWidth: 1,
    borderColor: t.line,
    minHeight: 320,
  },
  transcript: { flexGrow: 1 },
  rail: {},
  turn: { flexDirection: "row", gap: 14 },
  time: {
    fontFamily: mono,
    fontSize: 12,
    color: t.muted,
    paddingTop: 3,
    width: 58,
  },
  turnBody: { flex: 1, borderLeftWidth: 1, paddingLeft: 14, gap: 6 },
  partial: { color: t.muted, fontStyle: "italic" },
  reply: { backgroundColor: t.wash, minHeight: 110 },
  reason: { color: t.brass, fontSize: 14, lineHeight: 20 },
  composer: { gap: 12 },
  hint: {
    borderWidth: 1,
    borderColor: t.green,
    borderRadius: 16,
    paddingHorizontal: 12,
    paddingVertical: 8,
  },
  evidence: {
    fontStyle: "italic",
    color: t.ink,
    borderLeftWidth: 2,
    borderColor: t.green,
    paddingLeft: 10,
    lineHeight: 22,
  },
  link: { color: t.green, fontSize: 16, paddingVertical: 12 },
});
