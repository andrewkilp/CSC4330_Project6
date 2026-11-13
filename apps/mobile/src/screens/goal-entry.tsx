import { CallRequestError, createCall } from "@switchboard/api";
import { randomUUID } from "expo-crypto";
import { useRouter } from "expo-router";
import {
  type ReactNode,
  type RefObject,
  useEffect,
  useRef,
  useState,
} from "react";
import {
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  useWindowDimensions,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { AppText, Badge, Button, Field, RuledPanel } from "../components";
import { theme as t } from "../theme";
import { useGoalDraft } from "./goal-context";
import { type GoalDraft, validateDraft } from "./goal-draft";

function GoalShell({
  title,
  subtitle,
  children,
  scrollRef,
}: {
  title: string;
  subtitle: string;
  children: ReactNode;
  scrollRef?: RefObject<ScrollView | null>;
}) {
  const router = useRouter();
  const wide = useWindowDimensions().width >= 1000;
  return (
    <SafeAreaView style={s.safe}>
      <View style={[s.shell, wide && s.wide]}>
        <View style={[s.nav, wide && s.navWide]}>
          <View>
            <AppText style={s.mark}>◉</AppText>
            <AppText variant="heading">Switchboard</AppText>
            <AppText variant="label" tone="muted">
              A LITTLE HELP ON THE LINE
            </AppText>
          </View>
          <View style={s.navActions}>
            <Button
              label="Calls"
              variant="secondary"
              onPress={() => router.push("/")}
            />
            <Badge label="NEW CALL" tone="success" />
          </View>
          {wide && (
            <AppText variant="caption" tone="muted" style={s.navFooter}>
              Reservations. Appointments.{"\n"}You set the direction.
            </AppText>
          )}
        </View>
        <ScrollView
          ref={scrollRef}
          style={s.scroll}
          contentContainerStyle={[s.content, wide && s.contentWide]}
          keyboardShouldPersistTaps="handled"
        >
          <View style={s.top}>
            <AppText variant="label" tone="muted">
              YOUR PERSONAL PHONE CONCIERGE
            </AppText>
            <Badge label="MOCK · APPROVAL ALWAYS" tone="warning" />
          </View>
          <View style={s.heading}>
            <AppText variant="display" accessibilityRole="header">
              {title}
            </AppText>
            <AppText variant="label" tone="muted">
              {subtitle}
            </AppText>
          </View>
          {children}
          <AppText variant="caption" tone="muted">
            SWITCHBOARD / A LITTLE HELP WITH THE NEXT CONVERSATION
          </AppText>
        </ScrollView>
      </View>
    </SafeAreaView>
  );
}

export function NewGoalScreen() {
  const router = useRouter();
  const { draft, setDraft } = useGoalDraft();
  const [attempt, setAttempt] = useState(0);
  const [checkFocused, setCheckFocused] = useState(false);
  const shouldFocus = useRef(false);
  const scroll = useRef<ScrollView>(null);
  const result = validateDraft(draft);
  const errors = attempt ? result.errors : {};
  const update = <K extends keyof GoalDraft>(key: K, value: GoalDraft[K]) =>
    setDraft({
      ...draft,
      [key]: value,
      ...(["date", "time", "timezone"].includes(key) ? { offset: "" } : {}),
    });
  useEffect(() => {
    if (attempt && shouldFocus.current) {
      shouldFocus.current = false;
      scroll.current?.scrollTo({ y: 0, animated: false });
      if (Platform.OS === "web")
        document.getElementById("goal-errors")?.focus();
    }
  }, [attempt]);
  function review() {
    shouldFocus.current = !result.review;
    setAttempt(attempt + 1);
    if (result.review) router.push("/review");
  }
  return (
    <GoalShell
      title="What can we take off your list?"
      subtitle="01 / PREPARE A RESERVATION OR APPOINTMENT"
      scrollRef={scroll}
    >
      <AppText tone="muted">
        Plan a fictional call. Nothing is saved or dialed when you review.
      </AppText>
      {attempt > 0 && Object.keys(result.errors).length > 0 && (
        <View
          nativeID="goal-errors"
          tabIndex={-1}
          accessibilityRole="alert"
          style={s.errorSummary}
        >
          <AppText tone="danger">
            Check the highlighted details before reviewing.
          </AppText>
        </View>
      )}
      <View style={s.kindChoices}>
        <Button
          label="Restaurant reservation"
          variant={draft.kind === "restaurant" ? "primary" : "secondary"}
          accessibilityState={{ selected: draft.kind === "restaurant" }}
          onPress={() => update("kind", "restaurant")}
        />
        <Button
          label="Appointment"
          variant={draft.kind === "appointment" ? "primary" : "secondary"}
          accessibilityState={{ selected: draft.kind === "appointment" }}
          onPress={() => update("kind", "appointment")}
        />
      </View>
      <View style={s.columns}>
        <View style={s.column}>
          <RuledPanel title="Who are we calling?">
            <View style={s.fields}>
              <Field
                label="Business name"
                required
                value={draft.business}
                onChangeText={(v) => update("business", v)}
                error={errors.business}
                placeholder="Bayou Lantern Bistro (mock)"
                maxLength={120}
              />
              <Field
                label="Business phone"
                required
                value={draft.destination}
                onChangeText={(v) => update("destination", v)}
                error={errors.destination}
                hint="Fictional demo numbers only: +1, area code, then 555-0100 through 555-0199."
                keyboardType="phone-pad"
                autoCapitalize="none"
                maxLength={16}
              />
            </View>
          </RuledPanel>
          <RuledPanel title="What should we arrange?">
            <View style={s.fields}>
              {draft.kind === "restaurant" ? (
                <Field
                  label="Party size"
                  required
                  value={draft.partySize}
                  onChangeText={(v) => update("partySize", v)}
                  error={errors.partySize}
                  hint="Between 1 and 20 guests."
                  keyboardType="number-pad"
                  maxLength={10}
                />
              ) : (
                <Field
                  label="Service"
                  required
                  value={draft.service}
                  onChangeText={(v) => update("service", v)}
                  error={errors.service}
                  placeholder="Haircut"
                  maxLength={160}
                />
              )}
              <View style={s.dateRow}>
                <Field
                  label="Preferred date"
                  required
                  value={draft.date}
                  onChangeText={(v) => update("date", v)}
                  error={errors.date}
                  hint="YYYY-MM-DD"
                  placeholder="2026-11-20"
                  maxLength={10}
                  containerStyle={s.dateField}
                  autoCapitalize="none"
                />
                <Field
                  label="Preferred time"
                  required
                  value={draft.time}
                  onChangeText={(v) => update("time", v)}
                  hint="24-hour time, HH:MM"
                  placeholder="19:00"
                  maxLength={5}
                  containerStyle={s.timeField}
                  autoCapitalize="none"
                />
              </View>
              <Field
                label="Business time zone"
                required
                value={draft.timezone}
                onChangeText={(v) => update("timezone", v)}
                error={errors.timezone}
                hint="Use the business's time zone, for example America/Chicago."
                autoCapitalize="none"
                autoCorrect={false}
                maxLength={100}
              />
              {result.choices.length > 1 && (
                <View style={s.fields}>
                  <AppText>
                    That time occurs twice when the clocks change. Which
                    occurrence?
                  </AppText>
                  <View style={s.kindChoices}>
                    {result.choices.map((c) => (
                      <Button
                        key={c.offset}
                        label={`UTC ${c.offset}`}
                        variant={
                          draft.offset === c.offset ? "primary" : "secondary"
                        }
                        accessibilityState={{
                          selected: draft.offset === c.offset,
                        }}
                        onPress={() => update("offset", c.offset)}
                      />
                    ))}
                  </View>
                  {errors.offset && (
                    <AppText tone="danger" accessibilityRole="alert">
                      {errors.offset}
                    </AppText>
                  )}
                </View>
              )}
              <Field
                label="Preferences and constraints"
                value={draft.constraints}
                onChangeText={(v) => update("constraints", v)}
                error={errors.constraints}
                hint="Optional. One preference per line, up to 12. Include timing flexibility or seating preferences."
                placeholder={"Indoor seating\nNo later than 8 PM"}
                multiline
                maxLength={2412}
              />
            </View>
          </RuledPanel>
        </View>
        <View style={s.column}>
          <RuledPanel title="Who is the booking for?">
            <View style={s.fields}>
              <Field
                label="Booking name"
                required
                value={draft.contactName}
                onChangeText={(v) => update("contactName", v)}
                error={errors.contactName}
                maxLength={120}
                placeholder="Jordan Lee"
              />
              <Field
                label="Callback phone"
                value={draft.contactPhone}
                onChangeText={(v) => update("contactPhone", v)}
                error={errors.contactPhone}
                hint="Optional. Shared only if you provide it. Include the country code; use fictional details for this demo."
                keyboardType="phone-pad"
                maxLength={16}
              />
            </View>
          </RuledPanel>
          <RuledPanel title="You stay in control">
            <View style={s.fields}>
              <AppText>
                Mock mode uses fictional details. Every suggested response
                requires your approval. Reviewing a goal cannot place a call.
              </AppText>
              {Platform.OS === "web" ? (
                <label
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: 12,
                    minHeight: 48,
                    cursor: "pointer",
                    fontFamily: t.font.body,
                    fontSize: 16,
                    lineHeight: "25px",
                    color: t.ink,
                  }}
                >
                  <input
                    type="checkbox"
                    aria-label="I understand this is a fictional simulation"
                    checked={draft.acknowledged}
                    onChange={(event) =>
                      update("acknowledged", event.target.checked)
                    }
                    onFocus={() => setCheckFocused(true)}
                    onBlur={() => setCheckFocused(false)}
                    style={{
                      width: 48,
                      height: 48,
                      flexShrink: 0,
                      margin: 0,
                      accentColor: t.green,
                      outlineColor: t.ink,
                      outlineWidth: 2,
                      outlineOffset: 3,
                      outlineStyle: checkFocused ? "solid" : "none",
                    }}
                  />
                  <span>I understand this is a fictional simulation.</span>
                </label>
              ) : (
                <Pressable
                  accessibilityRole="checkbox"
                  accessibilityLabel="I understand this is a fictional simulation"
                  accessibilityState={{ checked: draft.acknowledged }}
                  onFocus={() => setCheckFocused(true)}
                  onBlur={() => setCheckFocused(false)}
                  onPress={() => update("acknowledged", !draft.acknowledged)}
                  style={s.check}
                >
                  <AppText style={s.checkMark}>
                    {draft.acknowledged ? "☑" : "☐"}
                  </AppText>
                  <AppText style={s.checkText}>
                    I understand this is a fictional simulation.
                  </AppText>
                </Pressable>
              )}
              {errors.acknowledged && (
                <AppText tone="danger" accessibilityRole="alert">
                  {errors.acknowledged}
                </AppText>
              )}
              <Button label="Review goal" onPress={review} />
            </View>
          </RuledPanel>
        </View>
      </View>
    </GoalShell>
  );
}

