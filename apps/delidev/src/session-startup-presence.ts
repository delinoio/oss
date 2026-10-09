// SPDX-License-Identifier: Apache-2.0
import { createClient, type Transport } from "@connectrpc/connect";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useLayoutEffect, useMemo } from "react";
import { EntityKind, ResourceQuery, ResourceService, SystemService } from "@delinoio/delidev-api-client";

// Machine JSON remains compatible with original strict readers. The existing
// Overview clock is read afterward on the exact same authenticated transport;
// neither response authorizes execution or replaces the original Worker lease.
export async function readStartupPresence(transport: Transport, machineId: string, signal: AbortSignal) {
  const startedAt = performance.now();
  signal.throwIfAborted();
  const response = await createClient(ResourceService, transport).getResource({ kind: EntityKind.MACHINE, id: machineId }, { signal });
  signal.throwIfAborted();
  const overview = await createClient(SystemService, transport).getOverview({}, { signal });
  signal.throwIfAborted();
  return { resource: response.resource, observedAt: overview.observedAt, startedAt };
}
export function useStartupPresence(machineId: string, owner: string, enabled: boolean) {
  const transport = useTransport(), client = useQueryClient();
  const options = useMemo(() => {
    const original = createQueryOptions(ResourceQuery.getResource, { kind: EntityKind.MACHINE, id: machineId }, { transport });
    return { queryKey: [...original.queryKey, { startupPresence: owner }], queryFn: ({ signal }: { signal: AbortSignal }) => readStartupPresence(transport, machineId, signal) };
  }, [transport, machineId, owner]);
  // A tab becoming inactive must cancel an in-flight pair as well as its timer.
  // Cancellation fences late completion and never sends a mutation or retry.
  useLayoutEffect(() => {
    if (!enabled) void client.cancelQueries({ queryKey: options.queryKey, exact: true });
    return () => { void client.cancelQueries({ queryKey: options.queryKey, exact: true }); };
  }, [client, options, enabled]);
  return useQuery({ ...options, enabled, retry: false, gcTime: 0, staleTime: 0, refetchInterval: enabled ? 5000 : false, refetchOnWindowFocus: false, refetchOnReconnect: false });
}
