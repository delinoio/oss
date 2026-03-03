import { WorkspaceMode } from "./workspace-mode";

export enum WorkspaceEndpointSource {
  ManagedLoopback = "MANAGED_LOOPBACK",
  LocalOverride = "LOCAL_OVERRIDE",
  UserRemote = "USER_REMOTE",
}

export type ResolvedWorkspaceConnection = {
  mode: WorkspaceMode;
  endpointUrl: string;
  endpointSource: WorkspaceEndpointSource;
  token?: string;
  transport: "CONNECT_RPC";
};

export type ResolveWorkspaceConnectionInput = {
  mode: WorkspaceMode;
  remoteEndpointUrl?: string;
  remoteToken?: string;
};
