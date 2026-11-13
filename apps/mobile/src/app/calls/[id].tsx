import { useLocalSearchParams } from "expo-router";
import DraftCallScreen from "../../screens/draft-call";
import LiveCallPreview from "../../screens/live-call";

export default function CallRoute() {
  const { id } = useLocalSearchParams<{ id: string }>();
  return id === "preview" ? <LiveCallPreview /> : <DraftCallScreen />;
}
