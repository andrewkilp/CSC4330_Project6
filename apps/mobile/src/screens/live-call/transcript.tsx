import { type EventState, selectTranscript, type Turn } from "@switchboard/api";
import { useRef, useState } from "react";
import { ScrollView, StyleSheet, View } from "react-native";
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
function TranscriptTurn({
  turn,
  provisional = false,
}: {
  turn: Turn;
  provisional?: boolean;
}) {
  return (
    <View testID={`turn-${turn.id}`} style={s.turn}>
      <Timestamp ms={turn.started_at_ms} />
      <View style={[s.body, turn.speaker === "business" && s.business]}>
        <AppText
          variant="label"
          tone={turn.speaker === "assistant" ? "green" : "danger"}
        >
          {turn.speaker === "assistant" ? "AI" : "BUSINESS"}
          {provisional ? " · PROVISIONAL" : ""}
        </AppText>
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
}: {
  state: EventState;
  preview?: boolean;
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
          <TranscriptTurn key={turn.id} turn={turn} />
        ))}
        {provisional.length > 0 && (
          <View testID="provisional-turns" style={s.provisional}>
            <AppText variant="caption" tone="muted">
              Still being transcribed · wording may change
            </AppText>
            {provisional.map((turn) => (
              <TranscriptTurn key={turn.id} turn={turn} provisional />
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
  body: {
    flex: 1,
    minWidth: 0,
    borderLeftWidth: 1,
    borderColor: t.green,
    paddingLeft: 14,
    gap: 8,
  },
  business: { borderColor: t.vermilion },
  provisional: {
    gap: 16,
    paddingTop: 16,
    borderTopWidth: 1,
    borderColor: t.line,
  },
});
