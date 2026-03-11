import { createContext, useContext } from "react";
import type { SharedSelectionState } from "../contracts/selection-state";

export type SelectionContextValue = {
  selection: SharedSelectionState;
  onSelectionChange: (patch: Partial<SharedSelectionState>) => void;
};

export const SelectionContext = createContext<SelectionContextValue | null>(null);

export function useSelection(): SelectionContextValue {
  const ctx = useContext(SelectionContext);
  if (!ctx) throw new Error("useSelection must be used inside SelectionContext.Provider");
  return ctx;
}
