import { getHealth } from "@switchboard/api";
import { useRouter } from "expo-router";
import { useState } from "react";
import {
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  useWindowDimensions,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { theme as t } from "../theme";

type Page = "Calls" | "Settings";
const transcript = [
  {
    time: "00:00",
    speaker: "AI",
    text: "Hi, this is Switchboard, an AI assistant calling on behalf of a guest. I'd like to reserve a table for four this Friday around 7 pm.",
  },
  {
    time: "00:27",
    speaker: "RESTAURANT",
    text: "Let me check our availability. We're fully booked at 7 pm.",
  },
  {
    time: "01:03",
    speaker: "AI",
    text: "Do you have anything close to 7 pm for a party of four?",
  },
  { time: "01:32", speaker: "RESTAURANT", text: "Would 7:30 work instead?" },
];

function Button({
  label,
  onPress,
  destructive = false,
  secondary = false,
  disabled = false,
}: {
  label: string;
  onPress: () => void;
  destructive?: boolean;
  secondary?: boolean;
  disabled?: boolean;
}) {
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={label}
      disabled={disabled}
      onPress={onPress}
      style={({ pressed }) => [
        s.button,
        secondary && s.secondary,
        destructive && s.destructive,
        (disabled || pressed) && { opacity: 0.55 },
      ]}
    >
      <Text style={[s.buttonText, secondary && { color: t.ink }]}>{label}</Text>
    </Pressable>
  );
}

