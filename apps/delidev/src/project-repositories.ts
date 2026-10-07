// SPDX-License-Identifier: Apache-2.0
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, ResourceQuery, newRequestId, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, text } from "./documents";
import { ProductError } from "./localization";

export interface ProjectRepository { id: string; name: string; supported: boolean }
const PROJECT_REPOSITORY_NAME_CONCURRENCY = 8;
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
        // A repository ID must identify one catalog entry for the complete
        // traversal. Do not let a later page replace its earlier display name.
        if (page.rows.some(row => retained.current.has(row.id))) throw new ProductError("project-creation.invalidInventory");
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
  const transport = useTransport(), client = useQueryClient();
  const idsKey = JSON.stringify(ids);
  const requestedIds = useMemo(() => [...new Set(ids)], [idsKey]);
  const [state, setState] = useState<{ names: Map<string, string>; loading: boolean; error?: unknown; failedIds: string[] }>({
    names: new Map(), loading: active && requestedIds.length > 0, failedIds: [],
  });
  const owner = useRef<AbortController | undefined>(undefined);
  const start = useCallback(async (readIds: readonly string[], preserveNames: boolean) => {
    owner.current?.abort();
    const controller = new AbortController();
    owner.current = controller;
    setState(previous => ({
      names: preserveNames ? previous.names : new Map(), loading: readIds.length > 0, error: undefined, failedIds: [],
    }));
    let next = 0;
    const failedIds: string[] = [];
    const read = async () => {
      while (!controller.signal.aborted) {
        const index = next++;
        const id = readIds[index];
        if (id === undefined) return;
        const options = createQueryOptions(ResourceQuery.getResource, { kind: EntityKind.REPOSITORY, id }, { transport });
        // Strict Mode can dispose one generation while the replacement is
        // already reading the same ID. Keep their cancellation domains apart.
        const queryKey = [...options.queryKey, { projectRepositoryName: true, batch: newRequestId() }];
        const abort = () => { void client.cancelQueries({ queryKey, exact: true }); };
        controller.signal.addEventListener("abort", abort, { once: true });
        try {
          const result = await client.fetchQuery({ queryKey, retry: false, gcTime: 0, staleTime: 0, queryFn: async context => {
            const response = await options.queryFn({ ...context, queryKey: options.queryKey });
            if (!response.resource || response.resource.id !== id || response.resource.kind !== EntityKind.REPOSITORY) throw new ProductError("project-creation.nameUnavailable");
            return projection(response.resource);
          } });
          if (controller.signal.aborted) return;
          if (result.supported) setState(previous => {
            const names = new Map(previous.names);
            names.set(id, result.name);
            return { ...previous, names };
          });
        } catch (error) {
          if (controller.signal.aborted) return;
          failedIds.push(id);
          setState(previous => ({ ...previous, error: previous.error ?? error }));
        } finally {
          controller.signal.removeEventListener("abort", abort);
          client.removeQueries({ queryKey, exact: true });
        }
      }
    };
    await Promise.all(Array.from({ length: Math.min(PROJECT_REPOSITORY_NAME_CONCURRENCY, readIds.length) }, () => read()));
    if (!controller.signal.aborted) setState(previous => ({ ...previous, loading: false, failedIds }));
  }, [client, transport]);
  useEffect(() => {
    if (!active) {
      owner.current?.abort();
      setState({ names: new Map(), loading: false, failedIds: [] });
      return () => owner.current?.abort();
    }
    void start(requestedIds, false);
    return () => owner.current?.abort();
  }, [active, idsKey, requestedIds, start]);
  const retry = useCallback(() => {
    if (active) void start(state.failedIds.length ? state.failedIds : requestedIds, state.failedIds.length > 0);
  }, [active, requestedIds, start, state.failedIds]);
  return { names: state.names, loading: state.loading, error: state.error, retry };
}
