import {
  createEventState,
  type StreamUpdate,
  subscribeCall,
} from "@switchboard/api";
import { fetch as expoFetch } from "expo/fetch";
import { useFocusEffect, useRouter } from "expo-router";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  AccessibilityInfo,
  AppState,
  KeyboardAvoidingView,
  Platform,
  ScrollView,
  StyleSheet,
  useWindowDimensions,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { AppText, Badge, Button, RuledPanel } from "../../components";
import { theme as t } from "../../theme";
import {
  type AnnounceSnapshot,
  announcementsBetween,
  announceSnapshot,
} from "./announce";
import { API_URL } from "./api-url";
import { CallControls } from "./controls";
import {
  businessTag,
  callTitle,
  describeGoal,
  formatPhone,
  kindLabel,
  statusBadge,
} from "./goal-text";
import { InstructionComposer, InstructionHistory } from "./instructions";
import { SuggestedReply } from "./suggested-reply";
import { Transcript } from "./transcript";

const messages = {
  connecting: "Loading this call…",
  live: "Receiving call updates",
  reconnecting: "Connection interrupted. Reconnecting from the last update…",
  resyncing:
    "Refreshing missed updates. Suggestions are unavailable until recovery completes.",
  offline: "Updates suspended while this screen is inactive.",
  unauthenticated:
    "Your session expired. Return to your calls to sign in again.",
  not_found: "This call was not found in your session.",
  error: "The call updates could not be read. Try reconnecting.",
};
// Short screens and large text keep only Pause and End docked; the composer moves into
// the page so the transcript is never squeezed out.
const COMPACT_HEIGHT = 640;
const COMPACT_FONT_SCALE = 1.5;

