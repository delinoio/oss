import { WorkspaceEndpointSource } from "../contracts/workspace-connection";
import { defaultLogger, type DexDexLogger } from "./logger";

const DEFAULT_LOCAL_REMOTE_URL = "http://127.0.0.1:7878";

type ResolveLocalWorkspaceEndpointPayload = {
  endpoint_url: string;
  token?: string | null;
  endpoint_source: "MANAGED_LOOPBACK";
};

export type LocalWorkspaceEndpoint = {
  endpointUrl: string;
  token?: string;
  endpointSource: WorkspaceEndpointSource.ManagedLoopback;
};

export interface LocalRuntimeProvider {
  resolveLocalWorkspaceEndpoint(): Promise<LocalWorkspaceEndpoint>;
}

function normalizeOptionalToken(token?: string | null): string | undefined {
  if (typeof token !== "string") {
    return undefined;
  }

  const trimmedToken = token.trim();
  return trimmedToken.length > 0 ? trimmedToken : undefined;
}

function normalizeEndpointUrl(endpointUrl: string): string {
  const parsedUrl = new URL(endpointUrl);
  return parsedUrl.toString();
}

export function redactEndpointUrlForLogs(endpointUrl: string): string {
  try {
    const parsedUrl = new URL(endpointUrl);
    parsedUrl.username = "";
    parsedUrl.password = "";
    parsedUrl.search = "";
    parsedUrl.hash = "";
    return parsedUrl.toString();
  } catch {
    return "[invalid-endpoint-url]";
  }
}

function isTauriRuntimeAvailable(): boolean {
  return (
    typeof window !== "undefined" &&
    "__TAURI_INTERNALS__" in (window as Window & Record<string, unknown>)
  );
}

export function createStubLocalRuntimeProvider(options?: {
  defaultEndpointUrl?: string;
  defaultToken?: string;
  logger?: DexDexLogger;
}): LocalRuntimeProvider {
  const logger = options?.logger ?? defaultLogger;

  return {
    async resolveLocalWorkspaceEndpoint(): Promise<LocalWorkspaceEndpoint> {
      const endpointUrl = normalizeEndpointUrl(
        options?.defaultEndpointUrl ?? DEFAULT_LOCAL_REMOTE_URL,
      );
      const token = normalizeOptionalToken(options?.defaultToken);

      logger.info("local_runtime.resolve.stub", {
        endpoint_source: WorkspaceEndpointSource.ManagedLoopback,
        endpoint_url: redactEndpointUrlForLogs(endpointUrl),
      });

      return {
        endpointUrl,
        token,
        endpointSource: WorkspaceEndpointSource.ManagedLoopback,
      };
    },
  };
}

export function createTauriLocalRuntimeProvider(options?: {
  fallbackProvider?: LocalRuntimeProvider;
  logger?: DexDexLogger;
}): LocalRuntimeProvider {
  const logger = options?.logger ?? defaultLogger;
  const fallbackProvider =
    options?.fallbackProvider ?? createStubLocalRuntimeProvider({ logger });

  return {
    async resolveLocalWorkspaceEndpoint(): Promise<LocalWorkspaceEndpoint> {
      logger.info("local_runtime.resolve.start", {
        command: "resolve_local_workspace_endpoint",
      });

      if (!isTauriRuntimeAvailable()) {
        logger.warn("local_runtime.resolve.fallback", {
          result: "fallback",
          reason: "tauri_runtime_not_detected",
        });
        return fallbackProvider.resolveLocalWorkspaceEndpoint();
      }

      try {
        const { invoke } = await import("@tauri-apps/api/core");
        const payload = await invoke<ResolveLocalWorkspaceEndpointPayload>(
          "resolve_local_workspace_endpoint",
        );

        if (payload.endpoint_source !== "MANAGED_LOOPBACK") {
          throw new Error("Unsupported local workspace endpoint source.");
        }

        const endpointUrl = normalizeEndpointUrl(payload.endpoint_url);
        const token = normalizeOptionalToken(payload.token);

        logger.info("local_runtime.resolve.success", {
          endpoint_source: WorkspaceEndpointSource.ManagedLoopback,
          endpoint_url: redactEndpointUrlForLogs(endpointUrl),
          result: "success",
        });

        return {
          endpointUrl,
          token,
          endpointSource: WorkspaceEndpointSource.ManagedLoopback,
        };
      } catch (error) {
        const reason =
          error instanceof Error
            ? error.message
            : "unknown_local_runtime_resolution_error";

        logger.error("local_runtime.resolve.failure", {
          result: "failure",
          reason,
        });

        throw new Error(
          `Failed to resolve local workspace endpoint: ${reason}`,
        );
      }
    },
  };
}
