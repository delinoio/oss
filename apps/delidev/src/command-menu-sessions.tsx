// SPDX-License-Identifier: Apache-2.0
import { useCallback, useEffect, useRef, useState } from "react";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, FailureCode, isEntityId, newRequestId, ResourceQuery, SessionQuery, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, text } from "./documents";
import { ConversationKind, conversationProvenance } from "./home-navigation";
import { type PaginationReader } from "./scroll-pagination";
import { useConnectPaginationReader, usePaginationChain } from "./scroll-pagination-query";
export interface CommandSession { id: string; revision: bigint; title: string; projectId: string; conversationKind: ConversationKind; sidechatParent?: string }
const validName = (name: string) => Boolean(name.trim()) && !/[\u0000-\u001f\u007f]/.test(name) && new TextEncoder().encode(name).byteLength <= 256;
export function commandSession(row: Resource): CommandSession | undefined {
 if (!isEntityId(row.id) || row.kind !== EntityKind.SESSION || !supportsResourceSchema(row) || row.revision <= 0n || row.revision > 18446744073709551615n || row.projectId && !isEntityId(row.projectId)) return;
 const data = document(row), title = text(data.name);
 if (!validName(title)) return;
 const { conversationKind, sidechatParent } = conversationProvenance(row, data);
 return { id: row.id, revision: row.revision, title, projectId: row.projectId, conversationKind, ...(sidechatParent ? { sidechatParent } : {}) };
}
const sessionRequest = (pageToken: string) => ({ projectId: "", includeArchived: false, pageSize: 50, pageToken });
const sessionProjection = (response: { sessions: Resource[]; nextPageToken: string }) => ({ rows: response.sessions.flatMap(row => { const value = commandSession(row); return value ? [value] : []; }), nextPageToken: response.nextPageToken });
export interface CommandSessionState { rows: CommandSession[]; projects: ReadonlyMap<string, string | undefined>; complete: boolean; failed: boolean; reloadRequired: boolean; retry: () => void; reload: () => void }
/** Only a nonempty-query visit mounts this owner. Query edits do not restart it. */
export function CommandSessionReader({ publish }: { publish: (value: CommandSessionState) => void }) {
 const reader = useConnectPaginationReader(SessionQuery.listSessions, sessionRequest, sessionProjection);
 const latest = useRef(reader); latest.current = reader;
 // Reconnect changes the transport, not this visit's enumeration or cursor.
 const stable = useCallback<PaginationReader<CommandSession>>((token, signal) => latest.current(token, signal), []);
 const scan = usePaginationChain("command-menu-visit", true, stable);
 const transport = useTransport(), client = useQueryClient(), current = useRef({ transport, client }); current.current = { transport, client };
 const [projects, setProjects] = useState<ReadonlyMap<string, string | undefined>>(new Map());
 const retained = useRef(new Map<string, string | undefined>()), queue = useRef<string[]>([]), running = useRef(0), resume = useRef<() => void>(() => {}), owner = useRef<AbortController | undefined>(undefined);
 useEffect(() => { owner.current = new AbortController(); return () => { owner.current?.abort(); retained.current.clear(); queue.current = []; }; }, []);
 useEffect(() => {
  if (scan.loaded && scan.nextPageToken && !scan.loading && !scan.error) {
   // Yield between accepted pages so an immediately resolved transport cannot
   // monopolize rendering or exceed React's nested-update limit.
   const next = window.setTimeout(scan.append, 0); return () => window.clearTimeout(next);
  }
 }, [scan.loaded, scan.nextPageToken, scan.loading, scan.error, scan.append]);
 useEffect(() => {
  const controller = owner.current!;
  for (const row of scan.rows) if (row.projectId && !retained.current.has(row.projectId)) { retained.current.set(row.projectId, undefined); queue.current.push(row.projectId); }
  const pump = () => {
   while (!controller.signal.aborted && running.current < 8 && queue.current.length) {
    const id = queue.current.shift()!; running.current++;
    const { transport, client } = current.current;
    const options = createQueryOptions(ResourceQuery.getResource, { kind: EntityKind.PROJECT, id }, { transport });
    const queryKey = [...options.queryKey, { commandMenuProject: newRequestId() }];
    const cancel = () => { void client.cancelQueries({ queryKey, exact: true }); };
    controller.signal.addEventListener("abort", cancel, { once: true });
    // Release the permit only when the original transport wait settles. Query
    // cancellation may settle the observer before an abort-ignoring transport.
    let started = false;
    const finish = () => { running.current--; resume.current(); };
    void client.fetchQuery({ queryKey, retry: false, gcTime: 0, staleTime: 0, queryFn: async context => {
     started = true;
     try {
      const response = await options.queryFn({ ...context, queryKey: options.queryKey }), row = response.resource;
      if (!row || row.id !== id || row.kind !== EntityKind.PROJECT || !supportsResourceSchema(row) || row.revision <= 0n || row.revision > 18446744073709551615n) return undefined;
      const name = text(document(row).name); return validName(name) ? name : undefined;
     } finally { finish(); }
    } }).then(name => { if (!controller.signal.aborted) { retained.current.set(id, name ?? ""); setProjects(new Map(retained.current)); } }, () => { if (!controller.signal.aborted) { retained.current.set(id, ""); setProjects(new Map(retained.current)); } }).finally(() => { if (!started) finish(); controller.signal.removeEventListener("abort", cancel); client.removeQueries({ queryKey, exact: true }); });
   }
  };
  resume.current = pump; setProjects(new Map(retained.current)); pump();
 }, [scan.rows]);
 useEffect(() => { publish({ rows: scan.rows, projects, complete: scan.loaded && !scan.loading && !scan.error && !scan.nextPageToken, failed: Boolean(scan.error), reloadRequired: Boolean(scan.error?.stalled || scan.error?.failure.code === FailureCode.CursorExpired), retry: scan.retry, reload: scan.reload }); }, [scan.rows, scan.loaded, scan.loading, scan.error, scan.nextPageToken, scan.retry, scan.reload, projects, publish]);
 return null;
}
