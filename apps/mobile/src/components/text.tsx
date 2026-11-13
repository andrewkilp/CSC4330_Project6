import { Text, type TextProps, type TextStyle } from "react-native";
import { theme as t } from "../theme";

export type AppTextProps = TextProps & {
  variant?: keyof typeof t.type;
  tone?: "ink" | "muted" | "green" | "danger";
};

export function AppText({
  variant = "body",
  tone = "ink",
  style,
  ...props
}: AppTextProps) {
  const heading = variant === "display" || variant === "heading";
  const font: TextStyle = {
    fontFamily: heading
      ? t.font.serif
      : variant === "mono"
        ? t.font.mono
        : t.font.body,
    fontWeight: heading ? "700" : variant === "label" ? "600" : "400",
    letterSpacing: variant === "label" ? 1.5 : 0,
    color: tone === "danger" ? t.dangerInk : t[tone],
  };
  return <Text {...props} style={[t.type[variant], font, style]} />;
}
