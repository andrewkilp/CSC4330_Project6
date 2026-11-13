import { type EventState, selectTranscript, type Turn } from "@switchboard/api";
import { useRef, useState } from "react";
import {
  ScrollView,
  StyleSheet,
  useWindowDimensions,
  View,
} from "react-native";
import { AppText, Button } from "../../components";
import { theme as t } from "../../theme";

function Timestamp({ ms }: { ms: number }) {
  const seconds = Math.floor(ms / 1000);
  return (
    <AppText variant="mono" tone="muted">
      [{String(Math.floor(seconds / 60)).padStart(2, "0")}:
      {String(seconds % 60).padStart(2, "0")}]
    </AppText>
  );
}
// From this text scale up, timestamps sit above the words instead of beside them.
const LARGE_TEXT = 1.3;
function clockLabel(ms: number) {
  const seconds = Math.floor(ms / 1000);
  const m = Math.floor(seconds / 60),
    r = seconds % 60;
  return `${m} minute${m === 1 ? "" : "s"} ${r} second${r === 1 ? "" : "s"}`;
}
function TranscriptTurn({
  turn,
  businessTag,
  provisional = false,
}: {
  turn: Turn;
  businessTag: string;
  provisional?: boolean;
}) {
  const stacked = useWindowDimensions().fontScale >= LARGE_TEXT;
  const speaker = turn.speaker === "assistant" ? "AI" : "Business";
  return (
    <View
      testID={`turn-${turn.id}`}
      style={[s.turn, stacked && s.stacked]}
      // Read as one item: speaker, time and words, instead of three fragments.
      accessible
      accessibilityLabel={`${speaker}${provisional ? ", still being transcribed" : ""}, at ${clockLabel(turn.started_at_ms)}: ${turn.text || "listening"}`}
    >
      <Timestamp ms={turn.started_at_ms} />
      <View style={[s.body, turn.speaker === "business" && s.business]}>
        <View style={s.tagRow}>
          <View
            style={[
              s.tag,
              turn.speaker === "assistant" ? s.tagAi : s.tagBusiness,
            ]}
          >
            <AppText variant="label" style={s.tagText}>
              {turn.speaker === "assistant" ? "AI" : businessTag}
            </AppText>
          </View>
          {provisional ? (
            <AppText variant="label" tone="muted">
              PROVISIONAL
            </AppText>
          ) : null}
        </View>
        <AppText tone={provisional ? "muted" : "ink"}>
          {turn.text || "Listening…"}
        </AppText>
      </View>
    </View>
  );
}
export function Transcript({
  state,
  preview = false,
  businessTag = "BUSINESS",
}: {
  state: EventState;
  preview?: boolean;
  // The reference's short label for the business side, such as RESTAURANT.
  businessTag?: string;
}) {
  const { finalized, provisional } = selectTranscript(state);
  const scroll = useRef<ScrollView>(null);
  const following = useRef(true);
  const lastOffset = useRef(0);
  const [away, setAway] = useState(false);
  const [unread, setUnread] = useState(false);
  return (
    <View style={s.container}>
      <AppText variant="label" tone="muted">
        {preview
          ? "ON THE LINE / SAMPLE TRANSCRIPT"
          : "ON THE LINE / TRANSCRIPT"}
      </AppText>
      <ScrollView
        ref={scroll}
        testID="transcript-scroll"
        accessibilityLabel="Call transcript"
        // Keyboard users can focus the transcript and scroll it with the arrow keys.
        focusable
        style={s.scroll}
        contentContainerStyle={s.content}
        nestedScrollEnabled
        scrollEventThrottle={16}
        onScroll={({
          nativeEvent: { contentOffset, contentSize, layoutMeasurement },
        }) => {
          const nearEnd =
            contentSize.height - layoutMeasurement.height - contentOffset.y <
            48;
          // A content resize also emits scroll events. Only movement upward
          // retires following; growth below the reader must not impersonate it.
          if (nearEnd) following.current = true;
          else if (contentOffset.y < lastOffset.current - 1)
            following.current = false;
          lastOffset.current = contentOffset.y;
          setAway(!following.current);
          if (nearEnd) setUnread(false);
        }}
        onContentSizeChange={() => {
          if (following.current)
            scroll.current?.scrollToEnd({ animated: false });
          else setUnread(true);
        }}
      >
        {finalized.length === 0 && (
          <AppText tone="muted">No finalized speech yet.</AppText>
        )}
        {finalized.map((turn) => (
          <TranscriptTurn key={turn.id} turn={turn} businessTag={businessTag} />
        ))}
        {provisional.length > 0 && (
          <View testID="provisional-turns" style={s.provisional}>
            <AppText variant="caption" tone="muted">
              Still being transcribed · wording may change
            </AppText>
            {provisional.map((turn) => (
              <TranscriptTurn
                key={turn.id}
                turn={turn}
                businessTag={businessTag}
                provisional
              />
            ))}
          </View>
        )}
      </ScrollView>
      {away && (
        <Button
          label={
            unread ? "New transcript text · Jump to latest" : "Jump to latest"
          }
          variant="secondary"
          onPress={() => {
            following.current = true;
            setAway(false);
            setUnread(false);
            scroll.current?.scrollToEnd({ animated: false });
          }}
        />
      )}
    </View>
  );
}
const s = StyleSheet.create({
  container: { gap: 16, minWidth: 0 },
  scroll: { height: 420, flexGrow: 0 },
  content: { gap: 28, paddingVertical: 12, paddingRight: 8 },
  turn: { flexDirection: "row", gap: 12 },
  stacked: { flexDirection: "column", gap: 4 },
  body: {
    flex: 1,
    minWidth: 0,
    borderLeftWidth: 1,
    borderColor: t.green,
    paddingLeft: 14,
    gap: 8,
  },
  business: { borderColor: t.vermilion },
  tagRow: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 8,
    alignItems: "center",
  },
  tag: {
    alignSelf: "flex-start",
    maxWidth: "100%",
    borderRadius: t.radius,
    paddingHorizontal: t.space.sm,
    paddingVertical: 1,
  },
  tagAi: { backgroundColor: t.green },
  tagBusiness: { backgroundColor: t.vermilion },
  tagText: { color: t.white, letterSpacing: 1 },
  provisional: {
    gap: 16,
    paddingTop: 16,
    borderTopWidth: 1,
    borderColor: t.line,
  },
});
