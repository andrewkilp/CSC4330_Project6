import { router } from "expo-router";
import { useState } from "react";
import {
  ScrollView,
  StyleSheet,
  useWindowDimensions,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { theme as t } from "../theme";
import { AppText, Badge, Button, Field, RuledPanel } from "./index";

export default function ComponentGallery() {
  const wide = useWindowDimensions().width >= t.layout.breakpoint;
  const spacing = wide ? t.layout.wide : t.layout.narrow;
  const [reply, setReply] = useState("7:30 works. Is patio seating available?");
  const [instruction, setInstruction] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState(
    "Local component examples. No call is connected.",
  );
  return (
    <SafeAreaView style={s.safe}>
      <ScrollView
        testID="component-gallery-scroll"
        contentContainerStyle={[s.shell, wide && s.wide]}
        keyboardShouldPersistTaps="handled"
      >
        <View style={[s.sidebar, wide && s.sidebarWide]}>
          <AppText variant="heading" style={s.brand}>
            Switchboard
          </AppText>
          <AppText variant="label" tone="muted">
            THE CONVERSATION TOOLKIT
          </AppText>
          <View style={s.rule} />
          <Button
            label="Return to preview"
            variant="secondary"
            onPress={() => router.replace("/")}
          />
          <AppText variant="caption" tone="muted">
            Shared foundations for reservations and ordinary appointments.
          </AppText>
        </View>
        <View
          style={[
            s.main,
            { padding: spacing.pagePadding, gap: spacing.sectionGap },
          ]}
        >
          <View style={s.row}>
            <AppText variant="label" tone="muted">
              COMPONENT GALLERY
            </AppText>
            <Badge label="MOCK DATA" tone="warning" />
          </View>
          <View style={s.heading}>
            <AppText variant="display" accessibilityRole="header">
              Reservation at Marigold
            </AppText>
            <AppText variant="label" tone="muted">
              RESTAURANT RESERVATION · SHARED UI EXAMPLES
            </AppText>
          </View>
          <AppText
            variant="caption"
            tone="muted"
            accessibilityLiveRegion="polite"
          >
            {notice}
          </AppText>
          <View style={[s.workspace, wide && s.wide]}>
            <RuledPanel
              title="On the line"
              style={[s.transcript, wide && s.transcriptWide]}
              accessory={<Badge label="PREVIEW" tone="success" />}
            >
              {[
                {
                  time: "00:00",
                  speaker: "AI",
                  text: "Hi, this is Switchboard, an AI assistant calling about a table for four this Friday around 7 pm.",
                },
                {
                  time: "00:27",
                  speaker: "RESTAURANT",
                  text: "Let me check our availability. We’re fully booked at 7 pm.",
                },
                {
                  time: "01:32",
                  speaker: "RESTAURANT",
                  text: "Would 7:30 work instead?",
                },
              ].map((turn) => (
                <View key={turn.time} style={s.turn}>
                  <AppText variant="mono" tone="muted">
                    [{turn.time}]
                  </AppText>
                  <View
                    style={[
                      s.turnBody,
                      {
                        borderLeftColor:
                          turn.speaker === "AI" ? t.green : t.vermilion,
                      },
                    ]}
                  >
                    <Badge
                      label={turn.speaker}
                      tone={turn.speaker === "AI" ? "success" : "danger"}
                    />
                    <AppText>{turn.text}</AppText>
                  </View>
                </View>
              ))}
              <AppText variant="caption" tone="muted">
                Illustrative text only. Suggested replies remain separate from
                spoken turns.
              </AppText>
            </RuledPanel>
            <View style={[s.rail, wide && s.railWide]}>
              <RuledPanel title="Current goal">
                <AppText>Table for 4, Friday around 7 pm</AppText>
                <Badge label="AWAITING REVIEW" tone="warning" />
              </RuledPanel>
              <RuledPanel title="Suggested reply">
                <Field
                  label="Reply text"
                  value={reply}
                  onChangeText={setReply}
                  multiline
                  hint="Editable component example."
                />
                <Button
                  label="Approve sample"
                  disabled={!reply.trim()}
                  onPress={() =>
                    setNotice(
                      "Sample approved locally. Nothing was sent or spoken.",
                    )
                  }
                />
              </RuledPanel>
            </View>
          </View>
          <RuledPanel title="Guide the next response">
            <Field
              label="Private instruction"
              required
              value={instruction}
              onChangeText={(value) => {
                setInstruction(value);
                setError("");
              }}
              error={error}
              placeholder="Accept 7:30, but ask for the patio."
              hint="Try submitting an empty field to see validation feedback."
              multiline
            />
            <View style={s.actions}>
              <Button
                label="Preview feedback"
                onPress={() => {
                  if (!instruction.trim()) {
                    setError("Add an instruction before continuing.");
                    return;
                  }
                  setNotice(
                    "Instruction feedback previewed locally. Nothing was sent.",
                  );
                }}
              />
              <Button
                label="Pause example"
                variant="secondary"
                onPress={() =>
                  setNotice(
                    "Pause button example selected. No audio is playing.",
                  )
                }
              />
              <Button
                label="End example"
                variant="destructive"
                onPress={() =>
                  setNotice(
                    "End button example selected. No telephone call was made.",
                  )
                }
              />
            </View>
          </RuledPanel>
          <RuledPanel title="Clear states, without guessing">
            <View style={s.actions}>
              <Badge label="PENDING" />
              <Badge label="APPLIED" tone="success" />
              <Badge label="NEEDS ATTENTION" tone="warning" />
              <Badge label="FAILED" tone="danger" />
            </View>
            <View style={s.actions}>
              <Button
                label="Save sample"
                loading
                loadingLabel="Saving sample…"
                onPress={() => {}}
              />
              <Button label="Unavailable action" disabled onPress={() => {}} />
            </View>
            <Field
              label="Destination example"
              value="+1"
              error="Enter a complete international number, beginning with +."
              onChangeText={() => {}}
            />
            <Field
              label="Read-only example"
              value="Marigold (mock)"
              editable={false}
            />
          </RuledPanel>
          <AppText variant="label" tone="muted">
            SWITCHBOARD / PAPER, INK & A LITTLE ORDER
          </AppText>
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

const s = StyleSheet.create({
  safe: { flex: 1, backgroundColor: t.paper },
  shell: {
    minHeight: "100%",
    maxWidth: t.layout.maxWidth + 220,
    width: "100%",
    alignSelf: "center",
  },
  wide: { flexDirection: "row" },
  sidebar: {
    padding: t.space.xl,
    borderBottomWidth: 1,
    borderColor: t.line,
    gap: t.space.lg,
  },
  sidebarWide: {
    width: 230,
    borderRightWidth: 1,
    borderBottomWidth: 0,
    paddingTop: t.space.section,
  },
  brand: { fontSize: 26, lineHeight: 34 },
  rule: {
    height: 1,
    backgroundColor: t.brass,
    width: "100%",
    marginVertical: t.space.sm,
  },
  main: { flex: 1, minWidth: 0 },
  row: {
    flexDirection: "row",
    flexWrap: "wrap",
    alignItems: "center",
    justifyContent: "space-between",
    gap: t.space.md,
  },
  heading: { gap: t.space.md },
  workspace: { gap: t.space.xl },
  transcript: { flex: 1, gap: t.space.xl },
  transcriptWide: { paddingRight: t.space.xl },
  rail: { gap: t.space.xl },
  railWide: {
    width: 300,
    borderLeftWidth: 1,
    borderLeftColor: t.line,
    paddingLeft: t.space.xl,
  },
  turn: { flexDirection: "row", alignItems: "flex-start", gap: t.space.md },
  turnBody: {
    flex: 1,
    borderLeftWidth: 1,
    paddingLeft: t.space.lg,
    gap: t.space.sm,
  },
  actions: { flexDirection: "row", flexWrap: "wrap", gap: t.space.md },
});
