import { useRouter } from "expo-router";
import { useState } from "react";
import {
  KeyboardAvoidingView,
  Platform,
  ScrollView,
  StyleSheet,
  useWindowDimensions,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { AppText, Badge, Button, Field, RuledPanel } from "../../components";
import { theme as t } from "../../theme";
import {
  type PreviewPhase,
  previewCall,
  previewReply,
  previewTurns,
} from "./fixture";

export default function LiveCallPreview() {
  const router = useRouter();
  const wide = useWindowDimensions().width >= t.layout.breakpoint;
  const [phase, setPhase] = useState<PreviewPhase>("approval");
  const [instruction, setInstruction] = useState("");
  const [reply, setReply] = useState(previewReply.text);
  const [notice, setNotice] = useState(
    "Fictional preview. No telephone call is connected.",
  );
  const inactive = phase !== "approval";
  const labels: Record<PreviewPhase, string> = {
    connecting: "Connecting",
    approval: "Awaiting approval",
    paused: "AI paused",
    ended: "Call ended",
  };
  return (
    <SafeAreaView style={s.safe}>
      <KeyboardAvoidingView
        style={s.safe}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
      >
        <View style={[s.shell, wide && s.horizontal]}>
          <View style={[s.nav, wide && s.navWide]}>
            <AppText variant="heading">Switchboard</AppText>
            <AppText variant="label" tone="muted">
              A LITTLE HELP ON THE LINE
            </AppText>
            <View style={s.links}>
              <Button
                label="Calls"
                variant="secondary"
                onPress={() => router.push("/")}
              />
              <Button
                label="New call"
                variant="secondary"
                onPress={() => router.push("/new")}
              />
            </View>
            <Badge label="MOCK PREVIEW" tone="warning" />
          </View>
          <ScrollView
            style={s.main}
            contentContainerStyle={[s.content, wide && s.contentWide]}
            keyboardShouldPersistTaps="handled"
          >
            <View style={s.header}>
              <View style={s.title}>
                <AppText variant="display" accessibilityRole="header">
                  Reservation at {previewCall.business_name}
                </AppText>
                <AppText variant="label" tone="muted">
                  RESTAURANT RESERVATION / FICTIONAL EXAMPLE
                </AppText>
              </View>
              <Badge
                label={labels[phase]}
                tone={phase === "ended" ? "neutral" : "warning"}
              />
            </View>
            <AppText
              variant="caption"
              tone="muted"
              accessibilityLiveRegion="polite"
            >
              {notice}
            </AppText>
            <View style={s.phases}>
              {(["connecting", "approval", "paused", "ended"] as const).map(
                (value) => (
                  <Button
                    key={value}
                    label={`Preview: ${labels[value]}`}
                    variant="secondary"
                    accessibilityState={{ selected: phase === value }}
                    onPress={() => {
                      setPhase(value);
                      setNotice(
                        `Preview state: ${labels[value]}. No real call is connected.`,
                      );
                    }}
                  />
                ),
              )}
            </View>
            <View style={[s.workspace, wide && s.horizontal]}>
              <View style={s.transcript}>
                <AppText variant="label" tone="muted">
                  ON THE LINE / SAMPLE TRANSCRIPT
                </AppText>
                {phase === "connecting" ? (
                  <AppText>
                    Waiting for the fictional business to answer.
                  </AppText>
                ) : (
                  previewTurns.map((turn) => (
                    <View key={turn.id} style={s.turn}>
                      <AppText variant="mono" tone="muted">
                        [
                        {String(
                          Math.floor(turn.started_at_ms / 60000),
                        ).padStart(2, "0")}
                        :
                        {String(
                          Math.floor(turn.started_at_ms / 1000) % 60,
                        ).padStart(2, "0")}
                        ]
                      </AppText>
                      <View
                        style={[
                          s.turnBody,
                          turn.speaker === "business" && s.business,
                        ]}
                      >
                        <AppText
                          variant="label"
                          tone={
                            turn.speaker === "assistant" ? "green" : "danger"
                          }
                        >
                          {turn.speaker === "assistant" ? "AI" : "RESTAURANT"}
                        </AppText>
                        <AppText>{turn.text}</AppText>
                      </View>
                    </View>
                  ))
                )}
              </View>
              <View style={[s.rail, wide && s.railWide]}>
                <RuledPanel title="Current goal">
                  <View style={s.goal}>
                    <AppText>Table for 4, Friday around 7 pm</AppText>
                    <AppText variant="caption" tone="muted">
                      America/Chicago · Ask for patio seating
                    </AppText>
                  </View>
                </RuledPanel>
                <RuledPanel title="Suggested reply">
                  <AppText variant="caption" tone="muted">
                    {phase === "ended"
                      ? "Call ended. This sample was not spoken."
                      : phase === "paused"
                        ? "AI paused. Approval is unavailable."
                        : "Not spoken. Your approval is required."}
                  </AppText>
                  <Field
                    label="Suggested reply"
                    multiline
                    value={reply}
                    onChangeText={setReply}
                    editable={!inactive}
                    maxLength={1500}
                  />
                  <Button
                    label="Approve sample reply"
                    disabled={inactive || !reply.trim()}
                    onPress={() =>
                      setNotice(
                        "Approved locally for preview. No audio was played; the reply stays outside the transcript.",
                      )
                    }
                  />
                </RuledPanel>
              </View>
            </View>
            <RuledPanel title="Guide the next response">
              <Field
                label="Private instruction"
                hint="Preview only. Instructions guide the AI; they are not spoken aloud."
                placeholder="Accept 7:30, but ask for the patio."
                value={instruction}
                onChangeText={setInstruction}
                editable={!inactive}
                multiline
                maxLength={1000}
              />
              <View style={s.actions}>
                <Button
                  label="Send sample instruction"
                  disabled={inactive || !instruction.trim()}
                  onPress={() => {
                    setNotice(
                      "Instruction saved locally for preview. Nothing was sent to a call.",
                    );
                    setInstruction("");
                  }}
                />
                <Button
                  label={
                    phase === "paused"
                      ? "Resume AI preview"
                      : "Pause AI preview"
                  }
                  variant="secondary"
                  disabled={phase === "ended" || phase === "connecting"}
                  onPress={() => {
                    setPhase(phase === "paused" ? "approval" : "paused");
                    setNotice("Preview state changed. No audio is playing.");
                  }}
                />
                <Button
                  label={phase === "ended" ? "Call ended" : "End preview call"}
                  variant="destructive"
                  disabled={phase === "ended"}
                  onPress={() => {
                    setPhase("ended");
                    setNotice("Preview ended. No telephone call was placed.");
                  }}
                />
              </View>
            </RuledPanel>
          </ScrollView>
        </View>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}
const s = StyleSheet.create({
  safe: { flex: 1, backgroundColor: t.paper },
  shell: { flex: 1 },
  horizontal: { flexDirection: "row" },
  nav: { padding: 20, gap: 12, borderBottomWidth: 1, borderColor: t.line },
  navWide: {
    width: 210,
    borderRightWidth: 1,
    borderBottomWidth: 0,
    paddingTop: 40,
  },
  links: { flexDirection: "row", flexWrap: "wrap", gap: 8 },
  main: { flex: 1, minWidth: 0 },
  content: { padding: 20, gap: 24 },
  contentWide: {
    padding: 32,
    maxWidth: 1500,
    width: "100%",
    alignSelf: "center",
  },
  header: {
    flexDirection: "row",
    flexWrap: "wrap",
    alignItems: "center",
    gap: 16,
  },
  title: { flex: 1, minWidth: 220, gap: 12 },
  phases: { flexDirection: "row", flexWrap: "wrap", gap: 8 },
  workspace: { borderTopWidth: 1, borderColor: t.line },
  transcript: { flex: 1, minWidth: 0, paddingVertical: 24, gap: 28 },
  turn: { flexDirection: "row", gap: 14 },
  turnBody: {
    flex: 1,
    minWidth: 0,
    borderLeftWidth: 1,
    borderColor: t.green,
    paddingLeft: 16,
    gap: 8,
  },
  business: { borderColor: t.vermilion },
  rail: { gap: 16 },
  railWide: {
    width: 300,
    paddingLeft: 24,
    marginLeft: 24,
    borderLeftWidth: 1,
    borderColor: t.line,
  },
  goal: {
    borderWidth: 1,
    borderColor: t.green,
    borderRadius: t.radius,
    padding: 16,
    gap: 8,
  },
  actions: { flexDirection: "row", flexWrap: "wrap", gap: 12 },
});
