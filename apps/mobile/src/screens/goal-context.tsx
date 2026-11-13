import type { Call } from "@switchboard/api";
import { createContext, type ReactNode, useContext, useState } from "react";
import { type GoalDraft, initialDraft } from "./goal-draft";

export interface DraftIntent {
  fingerprint: string;
  key: string;
  call?: Call;
}

const GoalContext = createContext<{
  draft: GoalDraft;
  setDraft: (draft: GoalDraft) => void;
  intent: DraftIntent | null;
  setIntent: (intent: DraftIntent) => void;
} | null>(null);

export function GoalProvider({ children }: { children: ReactNode }) {
  const [draft, setDraft] = useState<GoalDraft>({ ...initialDraft });
  const [intent, setIntent] = useState<DraftIntent | null>(null);
  return (
    <GoalContext.Provider value={{ draft, setDraft, intent, setIntent }}>
      {children}
    </GoalContext.Provider>
  );
}

export function useGoalDraft() {
  const value = useContext(GoalContext);
  if (!value) throw new Error("Goal routes require GoalProvider");
  return value;
}
