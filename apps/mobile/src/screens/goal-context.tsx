import { createContext, type ReactNode, useContext, useState } from "react";
import { type GoalDraft, initialDraft } from "./goal-draft";

const GoalContext = createContext<{
  draft: GoalDraft;
  setDraft: (draft: GoalDraft) => void;
} | null>(null);

export function GoalProvider({ children }: { children: ReactNode }) {
  const [draft, setDraft] = useState<GoalDraft>({ ...initialDraft });
  return (
    <GoalContext.Provider value={{ draft, setDraft }}>
      {children}
    </GoalContext.Provider>
  );
}

export function useGoalDraft() {
  const value = useContext(GoalContext);
  if (!value) throw new Error("Goal routes require GoalProvider");
  return value;
}
