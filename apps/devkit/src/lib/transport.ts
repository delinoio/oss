import { createConnectTransport } from "@connectrpc/connect-web";

/**
 * Create a Connect RPC transport scoped to a specific backend service.
 * Uses Next.js rewrites to proxy requests to the actual backend server.
 */
export function createDevkitTransport(servicePath: string) {
  return createConnectTransport({
    baseUrl: `/api/${servicePath}`,
  });
}
