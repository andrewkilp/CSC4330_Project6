import {
  actOnDemo,
  applyDemoFrame,
  type DemoAction,
  type DemoMeta,
  type DemoView,
  pollDemo,
  startDemo,
} from "@switchboard/api";
import { useEffect, useRef, useState } from "react";
import {
  Pressable,
  ScrollView,
  Text,
  TextInput,
  useWindowDimensions,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { theme as t } from "../theme";
import { s } from "./demo-styles";

const api = process.env.EXPO_PUBLIC_API_URL || "http://127.0.0.1:8080";
type Page = "Calls" | "New call" | "About";
function Button({
  label,
  onPress,
  disabled = false,
  secondary = false,
  destructive = false,
}: {
  label: string;
  onPress: () => void;
  disabled?: boolean;
  secondary?: boolean;
  destructive?: boolean;
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
        (pressed || disabled) && { opacity: 0.5 },
      ]}
    >
      <Text style={[s.buttonText, secondary && { color: t.ink }]}>{label}</Text>
    </Pressable>
  );
}
function clock(seconds: number) {
  return `${Math.floor(seconds / 60)
    .toString()
    .padStart(2, "0")}:${Math.floor(seconds % 60)
    .toString()
    .padStart(2, "0")}`;
}

export default function DemoScreen() {
  const wide = useWindowDimensions().width >= 1000;
  const [page, setPage] = useState<Page>("Calls");
  const [scenario, setScenario] = useState<DemoMeta["scenario"]>("restaurant");
  const [view, setView] = useState<DemoView | null>(null);
  const latest = useRef(view);
  latest.current = view;
  const [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const [instruction, setInstruction] = useState(""),
    [reply, setReply] = useState("");
  const response = view?.state.currentResponseId
    ? view.state.responses[view.state.currentResponseId]
    : undefined;
  const ended = view?.meta.phase === "ended",
    paused = view?.meta.paused;
  const active = !!view && !ended;
  useEffect(() => {
    setReply(response?.id ? response.text : "");
  }, [response?.id, response?.text]);
  const sessionId = view?.state.callId;
  useEffect(() => {
    if (!sessionId || ended) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      try {
        const frame = await pollDemo(
          api,
          sessionId as string,
          latest.current?.state.lastSeq ?? 0,
          controller.signal,
        );
        if (!controller.signal.aborted) {
          setView((old) => applyDemoFrame(old, frame));
          setError("");
        }
      } catch (e) {
        if (!controller.signal.aborted)
          setError(
            e instanceof Error ? e.message : "Connection lost. Retrying…",
          );
      } finally {
        if (!controller.signal.aborted) timer = setTimeout(poll, 350);
      }
    }
    void poll();
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [sessionId, ended]);
  async function begin() {
    setBusy(true);
    setError("");
    try {
      const frame = await startDemo(api, scenario);
      setView(applyDemoFrame(null, frame));
      setPage("Calls");
      setInstruction("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not start the demo.");
    } finally {
      setBusy(false);
    }
  }
  async function action(action: DemoAction["action"], text?: string) {
    if (!view) return;
    setBusy(true);
    setError("");
    try {
      const frame = await actOnDemo(api, view.state.callId, {
        action,
        text,
        response_id: response?.id,
        context_revision: view.state.contextRevision,
      });
      setView((old) => applyDemoFrame(old, frame));
      if (action === "instruction") setInstruction("");
    } catch (e) {
      setError(
        e instanceof Error ? e.message : "The action could not be applied.",
      );
    } finally {
      setBusy(false);
    }
  }
  const steerable =
    active &&
    (view.meta.phase === "awaiting_approval" || view.meta.phase === "speaking");
  const presets =
    (view?.meta.scenario ?? scenario) === "restaurant"
      ? [
          "Accept 7:30; ask for indoor seating.",
          "Accept 7:30; ask for patio seating.",
          "Don't book. Politely decline.",
        ]
      : [
          "Thursday at 3 works; just a trim.",
          "Thursday at 3 works; a full haircut.",
          "Don't book. Politely decline.",
        ];
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
            {(["Calls", "New call", "About"] as const).map((item) => (
              <Pressable
                accessibilityRole="button"
                accessibilityState={{ selected: page === item }}
                key={item}
                onPress={() => setPage(item)}
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
              CSC 4330 / PROJECT 06{"\n\n"}You set the direction.{"\n"}We take
              the next turn.{"\n\n"}LOCAL DEMO / NO API COST
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
            <Text style={s.previewTag}>SIMULATED CALLS · NO REAL DIALING</Text>
          </View>
          {!!error && (
            <View style={[s.goalBox, { borderColor: t.vermilion }]}>
              <Text
                accessibilityLiveRegion="assertive"
                style={[s.body, { color: t.vermilion }]}
              >
                {error}
              </Text>
              <Text style={s.help}>
                Start the local demo with moon run api:demo. A refresh clears
                the browser view.
              </Text>
            </View>
          )}
          {page === "About" ? (
            <>
              <Text accessibilityRole="header" style={s.heading}>
                A voice of your own.
              </Text>
              <Text style={s.body}>
                Switchboard helps arrange reservations and appointments while
                you stay in control of the conversation.
              </Text>
              <Text style={s.smallHeading}>What this demo does</Text>
              <Text style={s.body}>
                A Go server runs two fictional conversations. Transcript turns
                stream in as text, private instructions revise the suggested
                reply. Suggested replies wait for your approval before delivery.
                Pause and end controls cancel obsolete work.
              </Text>
              <Text style={s.smallHeading}>A clear boundary</Text>
              <Text style={s.body}>
                This is a scripted simulation, with no phone network, Claude,
                ElevenLabs, microphone, audio recording or real booking.
                Unrecognized instructions produce a clarification reply. You can
                edit any suggestion yourself.
              </Text>
              <Button
                secondary
                label="Try a conversation"
                onPress={() => setPage("New call")}
              />
            </>
          ) : page === "New call" || !view ? (
            <>
              <Text accessibilityRole="header" style={s.heading}>
                Let us make the call.
              </Text>
              <Text style={s.subtitle}>
                TWO EVERYDAY CONVERSATIONS. YOU STAY IN CONTROL.
              </Text>
              <Text style={s.body}>
                Pick a scenario, watch the business ask a question, then guide
                the next reply.
              </Text>
              <View style={[s.workspace, { paddingVertical: 24, gap: 16 }]}>
                {(["restaurant", "appointment"] as const).map((option) => (
                  <Pressable
                    key={option}
                    accessibilityRole="radio"
                    accessibilityState={{ checked: scenario === option }}
                    onPress={() => setScenario(option)}
                    style={[
                      s.goalBox,
                      scenario === option && { backgroundColor: t.wash },
                    ]}
                  >
                    <Text style={s.smallHeading}>
                      {option === "restaurant"
                        ? "An evening at Marigold"
                        : "A little time at Juniper"}
                    </Text>
                    <Text style={[s.body, { marginTop: 10 }]}>
                      {option === "restaurant"
                        ? "Table for four · Friday, November 13 · Around 7 pm"
                        : "Haircut · Thursday, November 12 · Afternoon"}
                    </Text>
                    <Text style={[s.help, { marginTop: 8 }]}>
                      {option === "restaurant"
                        ? "The restaurant offers 7:30. You choose the seating."
                        : "The studio offers 3 pm. You choose a trim or a full cut."}
                    </Text>
                  </Pressable>
                ))}
              </View>
              <Button
                label={busy ? "Starting…" : "Start simulated call"}
                disabled={busy || active}
                onPress={() => {
                  void begin();
                }}
              />
              {active && (
                <Text style={s.help}>
                  A demo is already running. Return to Calls and end it before
                  starting another.
                </Text>
              )}
              <Text style={s.help}>
                Fictional recipient +1 (202) 555-0123. No keys, charges or real
                calls.
              </Text>
            </>
          ) : (
            <>
              <View
                style={[
                  s.headingRow,
                  !wide && {
                    flexDirection: "column",
                    alignItems: "flex-start",
                  },
                ]}
              >
                <View style={wide ? { flex: 1 } : { width: "100%" }}>
                  <Text accessibilityRole="header" style={s.heading}>
                    {view.meta.scenario === "restaurant"
                      ? "Reservation at Marigold"
                      : "Appointment at Juniper"}
                  </Text>
                  <Text style={s.subtitle}>
                    {view.meta.scenario === "restaurant"
                      ? "RESTAURANT RESERVATION"
                      : "APPOINTMENT SCHEDULING"}{" "}
                    / DEMO GUEST
                  </Text>
                </View>
                <Text style={s.status}>
                  {ended
                    ? "ENDED"
                    : paused
                      ? "PAUSED"
                      : view.meta.phase === "awaiting_approval"
                        ? "YOUR TURN"
                        : "LIVE DEMO"}{" "}
                  {clock(view.meta.elapsed_seconds)}
                </Text>
              </View>
              <Text accessibilityLiveRegion="polite" style={s.notice}>
                {view.meta.notice}
              </Text>
              <View style={[s.workspace, wide && { flexDirection: "row" }]}>
                <View style={s.transcript}>
                  <Text style={s.sectionLabel}>
                    ON THE LINE · {view.state.lastSeq} EVENTS
                  </Text>
                  {view.state.turnOrder.map((id) => {
                    const turn = view.state.turns[id];
                    return (
                      turn && (
                        <View key={id} style={s.turn}>
                          <Text style={s.time}>
                            [{clock(turn.started_at_ms / 1000)}]
                          </Text>
                          <View
                            style={[
                              s.turnBody,
                              {
                                borderLeftColor:
                                  turn.speaker === "assistant"
                                    ? t.green
                                    : t.vermilion,
                              },
                            ]}
                          >
                            <Text
                              style={[
                                s.speaker,
                                {
                                  color:
                                    turn.speaker === "assistant"
                                      ? t.green
                                      : t.vermilion,
                                },
                              ]}
                            >
                              {turn.speaker === "assistant"
                                ? "AI ASSISTANT"
                                : view.meta.scenario === "restaurant"
                                  ? "MARIGOLD"
                                  : "JUNIPER STUDIO"}
                            </Text>
                            <Text
                              style={[
                                s.body,
                                !turn.is_final && { color: t.muted },
                              ]}
                            >
                              {turn.text}
                              {!turn.is_final ? " …" : ""}
                            </Text>
                            {!turn.is_final && (
                              <Text style={s.help}>Transcribing…</Text>
                            )}
                          </View>
                        </View>
                      )
                    );
                  })}
                </View>
                <View
                  style={[
                    s.rail,
                    wide && {
                      width: 320,
                      borderLeftWidth: 1,
                      borderLeftColor: t.line,
                    },
                  ]}
                >
                  <Text style={s.smallHeading}>Current goal</Text>
                  <View style={s.goalBox}>
                    <Text style={s.body}>
                      {view.meta.scenario === "restaurant"
                        ? "A table for four, Friday around 7 pm. Confirm seating with me."
                        : "A Thursday afternoon haircut. Confirm the service with me."}
                    </Text>
                  </View>
                  <Text style={s.smallHeading}>Suggested reply</Text>
                  <Text style={s.help}>
                    {ended
                      ? "Conversation finished."
                      : paused
                        ? "Paused. Resume to generate the latest suggestion."
                        : response
                          ? "Review or edit before delivery. Scripted response generator."
                          : "Waiting for the business to finish its question…"}
                  </Text>
                  <TextInput
                    accessibilityLabel="Suggested reply"
                    multiline
                    value={reply}
                    onChangeText={setReply}
                    editable={
                      !!response &&
                      !paused &&
                      !ended &&
                      !busy &&
                      response.status === "suggested"
                    }
                    maxLength={1500}
                    style={[s.input, s.reply]}
                  />
                  <Button
                    label={
                      view.meta.phase === "speaking"
                        ? "Delivering reply…"
                        : "Approve & deliver reply"
                    }
                    disabled={
                      busy ||
                      paused ||
                      ended ||
                      response?.status !== "suggested" ||
                      !reply.trim()
                    }
                    onPress={() => {
                      void action("approve", reply);
                    }}
                  />
                  {view.state.contextRevision > 0 && (
                    <Text style={s.previewTag}>
                      DIRECTION REVISION {view.state.contextRevision}
                    </Text>
                  )}
                </View>
              </View>
              {!ended && (
                <View style={s.composer}>
                  <Text style={s.smallHeading}>Guide the next response</Text>
                  <Text style={s.help}>
                    This stays private. Try a direction below, or edit the
                    suggested reply directly.
                  </Text>
                  <View style={s.actions}>
                    {presets.map((preset) => (
                      <Pressable
                        key={preset}
                        accessibilityRole="button"
                        disabled={!steerable || busy}
                        onPress={() => setInstruction(preset)}
                        style={[
                          s.goalBox,
                          { padding: 10, opacity: steerable ? 1 : 0.5 },
                        ]}
                      >
                        <Text style={s.help}>{preset}</Text>
                      </Pressable>
                    ))}
                  </View>
                  <TextInput
                    accessibilityLabel="Mid-call instruction"
                    value={instruction}
                    onChangeText={setInstruction}
                    placeholder={presets[0]}
                    placeholderTextColor={t.muted}
                    multiline
                    maxLength={1000}
                    editable={steerable && !busy}
                    style={s.input}
                  />
                  <View style={s.actions}>
                    <Button
                      label="Send instruction"
                      disabled={!steerable || busy || !instruction.trim()}
                      onPress={() => {
                        void action("instruction", instruction);
                      }}
                    />
                    <Button
                      secondary
                      label={paused ? "Resume AI" : "Pause AI"}
                      disabled={busy}
                      onPress={() => {
                        void action(paused ? "resume" : "pause");
                      }}
                    />
                    <Button
                      destructive
                      label="End call"
                      disabled={busy}
                      onPress={() => {
                        void action("end");
                      }}
                    />
                  </View>
                  {view.state.instructions &&
                    Object.values(view.state.instructions).length > 0 && (
                      <View style={s.goalBox}>
                        <Text style={s.sectionLabel}>PRIVATE DIRECTIONS</Text>
                        {Object.values(view.state.instructions).map((item) => (
                          <Text key={item.id} style={s.help}>
                            Applied · {item.text}
                          </Text>
                        ))}
                      </View>
                    )}
                </View>
              )}
              {view.state.summary && (
                <View style={[s.goalBox, { backgroundColor: t.wash, gap: 12 }]}>
                  <Text style={s.sectionLabel}>SIMULATED OUTCOME</Text>
                  <Text accessibilityRole="header" style={s.smallHeading}>
                    {view.state.summary.outcome === "confirmed"
                      ? "All set. In the demo."
                      : view.state.summary.outcome === "declined"
                        ? "Politely declined."
                        : "No booking confirmed."}
                  </Text>
                  <Text style={s.body}>
                    {view.state.summary.confirmed_details
                      .confirmation_reference ??
                      "The transcript is your record of this simulated call."}
                  </Text>
                  {view.state.summary.confirmed_details.scheduled_at && (
                    <Text style={s.body}>
                      {view.meta.scenario === "restaurant"
                        ? "Friday, November 13 at 7:30 pm · 4 guests"
                        : "Thursday, November 12 at 3 pm"}
                    </Text>
                  )}
                  {view.state.summary.confirmed_details.service && (
                    <Text style={s.body}>
                      Service: {view.state.summary.confirmed_details.service}
                    </Text>
                  )}
                  <Text style={s.help}>
                    No real reservation or appointment was made.
                  </Text>
                  <Button
                    label="Try another demo"
                    onPress={() => {
                      setView(null);
                      setPage("New call");
                    }}
                  />
                </View>
              )}
            </>
          )}
          <Text style={s.footer}>
            SWITCHBOARD / YOU SET THE DIRECTION. WE TAKE THE NEXT TURN.
          </Text>
        </ScrollView>
      </View>
    </SafeAreaView>
  );
}
