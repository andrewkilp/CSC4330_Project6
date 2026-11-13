import {
  allowedActions,
  type ControlAction,
  type ControlProblem,
  controlCall,
  controlProblem,
  type EventState,
  isAcknowledged,
} from "@switchboard/api";
import { randomUUID } from "expo-crypto";
import { useEffect, useState } from "react";
import { StyleSheet, View } from "react-native";
import { AppText, Button } from "../../components";
import { theme as t } from "../../theme";
import { API_URL } from "./api-url";

type Request = { action: ControlAction; key: string; sent: boolean };

const WAITING: Record<ControlAction, string> = {
  pause: "Pause requested. Waiting for the call to confirm.",
  resume: "Resume requested. Waiting for the call to confirm.",
  end: "Hang-up requested. Waiting for the call to confirm.",
};

// Pause AI and End call (#37). The buttons reflect only what the server has confirmed
// through the event stream; a request in flight is shown as requested, never as done.
export function CallControls({ state }: { state: EventState }) {
  const status = state.call?.status ?? "draft";
  const allowed = allowedActions(status);
  const [request, setRequest] = useState<Request | null>(null);
  const [problem, setProblem] = useState<ControlProblem | null>(null);
  const [confirmingEnd, setConfirmingEnd] = useState(false);
  const [busy, setBusy] = useState(false);
  const acknowledged = request ? isAcknowledged(request.action, status) : false;
  const queued = request?.sent && !acknowledged ? request : null;

  // Once the stream confirms the action, it is no longer pending.
  useEffect(() => {
    if (request?.sent && acknowledged) setRequest(null);
  }, [request, acknowledged]);

  async function send(action: ControlAction, key: string) {
    setRequest({ action, key, sent: false });
    setProblem(null);
    setBusy(true);
    try {
      await controlCall(API_URL, state.callId, action, key);
      setRequest({ action, key, sent: true });
    } catch (error) {
      const next = controlProblem(action, error);
      setProblem(next);
      // Keep the key so "Try again" repeats the same request rather than a second one.
      if (next.kind === "moot") setRequest(null);
    } finally {
      setBusy(false);
    }
  }
  const start = (action: ControlAction) =>
    void send(
      action,
      request?.action === action && !request.sent && problem?.kind === "retry"
        ? request.key
        : randomUUID(),
    );

  if (allowed.length === 0)
    return (
      <View style={s.controls}>
        <AppText variant="caption" tone="muted">
          {status === "ended"
            ? "The call has ended. No controls remain."
            : status === "failed"
              ? "The call failed. No controls remain."
              : "The call was cancelled. No controls remain."}
        </AppText>
      </View>
    );

  const pauseAction: ControlAction | null = allowed.includes("pause")
    ? "pause"
    : allowed.includes("resume")
      ? "resume"
      : null;
  return (
    <View style={s.controls}>
      {confirmingEnd ? (
        <View style={s.confirm} accessibilityRole="alert">
          <AppText style={s.confirmTitle}>End this call?</AppText>
          <AppText variant="caption">
            This hangs up on {state.call?.business_name ?? "the business"} and
            cannot be undone. The transcript stays readable.
          </AppText>
          <View style={s.actions}>
            <Button
              variant="destructive"
              label="End call now"
              loading={busy && request?.action === "end"}
              loadingLabel="Ending…"
              onPress={() => {
                setConfirmingEnd(false);
                start("end");
              }}
            />
            <Button
              variant="secondary"
              label="Keep calling"
              onPress={() => setConfirmingEnd(false)}
            />
          </View>
        </View>
      ) : (
        <View style={s.actions}>
          {pauseAction ? (
            <Button
              variant="secondary"
              label={pauseAction === "pause" ? "Pause AI" : "Resume AI"}
              loading={busy && request?.action === pauseAction}
              loadingLabel={pauseAction === "pause" ? "Pausing…" : "Resuming…"}
              disabled={busy || Boolean(queued)}
              onPress={() => start(pauseAction)}
            />
          ) : null}
          <Button
            variant="destructive"
            label="End call"
            disabled={busy || queued?.action === "end"}
            onPress={() => setConfirmingEnd(true)}
          />
        </View>
      )}
      {queued ? (
        <AppText accessibilityLiveRegion="polite" variant="caption">
          {WAITING[queued.action]}
        </AppText>
      ) : null}
      {problem ? (
        <View style={s.actions}>
          <AppText
            accessibilityLiveRegion="assertive"
            variant="caption"
            tone="danger"
            style={s.problem}
          >
            {problem.message}
          </AppText>
          {problem.kind === "retry" && request ? (
            <Button
              variant="secondary"
              label="Try again"
              disabled={busy}
              onPress={() => void send(request.action, request.key)}
            />
          ) : null}
        </View>
      ) : null}
      {status === "paused" ? (
        <AppText variant="caption" tone="muted">
          The AI is silent. You can still read the transcript and send private
          instructions; resume to continue.
        </AppText>
      ) : null}
    </View>
  );
}

const s = StyleSheet.create({
  controls: { gap: t.space.sm },
  actions: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: t.space.md,
    alignItems: "center",
  },
  confirm: {
    gap: t.space.sm,
    borderWidth: 1,
    borderColor: t.vermilion,
    borderRadius: t.radius,
    padding: t.space.lg,
  },
  confirmTitle: { fontWeight: "700" },
  problem: { flexShrink: 1 },
});
