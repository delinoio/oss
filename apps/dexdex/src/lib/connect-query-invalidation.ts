import type { QueryClient } from "@tanstack/react-query";

const CONNECT_QUERY_KEY_PREFIX = "connect-query";

interface ConnectQueryService {
  typeName: string;
}

export type ConnectQueryServiceKey = [
  typeof CONNECT_QUERY_KEY_PREFIX,
  {
    serviceName: string;
  },
];

function serviceNameFromValue(service: ConnectQueryService | string): string {
  if (typeof service === "string") {
    return service;
  }
  return service.typeName;
}

/**
 * Build a Connect Query service-level key compatible with @connectrpc/connect-query.
 */
export function createConnectQueryServiceKey(service: ConnectQueryService | string): ConnectQueryServiceKey {
  return [
    CONNECT_QUERY_KEY_PREFIX,
    {
      serviceName: serviceNameFromValue(service),
    },
  ];
}

/**
 * Invalidate all cached queries for a specific Connect RPC service.
 */
export function invalidateConnectQueryServiceQueries(
  queryClient: QueryClient,
  service: ConnectQueryService | string,
): Promise<void> {
  return queryClient.invalidateQueries({
    queryKey: createConnectQueryServiceKey(service),
  });
}
