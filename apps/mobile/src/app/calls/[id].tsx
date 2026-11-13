import { useLocalSearchParams } from "expo-router";
import DraftCallScreen from "../../screens/draft-call";
import LiveCallPreview from "../../screens/live-call";
import ConnectedCall from "../../screens/live-call/connected";

export default function CallRoute() {
  const { id, live } = useLocalSearchParams<{ id: string; live?: string }>();
  return id === "preview" ? (
    <LiveCallPreview />
  ) : live === "1" ? (
    <ConnectedCall id={id ?? ""} />
  ) : (
    <DraftCallScreen />
  );
}
