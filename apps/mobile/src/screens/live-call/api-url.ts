// The API this build talks to. Expo inlines the variable when it bundles.
export const API_URL =
  process.env.EXPO_PUBLIC_API_URL || "http://localhost:8080";
