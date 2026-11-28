import { useFocusEffect } from "expo-router";
import { useCallback } from "react";
import { Platform } from "react-native";
import { ReviewGoalScreen } from "../screens/goal-entry";

export default function ReviewRoute() {
  useFocusEffect(
    useCallback(() => {
      if (Platform.OS !== "web") return;

      const previousTitle = document.title;
      const reviewTitle = "Review goal | Switchboard";
      document.title = reviewTitle;
      return () => {
        if (document.title === reviewTitle) document.title = previousTitle;
      };
    }, []),
  );

  return <ReviewGoalScreen />;
}
