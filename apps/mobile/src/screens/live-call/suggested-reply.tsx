import {
  type ApprovalProblem,
  approvalProblem,
  approveResponse,
  type EventState,
  LIMITS,
  type Response,
} from "@switchboard/api";
import { randomUUID } from "expo-crypto";
import { useRef, useState } from "react";
import { StyleSheet, View } from "react-native";
import { AppText, Badge, Button, Field, RuledPanel } from "../../components";
import { theme as t } from "../../theme";
import { API_URL } from "./api-url";

const STATUS: Record<
  Response["status"],
  { label: string; tone: "neutral" | "warning" | "success" }
> = {
  generating: { label: "DRAFTING", tone: "neutral" },
  suggested: { label: "NEEDS YOUR APPROVAL", tone: "warning" },
  approved: { label: "APPROVED, WAITING TO PLAY", tone: "success" },
  speaking: { label: "SPEAKING NOW", tone: "success" },
  spoken: { label: "SPOKEN", tone: "neutral" },
  cancelled: { label: "CANCELLED", tone: "neutral" },
  failed: { label: "FAILED", tone: "neutral" },
};

function emptyMessage(state: EventState, live: boolean): string {
  const status = state.call?.status;
  if (status === "ended" || status === "failed" || status === "cancelled")
    return "The call has ended. No more replies will be suggested.";
  if (status === "paused")
    return "Suggestions are on hold while the AI is paused.";
  if (status !== "connected")
    return "Suggestions appear once the call connects.";
  if (!live) return "Suggestions return once call updates are current again.";
  return "Listening. A suggestion appears after the business finishes speaking.";
}

// Edits belong to the suggestion they were made on. When that suggestion is replaced,
// the edit is kept aside instead of being applied to text the operator has not read.
type Edit = { responseId: string; text: string };

// Suggested reply editing and approval (#32). Reads the suggestion through
// currentResponseId only, and never puts draft text into the transcript.
export function SuggestedReply({
  state,
  live,
}: {
  state: EventState;
  // False while the stream reconnects or resyncs, so a stale suggestion is not approved.
  live: boolean;
}) {
  const response = state.currentResponseId
    ? (state.responses[state.currentResponseId] ?? null)
    : null;
  const [edit, setEdit] = useState<Edit | null>(null);
  const [problem, setProblem] = useState<ApprovalProblem | null>(null);
  const [approving, setApproving] = useState(false);
  const [confirmed, setConfirmed] = useState<Response | null>(null);
  const keys = useRef(new Map<string, string>());

  const editing = Boolean(edit && response && edit.responseId === response.id);
  const leftover =
    edit && (!response || edit.responseId !== response.id) ? edit : null;
  const text = editing && edit ? edit.text : (response?.text ?? "");
  const changed = Boolean(response && text.trim() !== response.text);
  const actionable =
    response?.status === "suggested" &&
    state.call?.status === "connected" &&
    live;

  async function approve(target: Response) {
    const editedText = changed ? text.trim() : undefined;
    // Same response, revision and text reuse their key, so a retry cannot double-approve.
    const fingerprint = `${target.id}:${target.context_revision}:${editedText ?? ""}`;
    const key = keys.current.get(fingerprint) ?? randomUUID();
    keys.current.set(fingerprint, key);
    setApproving(true);
    setProblem(null);
    try {
      const approved = await approveResponse(API_URL, state.callId, target.id, {
        contextRevision: target.context_revision,
        editedText,
        idempotencyKey: key,
      });
      setConfirmed(approved);
      setEdit(null);
    } catch (error) {
      setProblem(approvalProblem(error));
    } finally {
      setApproving(false);
    }
  }

  return (
    <RuledPanel title="Suggested reply">
      {response ? (
        <>
          <Badge
            label={
              response.status === "suggested" && !response.needs_approval
                ? "READY"
                : STATUS[response.status].label
            }
            tone={STATUS[response.status].tone}
          />
          {response.status === "suggested" && response.needs_approval ? (
            <AppText variant="caption" tone="muted">
              Why approval is needed:{" "}
              {response.reason ??
                "Every reply in this call needs your approval."}
            </AppText>
          ) : null}
          {response.status === "generating" ? (
            <View style={s.box}>
              <AppText tone="muted">Drafting a reply to the business…</AppText>
            </View>
          ) : response.status === "suggested" ? (
            <Field
              label="Reply to approve"
              hint={
                changed
                  ? "Edited. Your version replaces the suggestion when you approve."
                  : "Edit before approving if you like. Nothing is said until you approve."
              }
              value={text}
              onChangeText={(value) => {
                setEdit({ responseId: response.id, text: value });
                setProblem(null);
              }}
              maxLength={LIMITS.responseText}
              editable={actionable && !approving}
              multiline
              inputStyle={s.input}
            />
          ) : (
            <View style={s.box}>
              <AppText>{response.text}</AppText>
            </View>
          )}
          {response.status === "suggested" ? (
            <View style={s.actions}>
              <Button
                label={changed ? "Approve my edit" : "Approve reply"}
                loading={approving}
                loadingLabel="Approving…"
                disabled={!actionable || !text.trim()}
                onPress={() => void approve(response)}
              />
              {changed ? (
                <Button
                  variant="secondary"
                  label="Undo edit"
                  disabled={approving}
                  onPress={() => setEdit(null)}
                />
              ) : null}
            </View>
          ) : null}
        </>
      ) : (
        <AppText variant="caption" tone="muted">
          {emptyMessage(state, live)}
        </AppText>
      )}
      {problem ? (
        <AppText
          accessibilityLiveRegion="assertive"
          variant="caption"
          tone="danger"
        >
          {problem.message}
          {problem.kind === "stale" && leftover
            ? " Your edit is kept below."
            : ""}
        </AppText>
      ) : null}
      {leftover ? (
        <View style={s.leftover}>
          <AppText variant="label" tone="muted">
            YOUR EARLIER EDIT, NOT SENT
          </AppText>
          <AppText>{leftover.text}</AppText>
          <View style={s.actions}>
            {response?.status === "suggested" ? (
              <Button
                variant="secondary"
                label="Use my edit on the newest suggestion"
                onPress={() =>
                  setEdit({ responseId: response.id, text: leftover.text })
                }
              />
            ) : null}
            <Button
              variant="secondary"
              label="Discard edit"
              onPress={() => setEdit(null)}
            />
          </View>
        </View>
      ) : null}
      {confirmed && response?.id === confirmed.id ? (
        <AppText accessibilityLiveRegion="polite" variant="caption">
          Approved by the server: “{confirmed.text}” It enters the transcript
          once playback is confirmed.
        </AppText>
      ) : null}
    </RuledPanel>
  );
}

const s = StyleSheet.create({
  box: {
    backgroundColor: t.wash,
    borderWidth: 1,
    borderColor: t.green,
    borderRadius: t.radius,
    padding: t.space.lg,
  },
  input: { backgroundColor: t.wash, minHeight: 110 },
  actions: { flexDirection: "row", flexWrap: "wrap", gap: t.space.md },
  leftover: {
    gap: t.space.sm,
    borderLeftWidth: 3,
    borderLeftColor: t.brass,
    paddingLeft: t.space.md,
  },
});
