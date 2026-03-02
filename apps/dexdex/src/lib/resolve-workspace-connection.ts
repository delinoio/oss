import {
  type ResolveWorkspaceConnectionInput,
  type ResolvedWorkspaceConnection,
  WorkspaceEndpointSource,
} from "../contracts/workspace-connection";
import { WorkspaceMode } from "../contracts/workspace-mode";
import {
  createTauriLocalRuntimeProvider,
  type LocalRuntimeProvider,
} from "./local-runtime-provider";
import { defaultLogger, type DexDexLogger } from "./logger";

export type ResolveWorkspaceConnectionOptions = {
  localRuntimeProvider?: LocalRuntimeProvider;
  logger?: DexDexLogger;
};

export type ResolveWorkspaceConnection = (
  input: ResolveWorkspaceConnectionInput,
  options?: ResolveWorkspaceConnectionOptions,
) => Promise<ResolvedWorkspaceConnection>;

function normalizeEndpointUrl(
  endpointUrl: string,
  fieldName: "remoteEndpointUrl" | "endpointUrl",
): string {
  const trimmedUrl = endpointUrl.trim();
  if (trimmedUrl.length === 0) {
    throw new Error(`${fieldName} must not be empty.`);
  }

  try {
    const parsedUrl = new URL(trimmedUrl);
    if (parsedUrl.protocol !== "http:" && parsedUrl.protocol !== "https:") {
      throw new Error(`${fieldName} must use http or https scheme.`);
    }

    return parsedUrl.toString();
  } catch (error) {
    if (error instanceof Error && error.message.includes("must use http or https scheme.")) {
      throw error;
    }

    throw new Error(`${fieldName} must be a valid absolute URL.`);
  }
}

function normalizeOptionalToken(token?: string): string | undefined {
  if (typeof token !== "string") {
    return undefined;
  }

  const trimmedToken = token.trim();
  return trimmedToken.length > 0 ? trimmedToken : undefined;
}

function buildResolvedConnection(params: {
  mode: WorkspaceMode;
  endpointUrl: string;
  endpointSource: WorkspaceEndpointSource;
  token?: string;
}): ResolvedWorkspaceConnection {
  return {
    mode: params.mode,
    endpointUrl: params.endpointUrl,
    endpointSource: params.endpointSource,
    token: params.token,
    transport: "CONNECT_RPC",
  };
}

export const resolveWorkspaceConnection: ResolveWorkspaceConnection = async (
  input,
  options,
) => {
  const logger = options?.logger ?? defaultLogger;

  logger.info("workspace_connection.resolve.start", {
    mode: input.mode,
  });

  if (input.mode === WorkspaceMode.Local) {
    const localRuntimeProvider =
      options?.localRuntimeProvider ?? createTauriLocalRuntimeProvider({ logger });
    const localEndpoint = await localRuntimeProvider.resolveLocalWorkspaceEndpoint();

    const resolvedConnection = buildResolvedConnection({
      mode: WorkspaceMode.Local,
      endpointUrl: normalizeEndpointUrl(localEndpoint.endpointUrl, "endpointUrl"),
      endpointSource: localEndpoint.endpointSource,
      token: normalizeOptionalToken(localEndpoint.token),
    });

    logger.info("workspace_connection.resolve.success", {
      mode: resolvedConnection.mode,
      endpoint_source: resolvedConnection.endpointSource,
      transport: resolvedConnection.transport,
      result: "success",
    });

    return resolvedConnection;
  }

  const remoteEndpointUrl = normalizeEndpointUrl(
    input.remoteEndpointUrl ?? "",
    "remoteEndpointUrl",
  );

  const resolvedConnection = buildResolvedConnection({
    mode: WorkspaceMode.Remote,
    endpointUrl: remoteEndpointUrl,
    endpointSource: WorkspaceEndpointSource.UserRemote,
    token: normalizeOptionalToken(input.remoteToken),
  });

  logger.info("workspace_connection.resolve.success", {
    mode: resolvedConnection.mode,
    endpoint_source: resolvedConnection.endpointSource,
    transport: resolvedConnection.transport,
    result: "success",
  });

  return resolvedConnection;
};
