import { describe, expect, it, vi } from "vitest";
import { WorkspaceEndpointSource } from "../contracts/workspace-connection";
import {
  createStubLocalRuntimeProvider,
  redactEndpointUrlForLogs,
} from "./local-runtime-provider";

describe("redactEndpointUrlForLogs", () => {
  it("removes credentials, query, and fragment", () => {
    const redacted = redactEndpointUrlForLogs(
      "https://user:pass@dexdex.example/rpc?token=abc#frag",
    );

    expect(redacted).toBe("https://dexdex.example/rpc");
  });

  it("returns marker for invalid URLs", () => {
    const redacted = redactEndpointUrlForLogs("not-a-url");
    expect(redacted).toBe("[invalid-endpoint-url]");
  });
});

describe("createStubLocalRuntimeProvider", () => {
  it("logs redacted endpoint URL while returning normalized endpoint", async () => {
    const logger = {
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    };

    const provider = createStubLocalRuntimeProvider({
      defaultEndpointUrl: "https://user:pass@dexdex.example/rpc?token=abc#frag",
      logger,
    });

    const endpoint = await provider.resolveLocalWorkspaceEndpoint();

    expect(endpoint).toEqual({
      endpointUrl: "https://user:pass@dexdex.example/rpc?token=abc#frag",
      endpointSource: WorkspaceEndpointSource.ManagedLoopback,
      token: undefined,
    });

    expect(logger.info).toHaveBeenCalledWith("local_runtime.resolve.stub", {
      endpoint_source: WorkspaceEndpointSource.ManagedLoopback,
      endpoint_url: "https://dexdex.example/rpc",
    });
  });

  it("supports explicit LOCAL override source in stub mode", async () => {
    const logger = {
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    };

    const provider = createStubLocalRuntimeProvider({
      defaultEndpointUrl: "https://dexdex.example/rpc",
      defaultEndpointSource: WorkspaceEndpointSource.LocalOverride,
      logger,
    });

    const endpoint = await provider.resolveLocalWorkspaceEndpoint();

    expect(endpoint.endpointSource).toBe(WorkspaceEndpointSource.LocalOverride);
  });
});
