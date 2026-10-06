import { useLocale } from "./localization";
import { createContext, useContext, useState, type ReactNode } from "react";
import { PullRequestProblemCollectionKind } from "@delinoio/delidev-api-client";

export interface PRSelectionIdentity {
  repositoryId: string;
  remoteRepositoryId: string;
  pullRequestId: string;
  number: string;
}

export interface PRAllowanceConfirmation {
  key: string;
  selection: PRSelectionIdentity;
  id: string;
  revision: bigint;
}

interface PRWorkflowState {
  confirmations: ReadonlyMap<string, PRAllowanceConfirmation>;
  collectionKinds: ReadonlyMap<string, PullRequestProblemCollectionKind>;
}

interface PRWorkflowValue extends PRWorkflowState {
  confirmAllowance: (value: PRAllowanceConfirmation) => void;
  cancelAllowance: (key: string) => void;
  setCollectionKind: (key: string, kind: PullRequestProblemCollectionKind) => void;
}

const Context = createContext<PRWorkflowValue | undefined>(undefined);
export const prSelectionKey = (selection: PRSelectionIdentity) => `${selection.remoteRepositoryId}:${selection.pullRequestId}`;
export const prAllowanceKey = (selection: PRSelectionIdentity) => `pr-remediation-confirm:${prSelectionKey(selection)}`;

export function PRWorkflowProvider({ children }: { children: ReactNode }) {
  useLocale();
  const [state, setState] = useState<PRWorkflowState>(() => ({ confirmations: new Map(), collectionKinds: new Map() }));
  const value: PRWorkflowValue = {
    ...state,
    confirmAllowance: (confirmation) => setState((current) => {
      const confirmations = new Map(current.confirmations);
      confirmations.delete(confirmation.key);
      confirmations.set(confirmation.key, confirmation);
      while (confirmations.size > 100) confirmations.delete(confirmations.keys().next().value!);
      return { ...current, confirmations };
    }),
    cancelAllowance: (key) => setState((current) => {
      if (!current.confirmations.has(key)) return current;
      const confirmations = new Map(current.confirmations);
      confirmations.delete(key);
      return { ...current, confirmations };
    }),
    setCollectionKind: (key, kind) => setState((current) => {
      const collectionKinds = new Map(current.collectionKinds);
      collectionKinds.delete(key);
      collectionKinds.set(key, kind);
      while (collectionKinds.size > 100) collectionKinds.delete(collectionKinds.keys().next().value!);
      return { ...current, collectionKinds };
    }),
  };
  return <Context.Provider value={value}>{children}</Context.Provider>;
}

export function usePRWorkflow() {
  const value = useContext(Context);
  if (!value) throw new Error("The connection-scoped PR workflow provider is required.");
  return value;
}
