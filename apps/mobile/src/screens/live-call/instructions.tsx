import {
  type EventState,
  type Instruction,
  type InstructionAttempt,
  type InstructionProblem,
  instructionProblem,
  LIMITS,
  nextInstructionAttempt,
  sendInstruction,
} from "@switchboard/api";
import { randomUUID } from "expo-crypto";
import { useEffect, useState } from "react";
import { StyleSheet, View } from "react-native";
import { AppText, Badge, Button, Field, RuledPanel } from "../../components";
import { theme as t } from "../../theme";
import { API_URL } from "./api-url";

const STATUS_BADGE = {
  pending: { label: "PENDING", tone: "warning" },
  applied: { label: "APPLIED", tone: "success" },
  failed: { label: "FAILED", tone: "danger" },
} as const;

// Private steering for the AI (#27). The text is context for the next reply and is never
// spoken; the revised suggestion arrives from the server as a response event.
export function InstructionComposer({
  state,
  live,
}: {
  state: EventState;
  // False while the stream reconnects or resyncs, so nothing is sent against stale state.
  live: boolean;
}) {
  const [text, setText] = useState("");
  const [attempt, setAttempt] = useState<InstructionAttempt | null>(null);
  const [sending, setSending] = useState(false);
  const [problem, setProblem] = useState<InstructionProblem | null>(null);
  const [sentId, setSentId] = useState<string | null>(null);
  const status = state.call?.status;
  const open = status === "connected" || status === "paused";
  const sent = sentId ? (state.instructions[sentId] ?? null) : null;

  // A failed instruction comes back to the box so nothing typed is lost.
  useEffect(() => {
    if (sent?.status !== "failed") return;
    setText(sent.text);
    setSentId(null);
    setProblem({
      kind: "retry",
      message:
        "The AI could not apply that instruction. Your text is back in the box.",
    });
  }, [sent]);

  async function send(revision: number) {
    const next = nextInstructionAttempt(
      attempt,
      text.trim(),
      revision,
      randomUUID,
    );
    setAttempt(next);
    setSending(true);
    setProblem(null);
    try {
      const instruction = await sendInstruction(API_URL, state.callId, next);
      setSentId(instruction.id);
      setText("");
      setAttempt(null);
    } catch (error) {
      setProblem(instructionProblem(error));
    } finally {
      setSending(false);
    }
  }

  const ready = open && live && !sending && Boolean(text.trim());
  const history = Object.values(state.instructions)
    .slice()
    .sort((a, b) => a.created_at.localeCompare(b.created_at));
  return (
    <RuledPanel title="Guide the next response">
      <Field
        label="Private instruction"
        hint={`Never read aloud. ${text.length} / ${LIMITS.instructionText} characters.`}
        placeholder="Accept 7:30, but ask for the patio."
        value={text}
        onChangeText={(value) => {
          setText(value);
          if (problem?.kind !== "stale") setProblem(null);
        }}
        maxLength={LIMITS.instructionText}
        editable={open && !sending}
        multiline
      />
      {problem ? (
        <AppText
          accessibilityLiveRegion="assertive"
          tone="danger"
          variant="caption"
        >
          {problem.message}
        </AppText>
      ) : null}
      <View style={s.actions}>
        {problem?.kind === "stale" ? (
          <Button
            label="Send with newest context"
            loading={sending}
            loadingLabel="Sending…"
            disabled={!ready}
            onPress={() => void send(state.contextRevision)}
          />
        ) : (
          <Button
            label={problem?.kind === "retry" ? "Try again" : "Send instruction"}
            loading={sending}
            loadingLabel="Sending…"
            disabled={!ready}
            onPress={() =>
              void send(attempt?.expectedRevision ?? state.contextRevision)
            }
          />
        )}
      </View>
      <SentStatus state={state} sent={sent} />
      {!open ? (
        <AppText variant="caption" tone="muted">
          {status === "ended" || status === "failed" || status === "cancelled"
            ? "The call has ended. Instructions are closed."
            : "Instructions open once the call connects."}
        </AppText>
      ) : !live ? (
        <AppText variant="caption" tone="muted">
          Sending waits until call updates are current again.
        </AppText>
      ) : null}
      {history.length > 0 ? (
        <View style={s.history}>
          <AppText variant="label" tone="muted" accessibilityRole="header">
            YOUR INSTRUCTIONS
          </AppText>
          {history.map((item) => (
            <HistoryRow key={item.id} instruction={item} />
          ))}
        </View>
      ) : null}
    </RuledPanel>
  );
}

function SentStatus({
  state,
  sent,
}: {
  state: EventState;
  sent: Instruction | null;
}) {
  if (!sent) return null;
  const reply = state.currentResponseId
    ? state.responses[state.currentResponseId]
    : null;
  const revised =
    sent.status === "applied" &&
    reply?.status === "suggested" &&
    reply.context_revision >= sent.context_revision;
  return (
    <AppText accessibilityLiveRegion="polite" variant="caption">
      {sent.status === "pending"
        ? "Sent privately. Waiting for the AI to apply it."
        : revised
          ? "Applied. A revised suggestion is ready to review."
          : "Applied. The AI is revising its suggestion."}
    </AppText>
  );
}

function HistoryRow({ instruction }: { instruction: Instruction }) {
  const badge = STATUS_BADGE[instruction.status];
  return (
    <View style={s.row}>
      <Badge label={badge.label} tone={badge.tone} />
      <AppText variant="caption" style={s.rowText}>
        {instruction.text}
      </AppText>
    </View>
  );
}

const s = StyleSheet.create({
  actions: { flexDirection: "row", flexWrap: "wrap", gap: t.space.md },
  history: { gap: t.space.sm },
  row: { flexDirection: "row", gap: t.space.md, alignItems: "flex-start" },
  rowText: { flex: 1, minWidth: 0 },
});
