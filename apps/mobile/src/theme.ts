import { Platform } from "react-native";

export const theme = {
  paper: "#F5F0E6",
  ink: "#202923",
  green: "#234D3C",
  vermilion: "#C65036",
  dangerInk: "#9A3D2A",
  brass: "#94733A",
  muted: "#666E64",
  white: "#FFFFFF",
  wash: "#EAEBDD",
  line: "#B8BDAE",
  font: {
    serif: Platform.OS === "android" ? "serif" : "Georgia",
    body: Platform.OS === "ios" ? "System" : "sans-serif",
    mono: Platform.OS === "ios" ? "Menlo" : "monospace",
  },
  type: {
    display: { fontSize: 36, lineHeight: 44 },
    heading: { fontSize: 25, lineHeight: 33 },
    body: { fontSize: 16, lineHeight: 25 },
    label: { fontSize: 11, lineHeight: 18 },
    caption: { fontSize: 13, lineHeight: 20 },
    mono: { fontSize: 12, lineHeight: 20 },
  },
  space: { xs: 4, sm: 8, md: 12, lg: 16, xl: 24, xxl: 32, section: 48 },
  layout: {
    breakpoint: 1000,
    maxWidth: 1200,
    narrow: { pagePadding: 20, sectionGap: 24 },
    wide: { pagePadding: 40, sectionGap: 32 },
  },
  radius: 5,
  target: 48,
} as const;
