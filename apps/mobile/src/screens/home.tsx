// Home (H1) with the MVP's new-call flow (N1/N2): pick a mock scenario, review the
// goal and disclosure, then create a draft call. Creating never dials; the live call
// screen has the explicit "Place mock call" action.
import {
  ApiError,
  type Call,
  createCall,
  getProviders,
  listMockScenarios,
  type MockScenario,
  newIdempotencyKey,
  type Providers,
  realCallBlockers,
  startSession,
} from "@switchboard/api";
import { useRouter } from "expo-router";
import { useEffect, useRef, useState } from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { Badge, Button, Shell, serif, ui, useWide } from "../components/ui";
import { apiBase, describeGoal } from "../config";
import { theme as t } from "../theme";

export default function Home() {
  const router = useRouter();
  const wide = useWide();
  const [scenarios, setScenarios] = useState<readonly MockScenario[]>([]);
  const [providers, setProviders] = useState<Providers | null>(null);
  const [selected, setSelected] = useState<MockScenario | null>(null);
  const [consent, setConsent] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  // One key per reviewed request, reused if the operator retries after an error.
  const key = useRef(newIdempotencyKey());

  useEffect(() => {
    const abort = new AbortController();
    listMockScenarios(apiBase(), abort.signal)
      .then(setScenarios)
      .catch(() =>
        setError(
          "The call server is unavailable. Start it with DATABASE_URL set (see docs/demo.md).",
        ),
      );
    getProviders(apiBase(), abort.signal)
      .then(setProviders)
      .catch(() => {});
    return () => abort.abort();
  }, []);

  async function create() {
    if (!selected) return;
    setBusy(true);
    setError("");
    const request = {
      mode: "mock" as const,
      destination: selected.destination,
      business_name: selected.business_name,
      goal: selected.goal,
      approval_mode: selected.approval_mode,
      consent_acknowledged: true as const,
      idempotency_key: key.current,
    };
    try {
      let call: Call;
      try {
        call = await createCall(apiBase(), request);
      } catch (e) {
        if (!(e instanceof ApiError) || e.status !== 401) throw e;
        await startSession(apiBase());
        call = await createCall(apiBase(), request);
      }
      key.current = newIdempotencyKey();
      router.push(`/calls/${call.id}`);
    } catch (e) {
      setError(
        e instanceof Error ? e.message : "The call could not be created.",
      );
    } finally {
      setBusy(false);
    }
  }

  const blockers = providers ? realCallBlockers(providers) : [];
  return (
    <Shell>
      <Text style={ui.label}>NEW CALL</Text>
      <Text accessibilityRole="header" style={ui.heading}>
        What can we take off your list?
      </Text>
      <Text style={ui.help}>
        Choose a call. Switchboard drafts every reply and waits for your
        approval before anything is said.
      </Text>
      {!!error && (
        <Text accessibilityLiveRegion="polite" style={ui.error}>
          {error}
        </Text>
      )}
      <View style={[s.columns, wide && { flexDirection: "row" }]}>
        <View style={{ flex: 1, gap: 14 }}>
          <Text style={ui.subheading}>Mock scenarios</Text>
          {scenarios.map((sc) => {
            const active = selected?.name === sc.name;
            return (
              <Pressable
                key={sc.name}
                accessibilityRole="radio"
                accessibilityState={{ checked: active }}
                accessibilityLabel={`${sc.business_name}: ${describeGoal(sc.goal)}`}
                onPress={() => {
                  setSelected(sc);
                  setConsent(false);
                  key.current = newIdempotencyKey();
                }}
                style={[ui.panel, active && s.selected]}
              >
                <View style={ui.row}>
                  <Badge
                    label={
                      sc.goal.kind === "restaurant"
                        ? "RESTAURANT"
                        : "APPOINTMENT"
                    }
                    tone={sc.goal.kind === "restaurant" ? "vermilion" : "brass"}
                  />
                  <Badge label="MOCK" tone="muted" />
                </View>
                <Text style={s.business}>{sc.business_name}</Text>
                <Text style={ui.body}>{describeGoal(sc.goal)}</Text>
                <Text style={ui.help}>
                  For {sc.goal.contact_name}
                  {sc.goal.constraints.length
                    ? ` · ${sc.goal.constraints.join(" · ")}`
                    : ""}
                </Text>
              </Pressable>
            );
          })}
        </View>
        <View style={[{ gap: 14 }, wide ? { width: 380 } : null]}>
          <Text style={ui.subheading}>Review</Text>
          {selected ? (
            <View style={[ui.panel, { gap: 12 }]}>
              <Row label="Business" value={selected.business_name} />
              <Row
                label="Number"
                value={`${selected.destination} (fictional)`}
              />
              <Row label="Goal" value={describeGoal(selected.goal)} />
              <Row
                label="Approval"
                value={
                  selected.approval_mode === "always"
                    ? "Every reply"
                    : "Uncertain replies; you can approve all"
                }
              />
              <Row
                label="Mode"
                value="Mock: scripted business, no phone call"
              />
              <Pressable
                accessibilityRole="checkbox"
                accessibilityState={{ checked: consent }}
                onPress={() => setConsent(!consent)}
                style={s.consent}
              >
                <Text style={s.box}>{consent ? "✓" : ""}</Text>
                <Text style={[ui.body, { flex: 1, fontSize: 14 }]}>
                  The assistant introduces itself as an AI calling on{" "}
                  {selected.goal.contact_name}'s behalf. This business is
                  fictional.
                </Text>
              </Pressable>
              <Button
                label={busy ? "Creating…" : "Create call"}
                disabled={!consent || busy}
                onPress={() => void create()}
              />
              <Text style={ui.help}>
                Creating saves a draft. You place the call on the next screen.
              </Text>
            </View>
          ) : (
            <Text style={ui.help}>Select a scenario to review it.</Text>
          )}
          <View style={[ui.panel, { gap: 6 }]}>
            <Text style={ui.label}>REAL CALLING</Text>
            {providers?.mode === "real" ? (
              <Text style={ui.body}>Available.</Text>
            ) : (
              <>
                <Text style={ui.body}>Unavailable on this server.</Text>
                {blockers.map((b) => (
                  <Text key={b.provider} style={ui.help}>
                    · {b.reason}
                  </Text>
                ))}
              </>
            )}
          </View>
        </View>
      </View>
    </Shell>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <View style={{ gap: 2 }}>
      <Text style={ui.label}>{label.toUpperCase()}</Text>
      <Text style={ui.body}>{value}</Text>
    </View>
  );
}

const s = StyleSheet.create({
  columns: { gap: 28 },
  selected: { borderColor: t.green, borderWidth: 2, backgroundColor: t.wash },
  business: {
    fontFamily: serif,
    fontSize: 22,
    fontWeight: "700",
    color: t.ink,
  },
  consent: {
    flexDirection: "row",
    gap: 12,
    alignItems: "flex-start",
    minHeight: 48,
  },
  box: {
    width: 24,
    height: 24,
    borderWidth: 1,
    borderColor: t.green,
    borderRadius: 4,
    textAlign: "center",
    color: t.green,
    fontWeight: "700",
    lineHeight: 22,
  },
});
