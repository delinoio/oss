// SPDX-License-Identifier: Apache-2.0
import { useCallback, useEffect, useRef, useState } from "react";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQueries, useQueryClient, type QueryFunctionContext, type UseQueryOptions } from "@tanstack/react-query";
import { EntityKind, ResourceQuery, newRequestId, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, text } from "./documents";
import { ProductError } from "./localization";

export interface ProjectRepository { id: string; name: string; supported: boolean }
function projection(row: Resource): ProjectRepository {
  const name = text(document(row).name);
  return { id: row.id, name, supported: row.kind === EntityKind.REPOSITORY && supportsResourceSchema(row) && Boolean(name.trim()) && !name.includes("\0") && new TextEncoder().encode(name).byteLength <= 256 };
}
interface Catalog { rows: ProjectRepository[]; loading: boolean; complete: boolean; error?: unknown }

// Each RPC remains bounded. Only display metadata accumulates; original resource
// documents never accumulate in either this controller or React Query's cache.
export function useProjectRepositoryCatalog(active: boolean) {
  const transport = useTransport(), client = useQueryClient();
  const [catalog, setCatalog] = useState<Catalog>({ rows: [], loading: true, complete: false });
  const retained = useRef(new Map<string, ProjectRepository>());
  const cursor = useRef("");
  const accepted = useRef(new Set<string>());
  const owner = useRef<AbortController | undefined>(undefined);
  const load = useCallback(async (reload = false) => {
    owner.current?.abort();
    const controller = new AbortController();
    owner.current = controller;
    if (reload) { retained.current.clear(); accepted.current.clear(); cursor.current = ""; }
    setCatalog({ rows: [...retained.current.values()], loading: true, complete: false });
    try {
      while (!controller.signal.aborted) {
        const token = cursor.current;
        const options = createQueryOptions(ResourceQuery.listResources, { filter: { kind: EntityKind.REPOSITORY, pageSize: 50, pageToken: token } }, { transport });
        const queryKey = [...options.queryKey, { projectRepositoryBatch: newRequestId() }];
        const abort = () => { void client.cancelQueries({ queryKey, exact: true }); };
        controller.signal.addEventListener("abort", abort, { once: true });
        let page: { rows: ProjectRepository[]; next: string };
        try {
          page = await client.fetchQuery({ queryKey, retry: false, gcTime: 0, staleTime: 0, queryFn: async context => {
            const result = await options.queryFn({ ...context, queryKey: options.queryKey });
            if (result.resources.length > 50 || result.resources.some(row => row.kind !== EntityKind.REPOSITORY || !row.id) || new Set(result.resources.map(row => row.id)).size !== result.resources.length) throw new ProductError("project-creation.invalidInventory");
            return { rows: result.resources.map(projection), next: result.nextPageToken };
          } });
        } finally {
          controller.signal.removeEventListener("abort", abort);
          client.removeQueries({ queryKey, exact: true });
        }
        if (controller.signal.aborted) return;
        // A repeated cursor must not create an unbounded traversal or silently
        // claim that a partial inventory is complete. Reload starts a new chain.
        if (page.next && (page.next === token || accepted.current.has(page.next))) throw new ProductError("project-creation.invalidInventory");
        accepted.current.add(token);
        for (const row of page.rows) retained.current.set(row.id, row);
        cursor.current = page.next;
        setCatalog({ rows: [...retained.current.values()], loading: Boolean(page.next), complete: !page.next });
        if (!page.next) return;
      }
    } catch (error) {
      if (!controller.signal.aborted) setCatalog({ rows: [...retained.current.values()], loading: false, complete: false, error });
    }
  }, [client, transport]);
  useEffect(() => {
    if (active) void load(true);
    return () => owner.current?.abort();
  }, [active, load]);
  return { ...catalog, retry: () => { if (active) void load(); }, reload: () => { if (active) void load(true); } };
}

// Editing resolves the exact selected IDs, including entries outside the
// selector's current page. Missing names never substitute another repository.
export function useProjectRepositoryNames(ids: readonly string[], active: boolean) {
  const transport = useTransport();
  const results = useQueries({ queries: ids.map<UseQueryOptions<ProjectRepository>>(id => {
    const options = createQueryOptions(ResourceQuery.getResource, { kind: EntityKind.REPOSITORY, id }, { transport });
    return { queryKey: [...options.queryKey, { projectRepositoryName: true }], enabled: active && Boolean(id), gcTime: 0, retry: false, queryFn: async (context: QueryFunctionContext) => {
      const result = await options.queryFn({ ...context, queryKey: options.queryKey });
      if (!result.resource || result.resource.id !== id || result.resource.kind !== EntityKind.REPOSITORY) throw new ProductError("project-creation.nameUnavailable");
      return projection(result.resource);
    } };
  }) });
  const names = new Map<string, string>();
  results.forEach((result, index) => { if (result.data?.supported) names.set(ids[index]!, result.data.name); });
  return { names, loading: results.some(result => result.isLoading), error: results.find(result => result.error)?.error,
    retry: () => { if (active) for (const result of results) if (result.error) void result.refetch(); } };
}
