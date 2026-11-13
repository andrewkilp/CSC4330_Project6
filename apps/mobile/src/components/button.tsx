import { useState } from "react";
import {
  Platform,
  Pressable,
  type PressableProps,
  type StyleProp,
  StyleSheet,
  type ViewStyle,
} from "react-native";
import { theme as t } from "../theme";
import { AppText } from "./text";

export type ButtonProps = Omit<PressableProps, "children" | "style"> & {
  label: string;
  variant?: "primary" | "secondary" | "destructive";
  loading?: boolean;
  loadingLabel?: string;
  style?: StyleProp<ViewStyle>;
};

export function Button({
  label,
  variant = "primary",
  loading = false,
  loadingLabel = "Working…",
  disabled = false,
  style,
  onFocus,
  onBlur,
  ...props
}: ButtonProps) {
  const [focused, setFocused] = useState(false);
  const inactive = disabled || loading;
  return (
    <Pressable
      {...props}
      accessibilityRole="button"
      accessibilityLabel={props.accessibilityLabel ?? label}
      accessibilityState={{
        ...props.accessibilityState,
        disabled: inactive,
        busy: loading,
      }}
      aria-busy={loading}
      aria-disabled={inactive}
      disabled={inactive}
      onFocus={(event) => {
        setFocused(true);
        onFocus?.(event);
      }}
      onBlur={(event) => {
        setFocused(false);
        onBlur?.(event);
      }}
      style={({ pressed }) => [
        s.button,
        variant === "secondary" && s.secondary,
        variant === "destructive" && s.destructive,
        inactive && s.disabled,
        pressed && !inactive && s.pressed,
        focused && Platform.OS === "web" && s.focus,
        style,
      ]}
    >
      <AppText
        variant="caption"
        style={[
          s.label,
          variant === "secondary" && s.secondaryLabel,
          inactive && s.disabledLabel,
        ]}
      >
        {loading ? loadingLabel : label}
      </AppText>
    </Pressable>
  );
}

const s = StyleSheet.create({
  button: {
    minHeight: t.target,
    minWidth: t.target,
    justifyContent: "center",
    alignItems: "center",
    borderRadius: t.radius,
    borderWidth: 1,
    borderColor: t.green,
    backgroundColor: t.green,
    paddingVertical: t.space.md,
    paddingHorizontal: t.space.lg,
  },
  secondary: { backgroundColor: t.paper },
  destructive: { backgroundColor: t.vermilion, borderColor: t.vermilion },
  label: { color: t.white, fontWeight: "600", textAlign: "center" },
  secondaryLabel: { color: t.ink },
  disabled: { backgroundColor: t.wash, borderColor: t.line },
  disabledLabel: { color: t.muted },
  pressed: { opacity: 0.85 },
  focus: {
    outlineColor: t.ink,
    outlineWidth: 2,
    outlineOffset: 3,
    outlineStyle: "solid",
  },
});
