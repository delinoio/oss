import { WorkspaceMode } from "./workspace-mode";

export type SavedWorkspaceProfile = {
  workspaceId: string;
  mode: WorkspaceMode;
  remoteEndpointUrl?: string;
  lastUsedAt: string;
};

