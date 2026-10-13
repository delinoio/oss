// SPDX-License-Identifier: Apache-2.0
import { createClient, type Transport } from "@connectrpc/connect";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useLayoutEffect, useMemo, useState } from "react";
import {
  EntityKind,
  ResourceQuery,
  ResourceService,
  SystemService,
  type Resource,
} from "@delinoio/delidev-api-client";
import { startupWorkerLeaseRemainingMs } from "./session-startup-operations";

// Machine JSON remains compatible with original strict readers. The existing
// Overview clock is read afterward on the exact same authenticated transport;
// neither response authorizes execution or replaces the original Worker lease.
export async function readStartupPresence(
  transport: Transport,
  machineId: string,
  signal: AbortSignal,
) {
  const startedAt = performance.now();
  signal.throwIfAborted();
  const response = await createClient(ResourceService, transport).getResource(
    { kind: EntityKind.MACHINE, id: machineId },
    { signal },
  );
  signal.throwIfAborted();
  const overview = await createClient(SystemService, transport).getOverview(
    {},
    { signal },
  );
  signal.throwIfAborted();
  return {
    resource: response.resource,
    transport,
    observedAt: overview.observedAt,
    clockReceivedAt: performance.now(),
    startedAt,
  };
}
export function useStartupPresence(
  machineId: string,
  owner: string,
  enabled: boolean,
) {
  const transport = useTransport(),
    client = useQueryClient();
  const options = useMemo(() => {
    const original = createQueryOptions(
      ResourceQuery.getResource,
      { kind: EntityKind.MACHINE, id: machineId },
      { transport },
    );
    return {
      queryKey: [...original.queryKey, { startupPresence: owner }],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        readStartupPresence(transport, machineId, signal),
    };
  }, [transport, machineId, owner]);
  // A tab becoming inactive must cancel an in-flight pair as well as its timer.
  // Cancellation fences late completion and never sends a mutation or retry.
  useLayoutEffect(() => {
    if (!enabled)
      void client.cancelQueries({ queryKey: options.queryKey, exact: true });
    return () => {
      void client.cancelQueries({ queryKey: options.queryKey, exact: true });
    };
  }, [client, options, enabled]);
  return useQuery({
    ...options,
    enabled,
    retry: false,
    gcTime: 0,
    staleTime: 0,
    refetchInterval: enabled ? 5000 : false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  });
}

/**
 * Re-renders once when the original Worker lease of the retained pair expires,
 * so a stalled reread cannot keep the retained startup presentation past the
 * original 60-second boundary. Never reads, extends or refreshes the lease.
 */
export function useStartupLeaseExpiry(
  session: Resource | undefined,
  observation:
    | { resource?: Resource; observedAt: string; startedAt: number }
    | undefined,
  retained: boolean,
) {
  const [, redraw] = useState(0);
  useEffect(() => {
    if (!retained || !observation) return;
    const remaining = startupWorkerLeaseRemainingMs(
      session,
      observation.resource,
      true,
      observation.observedAt,
      performance.now() - observation.startedAt,
    );
    if (remaining === undefined || !(remaining > 0)) return;
    const timer = setTimeout(
      () => redraw((v) => v + 1),
      Math.ceil(remaining) + 1,
    );
    return () => clearTimeout(timer);
  }, [session, observation, retained]);
}