export default function CallPreview() {
  const router = useRouter();
  const wide = useWindowDimensions().width >= 1000;
  const [page, setPage] = useState<Page>("Calls");
  const [instruction, setInstruction] = useState("");
  const [reply, setReply] = useState("7:30 works. Is patio seating available?");
  const [notice, setNotice] = useState(
    "Scripted example. No call is connected.",
  );
  const [paused, setPaused] = useState(false);
  const [ended, setEnded] = useState(false);
  const business = "Marigold";
  const goal = "Table for 4, Friday around 7 pm";
  const kind: string = "Restaurant reservation";
  const [health, setHealth] = useState("Not checked");
  const [checking, setChecking] = useState(false);
  const inactive = ended || paused;
  async function checkServer() {
    setChecking(true);
    try {
      const result = await getHealth(
        process.env.EXPO_PUBLIC_API_URL || "http://localhost:8080",
        AbortSignal.timeout(5000),
      );
      setHealth(`${result.status} / ${result.mode}`);
    } catch {
      setHealth(
        "Server unavailable. Start moon run api:dev and check EXPO_PUBLIC_API_URL.",
      );
    } finally {
      setChecking(false);
    }
  }
  return (
    <SafeAreaView style={s.safe}>
      <View style={[s.shell, wide && s.shellWide]}>
        <View style={[s.nav, wide && s.navWide]}>
          <View>
            <Text style={s.mark}>◉</Text>
            <Text style={s.brand}>Switchboard</Text>
            <Text style={s.eyebrow}>A LITTLE HELP ON THE LINE</Text>
          </View>
          <View style={[s.navLinks, wide && { flexDirection: "column" }]}>
            {(["Calls", "New call", "Settings"] as const).map((item) => (
              <Pressable
                key={item}
                accessibilityRole="button"
                accessibilityState={{ selected: page === item }}
                onPress={() =>
                  item === "New call" ? router.push("/new") : setPage(item)
                }
                style={[
                  s.navItem,
                  page === item && { backgroundColor: t.green },
                ]}
              >
                <Text style={[s.navLabel, page === item && { color: t.white }]}>
                  {item}
                </Text>
              </Pressable>
            ))}
          </View>
          {wide && (
            <Text style={s.navFooter}>
              CSC 4330 / PROJECT 06{"\n\n"}Reservations. Appointments.{"\n"}You
              set the direction.
            </Text>
          )}
        </View>
        <ScrollView
          style={s.main}
          contentContainerStyle={s.mainContent}
          keyboardShouldPersistTaps="handled"
        >
          <View style={s.topline}>
            <Text style={s.eyebrow}>YOUR PERSONAL PHONE CONCIERGE</Text>
            <Text style={s.previewTag}>DESIGN PREVIEW</Text>
          </View>
          {page === "Calls" ? (
            <>
              <View style={s.headingRow}>
                <View style={{ flex: 1 }}>
                  <Text accessibilityRole="header" style={s.heading}>
                    {kind === "Appointment" ? "Appointment" : "Reservation"} at{" "}
                    {business || "Marigold"}
                  </Text>
                  <Text style={s.subtitle}>
                    {kind.toUpperCase()} / SCRIPTED EXAMPLE
                  </Text>
                </View>
                <Text style={s.status}>
                  {ended ? "ENDED" : paused ? "PAUSED" : "SIMULATION"}
                </Text>
              </View>
              <Text accessibilityLiveRegion="polite" style={s.notice}>
                {notice}
              </Text>
              <View style={[s.workspace, wide && { flexDirection: "row" }]}>
                <View style={s.transcript}>
                  <Text style={s.sectionLabel}>ON THE LINE</Text>
                  {transcript.map((turn) => (
                    <View key={turn.time} style={s.turn}>
                      <Text style={s.time}>[{turn.time}]</Text>
                      <View
                        style={[
                          s.turnBody,
                          {
                            borderLeftColor:
                              turn.speaker === "AI" ? t.green : t.vermilion,
                          },
                        ]}
                      >
                        <Text
                          style={[
                            s.speaker,
                            {
                              color:
                                turn.speaker === "AI" ? t.green : t.vermilion,
                            },
                          ]}
                        >
                          {turn.speaker}
                        </Text>
                        <Text style={s.body}>{turn.text}</Text>
                      </View>
                    </View>
                  ))}
                  <Text style={s.transcriptFoot}>
                    End of sample transcript. Live events arrive in Stage 3.
                  </Text>
                </View>
                <View
                  style={[
                    s.rail,
                    wide && {
                      width: 300,
                      borderLeftWidth: 1,
                      borderLeftColor: t.line,
                    },
                  ]}
                >
                  <Text accessibilityRole="header" style={s.smallHeading}>
                    Current goal
                  </Text>
                  <View style={s.goalBox}>
                    <Text style={s.body}>{goal}</Text>
                  </View>
                  <Text accessibilityRole="header" style={s.smallHeading}>
                    Suggested reply
                  </Text>
                  <Text style={s.help}>
                    Editable sample. No model is connected.
                  </Text>
                  <TextInput
                    accessibilityLabel="Suggested reply"
                    multiline
                    value={reply}
                    onChangeText={setReply}
                    editable={!inactive}
                    style={[s.input, s.reply]}
                  />
                  <Button
                    label="Approve sample reply"
                    disabled={inactive || !reply.trim()}
                    onPress={() =>
                      setNotice(
                        `Approved locally: “${reply}” Nothing was spoken or sent.`,
                      )
                    }
                  />
                </View>
              </View>
              <View style={s.composer}>
                <Text accessibilityRole="header" style={s.smallHeading}>
                  Guide the next response
                </Text>
                <Text style={s.help}>
                  Type an instruction for the AI. The template saves it locally;
                  response generation is planned.
                </Text>
                <TextInput
                  accessibilityLabel="Mid-call instruction"
                  placeholder="Accept 7:30, but ask for the patio."
                  placeholderTextColor={t.muted}
                  multiline
                  value={instruction}
                  onChangeText={setInstruction}
                  editable={!inactive}
                  style={s.input}
                />
                <View style={s.actions}>
                  <Button
                    label="Send instruction"
                    disabled={inactive || !instruction.trim()}
                    onPress={() => {
                      setNotice(
                        `Instruction saved for preview: “${instruction.trim()}”`,
                      );
                      setInstruction("");
                    }}
                  />
                  <Button
                    secondary
                    label={paused ? "Resume AI" : "Pause AI"}
                    disabled={ended}
                    onPress={() => {
                      setPaused(!paused);
                      setNotice(
                        paused
                          ? "Preview resumed."
                          : "Preview paused. No audio is playing.",
                      );
                    }}
                  />
                  <Button
                    destructive
                    label={ended ? "Restart preview" : "End call"}
                    onPress={() => {
                      setEnded(!ended);
                      setPaused(false);
                      setNotice(
                        ended
                          ? "Scripted example restarted. No call is connected."
                          : "Preview ended. No telephone call was made.",
                      );
                    }}
                  />
                </View>
              </View>
            </>
          ) : (
            <>
              <Text accessibilityRole="header" style={s.heading}>
                Keep it simple.
              </Text>
              <Text style={s.subtitle}>CONNECTION & PROVIDERS</Text>
              <Text style={s.smallHeading}>Template mode</Text>
              <Text style={s.body}>
                No API keys are required for the design preview. Real calling,
                live transcription and Claude responses will be implemented
                through the project issues.
              </Text>
              <Text style={s.smallHeading}>API readiness</Text>
              <Text accessibilityLiveRegion="polite" style={s.body}>
                {health}
              </Text>
              <Button
                label={checking ? "Checking…" : "Check API connection"}
                disabled={checking}
                onPress={() => {
                  void checkServer();
                }}
              />
              <Text style={s.help}>
                Provider secrets belong in the server environment. The app never
                asks for them.
              </Text>
            </>
          )}
          <Text style={s.footer}>
            SWITCHBOARD / BUILT FOR THE CONVERSATIONS THAT MOVE YOUR DAY FORWARD
          </Text>
        </ScrollView>
      </View>
    </SafeAreaView>
  );
}

