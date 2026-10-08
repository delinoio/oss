// SPDX-License-Identifier: Apache-2.0
import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useId, useRef, useSyncExternalStore, type ReactNode } from "react";
import { EntityKind, ResourceQuery, clientFailure, isEntityId, newRequestId, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document } from "./documents";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";

// This category pool surrounds the original transport await. Query observer
// cancellation must never make a fifth transport request eligible while an
// original abort-ignoring wait is still unsettled.
class AgentModelReadPool {
  private running = 0;
  private waiting: (() => void)[] = [];
  async read<T>(signal: AbortSignal, operation: () => Promise<T>): Promise<T> {
    const admitted = await new Promise<boolean>(resolve => {
      const cancel = () => { const index = this.waiting.indexOf(start); if (index >= 0) this.waiting.splice(index, 1); resolve(false); };
      const start = () => { signal.removeEventListener("abort", cancel); if (signal.aborted) { resolve(false); return; } this.running++; resolve(true); };
      if (signal.aborted) resolve(false);
      else if (this.running < 4) start();
      else { this.waiting.push(start); signal.addEventListener("abort", cancel, { once: true }); }
    });
    if (!admitted) throw new Error("Disposed configured model read");
    try { if (signal.aborted) throw new Error("Disposed configured model read"); return await operation(); }
    finally { this.running--; this.waiting.shift()?.(); }
  }
}

export enum ModelSummaryState { Loading = "loading", Ready = "ready", Unavailable = "unavailable" }
export interface ModelSummary { state: ModelSummaryState; nativeID?: string; name?: string }
const unavailable: ModelSummary = { state: ModelSummaryState.Unavailable };
const loading: ModelSummary = { state: ModelSummaryState.Loading };
function identity(value: unknown): value is string { return typeof value === "string" && value.length <= 256 && Boolean(value.trim()) && !value.includes("\0") && new TextEncoder().encode(value).length <= 256; }
export function modelSummary(row: Resource | undefined, id: string): ModelSummary {
  if (!row || row.id !== id || row.kind !== EntityKind.MODEL || row.revision < 1n || !supportsResourceSchema(row)) throw new Error("Invalid configured model metadata");
  const data = document(row);
  if (!identity(data.native_id) || !identity(data.name)) throw new Error("Unreadable configured model identity");
  return { state: ModelSummaryState.Ready, nativeID: data.native_id, name: data.name };
}

