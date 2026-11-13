import {
  createEventState,
  type StreamUpdate,
  subscribeCall,
} from "@switchboard/api";
import { fetch as expoFetch } from "expo/fetch";
import { useFocusEffect, useRouter } from "expo-router";
import { useCallback, useState } from "react";
import {
  AppState,
  Platform,
  ScrollView,
  StyleSheet,
  useWindowDimensions,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { AppText, Badge, Button, RuledPanel } from "../../components";
import { theme as t } from "../../theme";
import { API_URL } from "./api-url";
import { CallControls } from "./controls";
import { InstructionComposer } from "./instructions";
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
export default function ConnectedCall({ id }: { id: string }) {
  const router = useRouter(),
    wide = useWindowDimensions().width >= t.layout.breakpoint;
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
  return (
    <SafeAreaView style={s.safe}>
      <ScrollView contentContainerStyle={s.content}>
        <View style={s.heading}>
          <AppText variant="heading">Switchboard</AppText>
          <Button
            label="Calls"
            variant="secondary"
            onPress={() => router.push("/")}
          />
        </View>
        <AppText variant="display" accessibilityRole="header">
          {call?.business_name ?? "Your call"}
        </AppText>
        {call && (
          <Badge
            label={`${call.mode.toUpperCase()} · ${call.status.toUpperCase()}`}
            tone={call.mode === "mock" ? "warning" : "neutral"}
          />
        )}
        <AppText
          accessibilityLiveRegion="polite"
          tone={
            update?.status === "unauthenticated" || update?.status === "error"
              ? "danger"
              : "muted"
          }
        >
          {update ? messages[update.status] : "This call could not be loaded."}
        </AppText>
        <View style={[s.workspace, wide && s.wide]}>
          <View style={s.transcript}>
            {update?.state.call && <Transcript key={id} state={update.state} />}
          </View>
          {call && (
            <View style={[s.rail, wide && { width: 300 }]}>
              <RuledPanel title="Current goal">
                <AppText>
                  {call.goal.kind === "restaurant"
                    ? `Table for ${call.goal.party_size}`
                    : call.goal.service}
                </AppText>
                <AppText>
                  {call.goal.preferred_at} · {call.goal.timezone}
                </AppText>
                {call.goal.constraints.map((text, index) => (
                  <AppText key={`${index}-${text}`}>{text}</AppText>
                ))}
              </RuledPanel>
              {update && (
                <SuggestedReply
                  state={update.state}
                  live={update.status === "live"}
                />
              )}
            </View>
          )}
        </View>
        {update?.state.call && (
          <>
            <InstructionComposer
              state={update.state}
              live={update.status === "live"}
            />
            <CallControls state={update.state} />
          </>
        )}
        <Button
          label="Reconnect call updates"
          variant="secondary"
          onPress={() => setRetry((value) => value + 1)}
        />
        <AppText variant="caption" tone="muted">
          Opening or reconnecting this page never places a call.
        </AppText>
      </ScrollView>
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
});
