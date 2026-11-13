// Shared primitives for the MVP screens, following docs/design.md: paper, ink, forest,
// vermilion and brass; thin rules; serif headings; monospaced timestamps; 48 px targets.
// The full component set and gallery belong to #2.
import { usePathname, useRouter } from "expo-router";
import type { ReactNode } from "react";
import {
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  useWindowDimensions,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { theme as t } from "../theme";
import { Badge as PrimitiveBadge, Button as PrimitiveButton } from ".";

export const serif = Platform.OS === "android" ? "serif" : "Georgia";
export const mono = Platform.OS === "ios" ? "Menlo" : "monospace";

export function useWide(): boolean {
  return useWindowDimensions().width >= 1000;
}

// Thin adapters over the #2 primitives, keeping this screen's call sites short.
export function Button({
  label,
  onPress,
  kind = "primary",
  disabled = false,
  hint,
}: {
  label: string;
  onPress: () => void;
  kind?: "primary" | "secondary" | "danger";
  disabled?: boolean;
  hint?: string;
}) {
  return (
    <PrimitiveButton
      label={label}
      onPress={onPress}
      disabled={disabled}
      accessibilityHint={hint}
      variant={kind === "danger" ? "destructive" : kind}
    />
  );
}

export function Badge({
  label,
  tone = "green",
}: {
  label: string;
  tone?: "green" | "vermilion" | "brass" | "muted";
}) {
  const map = {
    green: "success",
    vermilion: "danger",
    brass: "warning",
    muted: "neutral",
  } as const;
  return <PrimitiveBadge label={label} tone={map[tone]} />;
}

const NAV = [
  { href: "/", label: "Calls" },
  { href: "/preview", label: "Design preview" },
] as const;

// Page frame with the Switchboard sidebar (wide) or top bar (narrow).
export function Shell({
  children,
  scroll = true,
}: {
  children: ReactNode;
  scroll?: boolean;
}) {
  const wide = useWide();
  const path = usePathname();
  const router = useRouter();
  const body = scroll ? (
    <ScrollView
      style={{ flex: 1 }}
      contentContainerStyle={ui.main}
      keyboardShouldPersistTaps="handled"
    >
      {children}
    </ScrollView>
  ) : (
    <View style={[{ flex: 1 }, ui.main]}>{children}</View>
  );
  return (
    <SafeAreaView style={ui.safe}>
      <View style={[{ flex: 1 }, wide && { flexDirection: "row" }]}>
        <View style={[ui.nav, wide && ui.navWide]}>
          <View>
            <Text style={ui.brand}>Switchboard</Text>
            <Text style={ui.eyebrow}>AI PHONE CONCIERGE</Text>
            <Text style={ui.eyebrow}>RESERVATIONS & APPOINTMENTS</Text>
          </View>
          <View style={[ui.navLinks, wide && { flexDirection: "column" }]}>
            {NAV.map((item) => {
              const active =
                item.href === "/"
                  ? path === "/" || path.startsWith("/calls")
                  : path === item.href;
              return (
                <Pressable
                  key={item.href}
                  accessibilityRole="link"
                  accessibilityState={{ selected: active }}
                  onPress={() => router.push(item.href)}
                  style={[ui.navItem, active && { backgroundColor: t.green }]}
                >
                  <Text style={[ui.navLabel, active && { color: t.white }]}>
                    {item.label}
                  </Text>
                </Pressable>
              );
            })}
          </View>
          {wide && (
            <Text style={ui.navFooter}>
              CSC 4330 / PROJECT 06{"\n"}MVP build. Mock calls use fictional
              businesses and scripted replies.
            </Text>
          )}
        </View>
        {body}
      </View>
    </SafeAreaView>
  );
}

export const ui = StyleSheet.create({
  safe: { flex: 1, backgroundColor: t.paper },
  nav: { padding: 20, borderBottomWidth: 1, borderColor: t.line, gap: 18 },
  navWide: {
    width: 230,
    borderRightWidth: 1,
    borderBottomWidth: 0,
    paddingTop: 36,
  },
  brand: { fontFamily: serif, fontSize: 30, fontWeight: "700", color: t.ink },
  eyebrow: { fontSize: 10, letterSpacing: 2, color: t.muted, lineHeight: 18 },
  navLinks: { flexDirection: "row", gap: 8, flexWrap: "wrap" },
  navItem: {
    paddingHorizontal: 16,
    minHeight: 48,
    justifyContent: "center",
    borderRadius: 5,
  },
  navLabel: { fontSize: 16, color: t.ink },
  navFooter: {
    marginTop: "auto",
    fontSize: 11,
    color: t.muted,
    lineHeight: 19,
  },
  main: { padding: 24, gap: 18, flexGrow: 1 },
  heading: {
    fontFamily: serif,
    fontWeight: "700",
    fontSize: 36,
    lineHeight: 43,
    color: t.ink,
  },
  subheading: {
    fontFamily: serif,
    fontWeight: "700",
    fontSize: 23,
    color: t.ink,
  },
  label: { fontSize: 10, letterSpacing: 2, color: t.muted },
  body: { fontSize: 16, lineHeight: 24, color: t.ink },
  help: { fontSize: 13, lineHeight: 20, color: t.muted },
  rule: { height: 1, backgroundColor: t.line },
  panel: {
    borderWidth: 1,
    borderColor: t.line,
    borderRadius: 5,
    padding: 16,
    gap: 10,
  },
  input: {
    borderWidth: 1,
    borderColor: t.green,
    borderRadius: 5,
    padding: 14,
    fontSize: 16,
    color: t.ink,
    minHeight: 52,
    backgroundColor: t.white,
    textAlignVertical: "top",
  },
  row: {
    flexDirection: "row",
    flexWrap: "wrap",
    gap: 12,
    alignItems: "center",
  },
  error: { color: t.vermilion, fontSize: 14, lineHeight: 20 },
});
