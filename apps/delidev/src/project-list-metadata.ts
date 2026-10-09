// SPDX-License-Identifier: Apache-2.0
import { useCallback, useEffect, useRef, useState } from "react";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { clientFailure, EntityKind, isEntityId, newRequestId, ResourceQuery, supportsResourceSchema, type ClientFailure, type Resource } from "@delinoio/delidev-api-client";
import { document, items, text } from "./documents";
import { repositoryCloneURL } from "./repository-clone-fields";

// Permits outlive response generations. Disposed queued reads release their
// place without dispatch, while started transport waits hold their original
// permit until they settle, even when a query observer has been canceled.
class ProjectRepositoryReadPool {
  private running = 0;
  private waiting: (() => void)[] = [];
  async read<T>(signal: AbortSignal, operation: () => Promise<T>): Promise<T> {
    const admitted = await new Promise<boolean>(resolve => {
      const cancel = () => {
        const index = this.waiting.indexOf(start);
        if (index >= 0) this.waiting.splice(index, 1);
        resolve(false);
      };
      const start = () => {
        signal.removeEventListener("abort", cancel);
        if (signal.aborted) { resolve(false); return; }
        this.running++; resolve(true);
      };
      if (signal.aborted) resolve(false);
      else if (this.running < 8) start();
      else { this.waiting.push(start); signal.addEventListener("abort", cancel, { once: true }); }
    });
    if (!admitted) throw new Error("Disposed project repository read");
    try { if (signal.aborted) throw new Error("Disposed project repository read"); return await operation(); }
    finally { this.running--; this.waiting.shift()?.(); }
  }
}

export enum RepositoryDetailsState { Loading, Ready, Unavailable }
export interface RepositoryDetails { state: RepositoryDetailsState; name?: string; url?: string; revision?: bigint; stale?: boolean; pending?: boolean; failure?: ClientFailure }
export function projectRepositoryIds(row: Resource): string[] { return items(document(row).repositories).map(text); }
export function repositoryDetails(row: Resource | undefined, id: string): RepositoryDetails | undefined {
  if (!row || row.id !== id || !isEntityId(id) || row.kind !== EntityKind.REPOSITORY || !supportsResourceSchema(row) || row.revision <= 0n || row.revision > 18446744073709551615n) return;
  const value = document(row), name = text(value.name);
  if (!name.trim() || /[\u0000-\u001f\u007f]/.test(name) || new TextEncoder().encode(name).byteLength > 256) return;
  if (value.remote_url !== undefined && typeof value.remote_url !== "string") return;
  const url = text(value.remote_url);
  if (url && !repositoryCloneURL(url)) return;
  return { state: RepositoryDetailsState.Ready, name, url, revision: row.revision };
}

// One category owner covers every mounted payload page. The cache stores only
// validated display projections; full repository documents are transient.
export function useProjectListMetadata(ids: readonly string[], active: boolean) {
  const transport = useTransport(), client = useQueryClient();
  const [rows, setRows] = useState<ReadonlyMap<string, RepositoryDetails>>(new Map());
  const retained = useRef(new Map<string, RepositoryDetails>()), owner = useRef<AbortController | undefined>(undefined);
  const pool = useRef(new ProjectRepositoryReadPool());
  const identity = JSON.stringify([...new Set(ids)]), selected = useRef(ids), enabled = useRef(active);
  selected.current = ids; enabled.current = active;
  const start = useCallback((refresh: boolean) => {
    owner.current?.abort();
    const controller = new AbortController(); owner.current = controller;
    const allIds = [...new Set(selected.current)], requested = allIds.filter(isEntityId), wanted = new Set(allIds);
    for (const id of allIds) if (!isEntityId(id)) retained.current.set(id, { state: RepositoryDetailsState.Unavailable });
    for (const id of retained.current.keys()) if (!wanted.has(id)) retained.current.delete(id);
    const pending = requested.filter(id => refresh || !retained.current.has(id) || retained.current.get(id)?.state === RepositoryDetailsState.Loading || retained.current.get(id)?.pending);
    for (const id of pending) {
      const previous = retained.current.get(id);
      retained.current.set(id, previous?.name ? { ...previous, stale: true, pending: true, failure: undefined } : { state: RepositoryDetailsState.Loading });
    }
    setRows(new Map(retained.current));
    let next = 0;
    const read = async () => {
      while (!controller.signal.aborted) {
        const id = pending[next++]; if (id === undefined) return;
        const options = createQueryOptions(ResourceQuery.getResource, { kind: EntityKind.REPOSITORY, id }, { transport });
        const queryKey = [...options.queryKey, { projectListMetadata: newRequestId() }];
        try {
          let result = await client.fetchQuery({ queryKey, retry: false, gcTime: 0, staleTime: 0, queryFn: async context => {
            const response = await pool.current.read(controller.signal, async () => options.queryFn({ ...context, queryKey: options.queryKey }));
            return repositoryDetails(response.resource, id) ?? null;
          } });
          if (controller.signal.aborted) return;
          const previous = retained.current.get(id);
          if (result && previous?.revision && (result.revision! < previous.revision || (result.revision === previous.revision && (result.name !== previous.name || result.url !== previous.url)))) result = null;
          retained.current.set(id, result ?? (previous?.name ? { ...previous, stale: true, pending: false } : { state: RepositoryDetailsState.Unavailable }));
        } catch (error) {
          if (controller.signal.aborted) return;
          const failure = clientFailure(error), previous = retained.current.get(id);
          // Repository inspection errors must never expose inspected URLs or
          // credential-bearing native output, even in technical disclosures.
          const safe = { ...failure, message: "Repository details could not be read.", guidance: "Refresh the Projects list after checking the selected connection." };
          retained.current.set(id, previous?.name ? { ...previous, stale: true, pending: false, failure: safe } : { state: RepositoryDetailsState.Unavailable, failure: safe });
        } finally {
          client.removeQueries({ queryKey, exact: true });
        }
        if (!controller.signal.aborted) setRows(new Map(retained.current));
      }
    };
    void Promise.all(Array.from({ length: Math.min(8, pending.length) }, read));
  }, [client, transport]);
  useEffect(() => {
    if (active) start(false);
    else { owner.current?.abort(); retained.current.clear(); setRows(new Map()); }
    return () => owner.current?.abort();
  }, [active, identity, start]);
  // App replaces the QueryClient only when the authenticated identity changes.
  // A same-identity reconnect may replace its transport and keeps accepted
  // projections; failed reads still require the explicit Refresh control.
  useEffect(() => () => { owner.current?.abort(); retained.current.clear(); }, [client]);
  const refresh = useCallback(() => { if (enabled.current) start(true); }, [start]);
  return { rows, refresh };
}