export function ReviewGoalScreen() {
  const router = useRouter();
  const { draft, intent, setIntent } = useGoalDraft();
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState("");
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);
  const { review } = validateDraft(draft);
  const fingerprint = review ? JSON.stringify(review) : "";
  const saved = intent?.fingerprint === fingerprint ? intent.call : undefined;
  async function saveDraft() {
    if (!review || controller.current) return;
    if (saved) {
      router.push(`/calls/${saved.id}`);
      return;
    }
    setCreateError("");
    setCreating(true);
    const abort = new AbortController();
    controller.current = abort;
    try {
      const key =
        intent?.fingerprint === fingerprint ? intent.key : randomUUID();
      setIntent({ fingerprint, key });
      const call = await createCall(
        process.env.EXPO_PUBLIC_API_URL || "http://localhost:8080",
        { ...review, consent_acknowledged: true, idempotency_key: key },
        abort.signal,
      );
      if (abort.signal.aborted) return;
      setIntent({ fingerprint, key, call });
      router.push(`/calls/${call.id}`);
    } catch (error) {
      if (!abort.signal.aborted)
        setCreateError(
          error instanceof CallRequestError
            ? error.message
            : "The draft could not be saved. Your goal is still here; try again.",
        );
    } finally {
      controller.current = null;
      if (!abort.signal.aborted) setCreating(false);
    }
  }
  if (!review)
    return (
      <GoalShell title="Let's start with a goal." subtitle="02 / REVIEW">
        <AppText>
          There is no complete goal to review yet. Enter the details first;
          drafts are not restored after a refresh.
        </AppText>
        <Button label="Enter a goal" onPress={() => router.replace("/new")} />
      </GoalShell>
    );
  const goal = review.goal;
  return (
    <GoalShell
      title="A quick check before the conversation."
      subtitle="02 / REVIEW YOUR GOAL"
    >
      <AppText tone="muted">
        {saved
          ? "A draft has been saved. Starting a call is always a separate action."
          : "These are the details your assistant would use. Nothing has been saved or dialed."}
      </AppText>
      <View style={s.columns}>
        <View style={s.column}>
          <RuledPanel title={review.business_name}>
            <View style={s.fields}>
              <Detail label="BUSINESS PHONE" value={review.destination} />
              <Detail
                label="REQUEST"
                value={
                  goal.kind === "restaurant"
                    ? `Restaurant reservation · ${goal.party_size} guests`
                    : `Appointment · ${goal.service}`
                }
              />
              <Detail
                label="BUSINESS LOCAL DATE & TIME"
                value={`${draft.date} at ${draft.time}`}
              />
              <Detail
                label="TIME ZONE"
                value={`${goal.timezone} · UTC ${goal.preferred_at.slice(-6)}`}
              />
              <Detail label="BOOKING NAME" value={goal.contact_name} />
              <Detail
                label="CALLBACK PHONE"
                value={
                  goal.contact_phone ??
                  "Not provided — do not share or invent one."
                }
              />
              <Detail
                label="PREFERENCES & CONSTRAINTS"
                value={
                  goal.constraints.length
                    ? goal.constraints.map((c) => `• ${c}`).join("\n")
                    : "None provided."
                }
              />
            </View>
          </RuledPanel>
        </View>
        <View style={s.column}>
          <RuledPanel title="Ready for your review">
            <View style={s.fields}>
              <Badge label="MOCK MODE" tone="warning" />
              <AppText>Fictional simulation only.</AppText>
              <Badge label="APPROVAL ALWAYS" tone="success" />
              <AppText>You review every suggested reply before speech.</AppText>
              <AppText>
                The assistant introduces itself as AI and calls on behalf of the
                booking name above.
              </AppText>
              <Button
                label={saved ? "Open saved draft" : "Save call draft"}
                loading={creating}
                loadingLabel="Saving draft…"
                onPress={() => {
                  void saveDraft();
                }}
              />
              {createError && (
                <AppText accessibilityRole="alert" tone="danger">
                  {createError}
                </AppText>
              )}
              <Button
                label="Edit goal"
                variant="secondary"
                onPress={() => router.dismissTo("/new")}
              />
              <Button
                label="Return to calls"
                onPress={() => router.push("/")}
              />
              <AppText variant="caption" tone="muted">
                Saving a draft stores these details. It does not dial the
                business or generate speech.
              </AppText>
            </View>
          </RuledPanel>
        </View>
      </View>
    </GoalShell>
  );
}