const serif = Platform.OS === "android" ? "serif" : "Georgia";
const mono = Platform.OS === "ios" ? "Menlo" : "monospace";
const s = StyleSheet.create({
  safe: { flex: 1, backgroundColor: t.paper },
  shell: { flex: 1 },
  shellWide: { flexDirection: "row" },
  nav: { padding: 20, borderBottomWidth: 1, borderColor: t.line, gap: 20 },
  navWide: {
    width: 230,
    borderRightWidth: 1,
    borderBottomWidth: 0,
    paddingTop: 38,
  },
  mark: { fontSize: 42, color: t.green },
  brand: { fontFamily: serif, fontSize: 30, fontWeight: "700", color: t.ink },
  eyebrow: { fontSize: 10, letterSpacing: 2, color: t.muted, lineHeight: 18 },
  navLinks: { flexDirection: "row", gap: 8 },
  navItem: { paddingHorizontal: 16, paddingVertical: 14, borderRadius: 5 },
  navLabel: { fontSize: 16, color: t.ink },
  navFooter: {
    marginTop: "auto",
    fontSize: 11,
    color: t.muted,
    lineHeight: 20,
  },
  main: { flex: 1 },
  mainContent: { padding: 24, gap: 20 },
  topline: {
    flexDirection: "row",
    justifyContent: "space-between",
    flexWrap: "wrap",
    gap: 8,
  },
  previewTag: {
    fontFamily: mono,
    fontSize: 10,
    color: t.brass,
    letterSpacing: 1,
  },
  headingRow: {
    flexDirection: "row",
    gap: 16,
    alignItems: "center",
    flexWrap: "wrap",
  },
  heading: {
    fontFamily: serif,
    fontWeight: "700",
    fontSize: 36,
    color: t.ink,
    lineHeight: 43,
  },
  subtitle: {
    fontSize: 10,
    letterSpacing: 2,
    color: t.muted,
    marginTop: 12,
    lineHeight: 18,
  },
  status: {
    backgroundColor: t.wash,
    padding: 10,
    color: t.green,
    fontFamily: mono,
    fontSize: 11,
  },
  notice: { fontSize: 13, color: t.muted, lineHeight: 20 },
  workspace: { borderTopWidth: 1, borderBottomWidth: 1, borderColor: t.line },
  transcript: { flex: 1, paddingVertical: 24, paddingRight: 16, gap: 25 },
  sectionLabel: {
    fontSize: 10,
    letterSpacing: 2,
    color: t.muted,
    marginTop: 8,
  },
  turn: { flexDirection: "row", gap: 16 },
  time: { fontFamily: mono, fontSize: 11, color: t.muted, paddingTop: 4 },
  turnBody: { flex: 1, borderLeftWidth: 1, paddingLeft: 16, gap: 7 },
  speaker: { fontSize: 10, fontWeight: "700", letterSpacing: 1 },
  body: { fontSize: 16, lineHeight: 25, color: t.ink },
  transcriptFoot: { fontSize: 12, color: t.muted, lineHeight: 20 },
  rail: { paddingVertical: 24, paddingHorizontal: 16, gap: 18 },
  smallHeading: {
    fontFamily: serif,
    fontSize: 25,
    fontWeight: "700",
    color: t.ink,
  },
  goalBox: {
    padding: 18,
    borderWidth: 1,
    borderColor: t.green,
    borderRadius: 5,
  },
  help: { fontSize: 13, lineHeight: 20, color: t.muted },
  input: {
    borderWidth: 1,
    borderColor: t.green,
    borderRadius: 5,
    padding: 16,
    fontSize: 16,
    color: t.ink,
    minHeight: 58,
    textAlignVertical: "top",
  },
  reply: { backgroundColor: t.wash, minHeight: 110 },
  composer: { gap: 16 },
  actions: { flexDirection: "row", flexWrap: "wrap", gap: 12 },
  button: {
    backgroundColor: t.green,
    borderWidth: 1,
    borderColor: t.green,
    borderRadius: 5,
    paddingVertical: 15,
    paddingHorizontal: 18,
    justifyContent: "center",
    minHeight: 48,
  },
  secondary: { backgroundColor: t.paper },
  destructive: { backgroundColor: t.vermilion, borderColor: t.vermilion },
  buttonText: {
    color: t.white,
    fontSize: 14,
    fontWeight: "600",
    textAlign: "center",
  },
  footer: {
    fontSize: 9,
    letterSpacing: 1.5,
    color: t.muted,
    lineHeight: 18,
    marginTop: 20,
  },
});
