import { Platform } from "react-native";

// The API base URL. On web the app is normally served by the API itself (WEB_DIR),
// so the page origin is the API. EXPO_PUBLIC_API_URL overrides it for Expo dev
// servers, emulators and devices; it is a public URL, never a secret.
export function apiBase(): string {
  const configured = process.env.EXPO_PUBLIC_API_URL;
  if (configured) return configured;
  if (Platform.OS === "web" && typeof window !== "undefined")
    return window.location.origin;
  return "http://localhost:8080";
}

export function describeGoal(goal: {
  kind: string;
  preferred_at: string;
  timezone: string;
  party_size?: number;
  service?: string;
}): string {
  const when = new Date(goal.preferred_at);
  const day = when.toLocaleDateString("en-US", {
    weekday: "long",
    month: "short",
    day: "numeric",
    timeZone: goal.timezone,
  });
  const time = when.toLocaleTimeString("en-US", {
    hour: "numeric",
    minute: "2-digit",
    timeZone: goal.timezone,
  });
  return goal.kind === "restaurant"
    ? `Table for ${goal.party_size}, ${day} at ${time}`
    : `${goal.service}, ${day} at ${time}`;
}
