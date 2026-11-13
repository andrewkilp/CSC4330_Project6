import type { ReactNode } from "react";
import { StyleSheet, View, type ViewProps } from "react-native";
import { theme as t } from "../theme";
import { AppText } from "./text";

export type RuledPanelProps = ViewProps & {
  title?: string;
  accessory?: ReactNode;
  children: ReactNode;
};

export function RuledPanel({
  title,
  accessory,
  children,
  style,
  ...props
}: RuledPanelProps) {
  return (
    <View {...props} style={[s.panel, style]}>
      {(title || accessory) && (
        <View style={s.header}>
          {title ? (
            <AppText
              accessibilityRole="header"
              variant="heading"
              style={s.title}
            >
              {title}
            </AppText>
          ) : null}
          {accessory}
        </View>
      )}
      {children}
    </View>
  );
}

const s = StyleSheet.create({
  panel: {
    borderTopWidth: 1,
    borderBottomWidth: 1,
    borderColor: t.line,
    paddingVertical: t.space.xl,
    gap: t.space.lg,
  },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    flexWrap: "wrap",
    gap: t.space.md,
  },
  title: { flexShrink: 1 },
});
