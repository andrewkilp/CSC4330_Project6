import { StyleSheet, View } from "react-native";
import { theme as t } from "../theme";
import { AppText } from "./text";

export type BadgeProps = {
  label: string;
  tone?: "neutral" | "success" | "warning" | "danger";
};

export function Badge({ label, tone = "neutral" }: BadgeProps) {
  return (
    <View
      style={[
        s.badge,
        tone === "success" && s.success,
        tone === "warning" && s.warning,
        tone === "danger" && s.danger,
      ]}
    >
      <AppText
        variant="label"
        style={[s.label, (tone === "success" || tone === "danger") && s.light]}
      >
        {label}
      </AppText>
    </View>
  );
}

const s = StyleSheet.create({
  badge: {
    alignSelf: "flex-start",
    backgroundColor: t.wash,
    borderWidth: 1,
    borderColor: t.line,
    borderRadius: t.radius,
    paddingHorizontal: t.space.sm,
    paddingVertical: t.space.xs,
  },
  success: { backgroundColor: t.green, borderColor: t.green },
  danger: { backgroundColor: t.vermilion, borderColor: t.vermilion },
  warning: { backgroundColor: t.paper, borderColor: t.brass },
  label: { color: t.ink, letterSpacing: 1 },
  light: { color: t.white },
});
