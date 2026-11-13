import { Stack } from "expo-router";
import { StatusBar } from "expo-status-bar";
import { GoalProvider } from "../screens/goal-context";
import { theme } from "../theme";
export default function RootLayout() {
  return (
    <GoalProvider>
      <StatusBar style="dark" />
      <Stack
        screenOptions={{
          headerShown: false,
          contentStyle: { backgroundColor: theme.paper },
        }}
      />
    </GoalProvider>
  );
}
