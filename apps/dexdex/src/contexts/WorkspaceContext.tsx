import { createContext, useContext } from "react";
import type { ResolvedWorkspaceConnection } from "../contracts/workspace-connection";

export type WorkspaceContextValue = {
  workspaceId: string;
  visualMode: boolean;
  connection: ResolvedWorkspaceConnection;
};

export const WorkspaceContext = createContext<WorkspaceContextValue | null>(null);

export function useWorkspace(): WorkspaceContextValue {
  const ctx = useContext(WorkspaceContext);
  if (!ctx) throw new Error("useWorkspace must be used inside WorkspaceContext.Provider");
  return ctx;
}