function Detail({ label, value }: { label: string; value: string }) {
  return (
    <View style={s.detail}>
      <AppText variant="label" tone="muted">
        {label}
      </AppText>
      <AppText>{value}</AppText>
    </View>
  );
}

const s = StyleSheet.create({
  safe: { flex: 1, backgroundColor: t.paper },
  shell: { flex: 1 },
  wide: { flexDirection: "row" },
  nav: { padding: 20, borderBottomWidth: 1, borderColor: t.line, gap: 20 },
  navWide: {
    width: 230,
    borderRightWidth: 1,
    borderBottomWidth: 0,
    paddingTop: 38,
  },
  mark: { fontSize: 42, color: t.green },
  navActions: {
    flexDirection: "row",
    alignItems: "center",
    flexWrap: "wrap",
    gap: 12,
  },
  navFooter: { marginTop: "auto" },
  scroll: { flex: 1 },
  content: { padding: 20, gap: 24 },
  contentWide: {
    padding: 40,
    maxWidth: 1400,
    width: "100%",
    alignSelf: "center",
  },
  top: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    gap: 12,
    flexWrap: "wrap",
  },
  heading: {
    gap: 12,
    paddingBottom: 24,
    borderBottomWidth: 1,
    borderColor: t.green,
  },
  columns: { flexDirection: "row", flexWrap: "wrap", gap: 32 },
  column: { flex: 1, flexBasis: 420, minWidth: 0, gap: 32 },
  fields: { gap: 20 },
  kindChoices: { flexDirection: "row", flexWrap: "wrap", gap: 12 },
  dateRow: { flexDirection: "row", flexWrap: "wrap", gap: 12 },
  dateField: { flex: 1, flexBasis: 170 },
  timeField: { flex: 1, flexBasis: 110 },
  check: {
    minHeight: 48,
    flexDirection: "row",
    alignItems: "center",
    gap: 12,
    paddingVertical: 12,
  },
  checkMark: { fontSize: 26, color: t.green },
  checkText: { flex: 1 },
  focus: {
    outlineColor: t.ink,
    outlineWidth: 2,
    outlineOffset: 3,
    outlineStyle: "solid",
  },
  errorSummary: { padding: 16, borderWidth: 1, borderColor: t.vermilion },
  detail: { gap: 8 },
});
