import { createConnectTransport } from "@connectrpc/connect-web";
import { invoke } from "@tauri-apps/api/core";

interface LocalWorkspaceEndpoint {
  url: string;
  token: string | null;
  source: string;
}

let cachedTransport: ReturnType<typeof createConnectTransport> | null = null;

export async function resolveTransport() {
  if (cachedTransport) return cachedTransport;

  let baseUrl = "http://127.0.0.1:7878";

  try {
    const endpoint = await invoke<LocalWorkspaceEndpoint>(
      "resolve_local_workspace_endpoint",
    );
    baseUrl = endpoint.url;
  } catch {
    // Fallback to default when not running in Tauri (e.g., browser dev mode)
  }

  cachedTransport = createConnectTransport({ baseUrl });
  return cachedTransport;
}

/**
 * Synchronous transport for use in React Query provider.
 * Uses default endpoint; call resolveTransport() for Tauri-resolved endpoint.
 */
export function createDefaultTransport() {
  return createConnectTransport({
    baseUrl: "http://127.0.0.1:7878",
  });
}
