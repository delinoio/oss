import { useLayoutEffect, useMemo, useSyncExternalStore } from "react";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { EntityKind, ResourceQuery, SessionQuery, newRequestId } from "@delinoio/delidev-api-client";
import { NavigationChain, navigationRow, type NavigationReader } from "./home-navigation";

export enum HomeScope { Catalog = "catalog", Sessions = "sessions" }
export function useNavigationQuery(chain: NavigationChain, scope: HomeScope, projectId: string, includeArchived: boolean, active: boolean) {
  const transport = useTransport();
  const client = useQueryClient();
  const reader = useMemo<NavigationReader>(() => async (token, signal) => {
    const options = scope === HomeScope.Catalog
      ? createQueryOptions(ResourceQuery.listResources, { filter: { kind: EntityKind.PROJECT, pageSize: 50, pageToken: token } }, { transport })
      : createQueryOptions(SessionQuery.listSessions, { projectId, includeArchived, pageSize: 50, pageToken: token }, { transport });
    // This is a generated authenticated Connect Query request, with projection
    // before cache publication. The extra key prevents shape aliasing with
    // ordinary Resource consumers, whose eight-inactive-query rule is unchanged.
    const queryKey = [...options.queryKey, { homeNavigationBatch: newRequestId() }];
    const abort = () => { void client.cancelQueries({ queryKey, exact: true }); };
    signal.addEventListener("abort", abort, { once: true });
    try {
      return await client.fetchQuery({ queryKey, retry: false, gcTime: 0, staleTime: 0, queryFn: async (context) => {
        if (scope === HomeScope.Catalog) {
          const catalog = createQueryOptions(ResourceQuery.listResources, { filter: { kind: EntityKind.PROJECT, pageSize: 50, pageToken: token } }, { transport });
          const response = await catalog.queryFn({ ...context, queryKey: catalog.queryKey });
          return { rows: response.resources.map(navigationRow), nextPageToken: response.nextPageToken };
        }
        const sessions = createQueryOptions(SessionQuery.listSessions, { projectId, includeArchived, pageSize: 50, pageToken: token }, { transport });
        const response = await sessions.queryFn({ ...context, queryKey: sessions.queryKey });
        return { rows: response.sessions.map(navigationRow), nextPageToken: response.nextPageToken };
      } });
    } finally {
      signal.removeEventListener("abort", abort);
      client.removeQueries({ queryKey, exact: true });
    }
  }, [client, transport, scope, projectId, includeArchived]);
  useLayoutEffect(() => {
    if (active) {
      chain.activate();
      // Strict Mode may dispose an earlier read before the observer's promise
      // settles. Each activation owns a fresh request/publication generation.
      void chain.refresh(reader);
    }
    return () => chain.suspend();
  }, [chain, active, reader]);
  // One small active observer makes ordinary mutation invalidation/reconnect
  // refresh the accepted ranges, without keeping a response per loaded batch.
  const options = scope === HomeScope.Catalog
    ? createQueryOptions(ResourceQuery.listResources, { filter: { kind: EntityKind.PROJECT, pageSize: 50 } }, { transport })
    : createQueryOptions(SessionQuery.listSessions, { projectId, includeArchived, pageSize: 50 }, { transport });
  useQuery({ queryKey: [...options.queryKey, { homeNavigationRefresh: true }], queryFn: async () => { await chain.refresh(reader); return null; }, enabled: active, initialData: null, refetchOnMount: false, retry: false, staleTime: Infinity, gcTime: 0, refetchInterval: active && scope === HomeScope.Sessions ? 15000 : false, refetchIntervalInBackground: false, refetchOnWindowFocus: false });
  const snapshot = useSyncExternalStore(chain.subscribe, chain.getSnapshot);
  return { ...snapshot, append: () => { void chain.append(reader); }, retry: () => { void chain.retry(reader); }, reload: () => { void chain.reload(reader); } };
}