// One category owner shares this queue across every mounted payload page. Only
// bounded display projections survive a read; neither rows nor credentials do.
export class AgentModelReader {
  private entries = new Map<string, ModelSummary>();
  private listeners = new Set<() => void>();
  private queue: string[] = [];
  private owners = new Map<string, Set<string>>();
  private managed = false;
  private tokens = new Map<string, number>();
  private sequence = 0;
  private controller = new AbortController();
  private generation = 0;
  private running = 0;
  private active = false;
  private disposed = false;
  constructor(private read: (id: string, signal: AbortSignal) => Promise<ModelSummary>) {}
  reopen() { if (this.disposed) { this.disposed = false; this.controller = new AbortController(); this.active = false; } }
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  snapshot = () => this.entries;
  private publish() { this.entries = new Map(this.entries); this.listeners.forEach(listener => listener()); }
  retain(owner: string, ids: readonly string[]) {
    this.managed = true; this.owners.set(owner, new Set(ids.filter(isEntityId))); this.prune();
  }
  release(owner: string) { this.owners.delete(owner); this.prune(); }
  private wanted(id: string) { return !this.managed || [...this.owners.values()].some(ids => ids.has(id)); }
  private prune() {
    this.queue = this.queue.filter(id => this.wanted(id));
    let changed = false;
    for (const id of this.entries.keys()) if (!this.wanted(id)) { this.entries.delete(id); this.tokens.delete(id); changed = true; }
    for (const id of this.tokens.keys()) if (!this.wanted(id)) this.tokens.delete(id);
    if (changed) this.publish();
  }
  request(ids: readonly string[]) {
    if (!this.active || this.disposed) return;
    let changed = false;
    for (const id of ids) {
      if (!isEntityId(id) || !this.wanted(id) || this.entries.has(id)) continue;
      this.entries.set(id, loading); this.tokens.set(id, ++this.sequence); this.queue.push(id); changed = true;
    }
    if (changed) this.publish();
    this.pump();
  }
  setActive(active: boolean) {
    if (this.disposed || this.active === active) return;
    this.active = active;
    if (!active) {
      this.controller.abort(); this.controller = new AbortController(); this.generation++; this.queue = [];
      for (const [id, value] of this.entries) if (value.state === ModelSummaryState.Loading) { this.entries.delete(id); this.tokens.delete(id); }
      this.publish();
    } else this.pump();
  }
  refresh() {
    if (this.disposed) return;
    this.controller.abort(); this.controller = new AbortController(); this.generation++; this.queue = []; this.tokens.clear(); this.entries.clear(); this.publish();
  }
  dispose() { this.disposed = true; this.active = false; this.controller.abort(); this.generation++; this.queue = []; this.owners.clear(); this.tokens.clear(); this.entries.clear(); this.publish(); }
  private pump() {
    while (this.active && !this.disposed && this.running < 4 && this.queue.length) {
      const id = this.queue.shift()!, generation = this.generation, token = this.tokens.get(id), signal = this.controller.signal;
      this.running++;
      void this.read(id, signal).catch(error => {
        if (!signal.aborted) console.warn("delidev.agent_model_summary.read_failed", { classification: clientFailure(error).code });
        return unavailable;
      }).then(value => {
        if (this.disposed || signal.aborted || generation !== this.generation || this.tokens.get(id) !== token || !this.wanted(id)) return;
        this.entries.set(id, value); this.publish();
      }).finally(() => { this.running--; this.pump(); });
    }
  }
}
interface MetadataContext { reader: AgentModelReader; entries: Map<string, ModelSummary>; active: boolean }
const Context = createContext<MetadataContext | undefined>(undefined);
export function AgentWorkerMetadataProvider({ active, refresh, children }: { active: boolean; refresh: number; children: ReactNode }) {
  const transport = useTransport(), client = useQueryClient();
  const pool = useRef(new AgentModelReadPool());
  const read = useCallback(async (id: string, signal: AbortSignal) => {
    const options = createQueryOptions(ResourceQuery.getResource, { kind: EntityKind.MODEL, id }, { transport });
    const queryKey = [...options.queryKey, { agentModelMetadata: newRequestId() }];
    try {
      return await client.fetchQuery({ queryKey, retry: false, gcTime: 0, staleTime: 0, queryFn: async context => {
        const result = await pool.current.read(signal, async () => options.queryFn({ ...context, queryKey: options.queryKey }));
        return modelSummary(result.resource, id);
      } });
    } finally { client.removeQueries({ queryKey, exact: true }); }
  }, [client, transport]);
  // App retains the QueryClient for same-identity authentication reconnects.
  // Keep the reader, references and original unsettled permits in that scope;
  // subsequent explicit reads use the latest authenticated transport.
  const currentRead = useRef(read); currentRead.current = read;
  const reader = useMemo(() => new AgentModelReader((id, signal) => currentRead.current(id, signal)), [client]);
  const entries = useSyncExternalStore(reader.subscribe, reader.snapshot, reader.snapshot);
  useLayoutEffect(() => { reader.reopen(); return () => reader.dispose(); }, [reader]);
  useLayoutEffect(() => { reader.setActive(active); }, [reader, active]);
  useLayoutEffect(() => { reader.refresh(); }, [reader, refresh]);
  const value = useMemo(() => ({ reader, entries, active }), [reader, entries, active]);
  return <Context.Provider value={value}>{children}</Context.Provider>;
}
export function useAgentModels(ids: readonly string[]) {
  const context = useContext(Context), key = JSON.stringify(ids), owner = useId();
  useLayoutEffect(() => () => context?.reader.release(owner), [context?.reader, owner]);
  useLayoutEffect(() => { context?.reader.retain(owner, JSON.parse(key)); }, [context?.reader, owner, key]);
  useEffect(() => { if (context?.active) context.reader.request(JSON.parse(key)); }, [context?.reader, context?.active, context?.entries, key]);
  return (id: string) => context?.entries.get(id) ?? (!isEntityId(id) || !context ? unavailable : loading);
}
