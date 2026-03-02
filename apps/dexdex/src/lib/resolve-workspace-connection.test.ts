import { describe, expect, it, vi } from "vitest";
import {
  WorkspaceEndpointSource,
  type ResolvedWorkspaceConnection,
} from "../contracts/workspace-connection";
import { WorkspaceMode } from "../contracts/workspace-mode";
import { resolveWorkspaceConnection } from "./resolve-workspace-connection";

function createNoopLogger() {
  return {
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  };
}

describe("resolveWorkspaceConnection", () => {
  it("resolves LOCAL mode through the local runtime provider", async () => {
    const localRuntimeProvider = {
      resolveLocalWorkspaceEndpoint: vi.fn().mockResolvedValue({
        endpointUrl: "http://127.0.0.1:7878",
        token: "local-token",
        endpointSource: WorkspaceEndpointSource.ManagedLoopback,
      }),
    };

    const connection = await resolveWorkspaceConnection(
      { mode: WorkspaceMode.Local },
      {
        localRuntimeProvider,
        logger: createNoopLogger(),
      },
    );

    expect(localRuntimeProvider.resolveLocalWorkspaceEndpoint).toHaveBeenCalledTimes(
      1,
    );
    expect(connection).toEqual<ResolvedWorkspaceConnection>({
      mode: WorkspaceMode.Local,
      endpointUrl: "http://127.0.0.1:7878/",
      endpointSource: WorkspaceEndpointSource.ManagedLoopback,
      token: "local-token",
      transport: "CONNECT_RPC",
    });
  });

  it("resolves REMOTE mode with normalized endpoint contract", async () => {
    const connection = await resolveWorkspaceConnection(
      {
        mode: WorkspaceMode.Remote,
        remoteEndpointUrl: "https://dexdex.example/rpc",
        remoteToken: " remote-token ",
      },
      { logger: createNoopLogger() },
    );

    expect(connection).toEqual<ResolvedWorkspaceConnection>({
      mode: WorkspaceMode.Remote,
      endpointUrl: "https://dexdex.example/rpc",
      endpointSource: WorkspaceEndpointSource.UserRemote,
      token: "remote-token",
      transport: "CONNECT_RPC",
    });
  });

  it("keeps LOCAL and REMOTE normalized payload shape identical", async () => {
    const localRuntimeProvider = {
      resolveLocalWorkspaceEndpoint: vi.fn().mockResolvedValue({
        endpointUrl: "http://127.0.0.1:7878",
        endpointSource: WorkspaceEndpointSource.ManagedLoopback,
      }),
    };

    const localConnection = await resolveWorkspaceConnection(
      { mode: WorkspaceMode.Local },
      {
        localRuntimeProvider,
        logger: createNoopLogger(),
      },
    );

    const remoteConnection = await resolveWorkspaceConnection(
      {
        mode: WorkspaceMode.Remote,
        remoteEndpointUrl: "http://127.0.0.1:7878",
      },
      { logger: createNoopLogger() },
    );

    expect(Object.keys(localConnection).sort()).toEqual(
      Object.keys(remoteConnection).sort(),
    );
    expect(localConnection.transport).toBe(remoteConnection.transport);
  });

  it("rejects REMOTE mode when endpoint URL is missing", async () => {
    await expect(
      resolveWorkspaceConnection(
        {
          mode: WorkspaceMode.Remote,
          remoteEndpointUrl: "",
        },
        { logger: createNoopLogger() },
      ),
    ).rejects.toThrow("remoteEndpointUrl must not be empty.");
  });
});