export default function ConnectedCall({ id }: { id: string }) {
  const router = useRouter();
  const { width, height, fontScale } = useWindowDimensions();
  const wide = width >= t.layout.breakpoint;
  const compact = height < COMPACT_HEIGHT || fontScale >= COMPACT_FONT_SCALE;
  const [retry, setRetry] = useState(0);
  const [update, setUpdate] = useState<StreamUpdate | null>(null);
  useFocusEffect(
    // biome-ignore lint/correctness/useExhaustiveDependencies: retry explicitly creates a fresh authenticated subscription.
    useCallback(() => {
      let mounted = true;
      let subscription: ReturnType<typeof subscribeCall>;
      try {
        setUpdate({ state: createEventState(id), status: "connecting" });
        subscription = subscribeCall({
          baseUrl: API_URL,
          callId: id,
          fetch: expoFetch as typeof globalThis.fetch,
          onUpdate: (value) => {
            if (mounted) setUpdate(value);
          },
        });
      } catch {
        setUpdate(null);
        return;
      }
      const change = () =>
        subscription.setActive(
          AppState.currentState === "active" &&
            (Platform.OS !== "web" || !document.hidden),
        );
      const listener = AppState.addEventListener("change", change);
      if (Platform.OS === "web")
        document.addEventListener("visibilitychange", change);
      change();
      return () => {
        mounted = false;
        subscription.close();
        listener.remove();
        if (Platform.OS === "web")
          document.removeEventListener("visibilitychange", change);
      };
      // retry intentionally replaces the closed subscription with a fresh snapshot.
    }, [id, retry]),
  );
  const call = update?.state.call;
  useAnnouncements(update);
  const live = update?.status === "live";
  return (
    <SafeAreaView style={s.safe}>
      <KeyboardAvoidingView
        style={s.safe}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
      >
        <ScrollView
          style={s.safe}
          contentContainerStyle={s.content}
          keyboardShouldPersistTaps="handled"
        >
          <View style={s.heading}>
            <AppText variant="heading">Switchboard</AppText>
            <Button
              label="Calls"
              variant="secondary"
              onPress={() => router.push("/")}
            />
          </View>
          <View style={s.titleRow}>
            <View style={s.titleText}>
              <AppText
                variant="display"
                accessibilityRole="header"
                // Long business names are capped here; the goal panel shows the full name.
                numberOfLines={wide ? 2 : 3}
                accessibilityLabel={call ? callTitle(call) : "Your call"}
              >
                {call ? callTitle(call) : "Your call"}
              </AppText>
              {call ? (
                <AppText variant="label" tone="muted" numberOfLines={2}>
                  {kindLabel(call.goal)} · {call.business_name.toUpperCase()}
                </AppText>
              ) : null}
            </View>
            {call ? (
              <View style={s.badges}>
                <Badge {...statusBadge(call.status)} />
                {call.mode === "mock" ? (
                  <Badge label="MOCK CALL" tone="warning" />
                ) : null}
              </View>
            ) : null}
          </View>
          <AppText
            tone={
              update?.status === "unauthenticated" || update?.status === "error"
                ? "danger"
                : "muted"
            }
          >
            {update
              ? messages[update.status]
              : "This call could not be loaded."}
          </AppText>
          <View style={[s.workspace, wide && s.wide]}>
            <View style={s.transcript}>
              {update?.state.call && (
                <Transcript
                  key={id}
                  state={update.state}
                  businessTag={businessTag(update.state.call.goal)}
                />
              )}
            </View>
            {call && (
              <View style={[s.rail, wide && { width: 300 }]}>
                <RuledPanel title="Current goal">
                  <View style={s.goal}>
                    <AppText style={s.goalWhat}>
                      {describeGoal(call.goal).what}
                    </AppText>
                    <AppText>{describeGoal(call.goal).when}</AppText>
                    <AppText variant="caption" tone="muted">
                      {call.goal.timezone} / for {call.goal.contact_name}
                    </AppText>
                    <AppText variant="caption" tone="muted">
                      Calling {call.business_name} at{" "}
                      {formatPhone(call.destination)}
                    </AppText>
                    {call.goal.constraints.map((text, index) => (
                      <AppText key={`${index}-${text}`} variant="caption">
                        • {text}
                      </AppText>
                    ))}
                  </View>
                </RuledPanel>
                {update && (
                  <>
                    <SuggestedReply state={update.state} live={live} />
                    <InstructionHistory state={update.state} />
                  </>
                )}
              </View>
            )}
          </View>
          {compact && update?.state.call ? (
            <InstructionComposer state={update.state} live={live} />
          ) : null}
          <Button
            label="Reconnect call updates"
            variant="secondary"
            onPress={() => setRetry((value) => value + 1)}
          />
          <AppText variant="caption" tone="muted">
            Opening or reconnecting this page never places a call.
          </AppText>
        </ScrollView>
        {update?.state.call ? (
          <View style={[s.dock, wide && s.dockWide]}>
            {compact ? null : (
              <ScrollView
                style={[
                  { maxHeight: Math.round(height * 0.28) },
                  wide && s.dockComposerWide,
                ]}
                keyboardShouldPersistTaps="handled"
                nestedScrollEnabled
              >
                <InstructionComposer state={update.state} live={live} docked />
              </ScrollView>
            )}
            <View style={wide ? s.dockControlsWide : undefined}>
              <CallControls state={update.state} />
            </View>
          </View>
        ) : null}
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}
const s = StyleSheet.create({
  safe: { flex: 1, backgroundColor: t.paper },
  content: {
    padding: 24,
    gap: 24,
    maxWidth: 1300,
    width: "100%",
    alignSelf: "center",
  },
  heading: {
    flexDirection: "row",
    justifyContent: "space-between",
    gap: 16,
    flexWrap: "wrap",
  },
  workspace: { gap: 24 },
  wide: { flexDirection: "row" },
  transcript: { flex: 1, minWidth: 0 },
  rail: { gap: 24 },
  titleRow: {
    flexDirection: "row",
    flexWrap: "wrap",
    alignItems: "flex-start",
    gap: t.space.lg,
  },
  titleText: { flex: 1, minWidth: 220, gap: t.space.sm },
  badges: { flexDirection: "row", flexWrap: "wrap", gap: t.space.sm },
  goal: {
    borderWidth: 1,
    borderColor: t.green,
    borderRadius: t.radius,
    padding: t.space.lg,
    gap: t.space.xs,
  },
  goalWhat: { fontWeight: "600" },
  dock: {
    borderTopWidth: 1,
    borderColor: t.line,
    backgroundColor: t.paper,
    paddingHorizontal: t.layout.narrow.pagePadding,
    paddingVertical: t.space.md,
    gap: t.space.sm,
  },
  dockWide: {
    paddingHorizontal: t.layout.wide.pagePadding,
    flexDirection: "row",
    alignItems: "flex-end",
    gap: t.space.xl,
  },
  dockComposerWide: { flex: 1 },
  dockControlsWide: { maxWidth: 360 },
});

// Screen readers hear status changes, a reply awaiting approval, each finished business
// turn and stream problems. Partial speech is never announced.
function useAnnouncements(update: StreamUpdate | null) {
  const previous = useRef<AnnounceSnapshot | null>(null);
  useEffect(() => {
    if (!update?.state.call) return;
    const next = announceSnapshot(update.state, update.status);
    for (const message of announcementsBetween(
      previous.current,
      next,
      update.state,
    ))
      AccessibilityInfo.announceForAccessibility(message);
    previous.current = next;
  }, [update]);
}
