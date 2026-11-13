import { useId, useState } from "react";
import {
  Platform,
  type StyleProp,
  StyleSheet,
  TextInput,
  type TextInputProps,
  type TextStyle,
  View,
  type ViewStyle,
} from "react-native";
import { theme as t } from "../theme";
import { AppText } from "./text";

export type FieldProps = Omit<TextInputProps, "style"> & {
  label: string;
  hint?: string;
  error?: string;
  required?: boolean;
  containerStyle?: StyleProp<ViewStyle>;
  inputStyle?: StyleProp<TextStyle>;
};

export function Field({
  label,
  hint,
  error,
  required = false,
  containerStyle,
  inputStyle,
  onFocus,
  onBlur,
  editable = true,
  multiline = false,
  ...props
}: FieldProps) {
  const generatedId = useId();
  const labelId = `${generatedId}-label`;
  const hintId = `${generatedId}-hint`;
  const errorId = `${generatedId}-error`;
  const webAccessibility: {
    "aria-describedby"?: string;
    "aria-invalid"?: boolean;
    "aria-required"?: boolean;
  } =
    Platform.OS === "web"
      ? {
          "aria-describedby":
            [hint ? hintId : undefined, error ? errorId : undefined]
              .filter(Boolean)
              .join(" ") || undefined,
          "aria-invalid": !!error,
          "aria-required": required,
        }
      : {};
  const [focused, setFocused] = useState(false);
  return (
    <View style={[s.container, containerStyle]}>
      <View style={s.labelRow}>
        <AppText nativeID={labelId} variant="label">
          {label}
        </AppText>
        {required && (
          <AppText
            variant="label"
            tone="muted"
            aria-hidden
            importantForAccessibility="no"
          >
            REQUIRED
          </AppText>
        )}
      </View>
      <TextInput
        {...props}
        {...webAccessibility}
        accessibilityLabel={props.accessibilityLabel ?? label}
        accessibilityLabelledBy={labelId}
        accessibilityHint={
          [
            required ? "Required" : undefined,
            hint,
            error ? `Error: ${error}` : undefined,
          ]
            .filter(Boolean)
            .join(". ") || undefined
        }
        accessibilityState={{
          ...props.accessibilityState,
          disabled: !editable,
        }}
        editable={editable}
        multiline={multiline}
        placeholderTextColor={t.muted}
        onFocus={(event) => {
          setFocused(true);
          onFocus?.(event);
        }}
        onBlur={(event) => {
          setFocused(false);
          onBlur?.(event);
        }}
        style={[
          s.input,
          multiline && s.multiline,
          !editable && s.disabled,
          !!error && s.invalid,
          focused && Platform.OS === "web" && s.focus,
          inputStyle,
        ]}
      />
      {!!hint && (
        <AppText nativeID={hintId} variant="caption" tone="muted">
          {hint}
        </AppText>
      )}
      {!!error && (
        <AppText
          nativeID={errorId}
          accessibilityRole="alert"
          accessibilityLiveRegion="polite"
          variant="caption"
          tone="danger"
        >
          {error}
        </AppText>
      )}
    </View>
  );
}

const s = StyleSheet.create({
  container: { gap: t.space.sm },
  labelRow: { flexDirection: "row", flexWrap: "wrap", gap: t.space.sm },
  input: {
    minHeight: t.target,
    borderWidth: 1,
    borderColor: t.green,
    borderRadius: t.radius,
    color: t.ink,
    backgroundColor: t.paper,
    fontFamily: t.font.body,
    fontSize: t.type.body.fontSize,
    lineHeight: t.type.body.lineHeight,
    paddingHorizontal: t.space.lg,
    paddingVertical: t.space.md,
  },
  multiline: { minHeight: t.target * 2, textAlignVertical: "top" },
  disabled: { backgroundColor: t.wash, color: t.muted, borderColor: t.line },
  invalid: { borderColor: t.vermilion },
  focus: {
    outlineColor: t.ink,
    outlineWidth: 2,
    outlineOffset: 3,
    outlineStyle: "solid",
  },
});
