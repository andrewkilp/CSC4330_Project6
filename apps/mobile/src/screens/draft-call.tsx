import { type Call, CallRequestError, getCall } from "@switchboard/api";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useRef, useState } from "react";
import { ScrollView, StyleSheet, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { AppText, Badge, Button, RuledPanel } from "../components";
import { theme as t } from "../theme";
import { useGoalDraft } from "./goal-context";

export default function DraftCallScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const router = useRouter();
  const { intent, setIntent } = useGoalDraft();
  const intentRef = useRef(intent);
  intentRef.current = intent;
  const [call, setCall] = useState<Call | null>(null),
    [error, setError] = useState(""),
    [retry, setRetry] = useState(0);
  // biome-ignore lint/correctness/useExhaustiveDependencies: retry explicitly reloads the same owner-gated resource.
  useEffect(() => {
    const controller = new AbortController();
    setCall(null);
    setError("");
    void getCall(
      process.env.EXPO_PUBLIC_API_URL || "http://localhost:8080",
      id ?? "",
      controller.signal,
    )
      .then((value) => {
        if (!controller.signal.aborted) setCall(value);
      })
      .catch((err) => {
        if (!controller.signal.aborted) {
          const current = intentRef.current;
          if (
            err instanceof CallRequestError &&
            (err.code === "unauthenticated" || err.code === "not_found") &&
            current?.call?.id === id
          ) {
            setIntent({ fingerprint: current.fingerprint, key: current.key });
          }
          setError(
            err instanceof CallRequestError
              ? err.message
              : "The saved call could not be loaded.",
          );
        }
      });
    return () => controller.abort();
  }, [id, retry, setIntent]);
  return (
    <SafeAreaView style={s.safe}>
      <ScrollView contentContainerStyle={s.content}>
        <AppText variant="label" tone="muted">
          SWITCHBOARD / SAVED CALL
        </AppText>
        <AppText variant="display" accessibilityRole="header">
          {call?.business_name ?? "Your call draft"}
        </AppText>
        {error ? (
          <View style={s.section}>
            <AppText accessibilityRole="alert" tone="danger">
              {error}
            </AppText>
            <Button
              label="Try loading again"
              onPress={() => setRetry(retry + 1)}
            />
          </View>
        ) : !call ? (
          <AppText accessibilityLiveRegion="polite">
            Loading your saved call…
          </AppText>
        ) : (
          <>
            <Badge
              label={`${call.mode.toUpperCase()} · ${call.status.toUpperCase()}`}
              tone={call.status === "draft" ? "neutral" : "warning"}
            />
            <Button
              label="Open live updates"
              variant="secondary"
              onPress={() => router.push(`/calls/${call.id}?live=1`)}
            />
            <AppText>
              {call.status === "draft"
                ? "Draft saved. No call has been placed."
                : "This is the saved call state. Live conversation controls are a separate step."}
            </AppText>
            <RuledPanel title="Booking details">
              <View style={s.section}>
                <AppText>{call.destination}</AppText>
                <AppText>
                  {call.goal.kind === "restaurant"
                    ? `Reservation for ${call.goal.party_size} guests`
                    : `Appointment: ${call.goal.service}`}
                </AppText>
                <AppText>
                  {call.goal.preferred_at} · {call.goal.timezone}
                </AppText>
                <AppText>Booking name: {call.goal.contact_name}</AppText>
                <AppText>
                  Callback phone: {call.goal.contact_phone ?? "Not provided"}
                </AppText>
                {call.goal.constraints.map((constraint, index) => (
                  <AppText key={`${index}-${constraint}`}>
                    • {constraint}
                  </AppText>
                ))}
                <AppText>
                  Approval:{" "}
                  {call.approval_mode === "always"
                    ? "Every suggested reply"
                    : "Uncertain replies"}
                </AppText>
              </View>
            </RuledPanel>
            <AppText tone="muted">
              Starting is a separate explicit action. Opening or reloading this
              page never starts a call.
            </AppText>
          </>
        )}
        <View style={s.actions}>
          <Button
            label="Return to review"
            variant="secondary"
            onPress={() => router.replace("/review")}
          />
          <Button
            label="Calls"
            variant="secondary"
            onPress={() => router.push("/")}
          />
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}
const s = StyleSheet.create({
  safe: { flex: 1, backgroundColor: t.paper },
  content: {
    padding: 24,
    gap: 24,
    maxWidth: 1000,
    width: "100%",
    alignSelf: "center",
  },
  section: { gap: 16 },
  actions: { flexDirection: "row", gap: 12, flexWrap: "wrap" },
});
