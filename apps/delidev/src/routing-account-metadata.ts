// SPDX-License-Identifier: Apache-2.0
import { useEffect, useMemo, useRef, useState } from "react";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, ResourceQuery, clientFailure, isEntityId, newRequestId, subscriptionService, subscriptionServiceNames, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { observeSettingsUnaryCompletion, useSettingsOpening } from "./settings-lifetime";
import { useSettingsTaskDismiss } from "./settings-task-context";
import { document, text } from "./documents";

export enum RoutingMetadataState { Loading = "loading", Ready = "ready", Unavailable = "unavailable" }
export interface RoutingAccountMetadata { state: RoutingMetadataState; name?: string; source?: string; sourceState?: RoutingMetadataState }
// One category owns four slots across account/provider reads and response generations.
class RoutingReadPool {
  private running = 0;
  private waiting: (() => void)[] = [];
  async read<T>(signal: AbortSignal, operation: (retain: (completion: Promise<void>) => void) => Promise<T>): Promise<T> {
    await new Promise<void>(resolve => {
      const start = () => { this.running++; resolve(); };
      if (this.running < 4) start(); else this.waiting.push(start);
    });
    const completions: Promise<void>[] = [];
    try { if (signal.aborted) throw new Error("Disposed routing metadata read"); return await operation(completion => completions.push(completion)); }
    finally {
      const release = () => { this.running--; this.waiting.shift()?.(); };
      // A nested opening rejects visible waits immediately on close. Its actual
      // upstream RPC may ignore abort, so only deepest completion frees a slot.
      if (completions.length) void Promise.all(completions).then(release);
      else release();
    }
  }
}
const categoryPools = new WeakMap<object, RoutingReadPool>();
function categoryPool(owner: object) { let pool = categoryPools.get(owner); if (!pool) { pool = new RoutingReadPool(); categoryPools.set(owner, pool); } return pool; }

interface Projection { name: string; source?: string; providerId?: string }

function projectMetadata(row: Resource, kind: EntityKind, id: string): Projection {
  if (row.id !== id || row.kind !== kind || row.revision < 1n || !supportsResourceSchema(row)) throw new Error("Invalid routing display metadata");
  const data = document(row);
  const name = text(data.name) || text(data.alias);
  if (data.retired === true || !name.trim() || name.includes("\0") || new TextEncoder().encode(name).length > 256) throw new Error("Unavailable routing display metadata");
  if (kind === EntityKind.PROVIDER) return { name };
  const service = subscriptionService(data.subscription_service);
  if (data.type === "subscription" && service) return { name, source: subscriptionServiceNames[service] };
  if (data.type === "api" && isEntityId(text(data.provider_id))) return { name, providerId: text(data.provider_id) };
  throw new Error("Unsupported routing display metadata");
}

// Names enrich the server's routing evidence only. Private query generations
// prevent a disposed dialog or Strict Mode cleanup from canceling another read.
// Project before caching so credentials and complete documents are not retained.
export function useRoutingAccountMetadata(ids: readonly string[], active: boolean, revision: string, sources: readonly string[] = []) {
  const transport = useTransport(), client = useQueryClient(), opening = useSettingsOpening();
  const pool = categoryPool(opening?.categoryOwner ?? transport);
  const sourcesKey = JSON.stringify([...new Set(sources)]);
  const requestedSources = useMemo<string[]>(() => JSON.parse(sourcesKey), [sourcesKey]);
  const idsKey = JSON.stringify([...new Set(ids)]);
  const requested = useMemo<string[]>(() => JSON.parse(idsKey), [idsKey]);
  const key = `${idsKey}:${sourcesKey}:${revision}`;
  const [state, setState] = useState<{ key: string; entries: Map<string, RoutingAccountMetadata> }>({ key: "", entries: new Map() });
  const owner = useRef<AbortController | undefined>(undefined);
  useSettingsTaskDismiss(() => owner.current?.abort());
  useEffect(() => {
    if (!active) return;
    const controller = new AbortController(), batch = newRequestId();
    owner.current = controller;
    const providers = new Map<string, Promise<Projection>>();
    setState({ key, entries: new Map() });
    const read = async (kind: EntityKind, id: string): Promise<Projection> => {
      if (controller.signal.aborted || !isEntityId(id)) throw new Error("Unavailable routing display identity");
      const options = createQueryOptions(ResourceQuery.getResource, { kind, id }, { transport });
      const queryKey = [...options.queryKey, { routingMetadata: batch }];
      // Keep original waits in the shared pool until their transport settles.
      // Cancellation fences publication without opening another four slots.
      try {
        return await client.fetchQuery({ queryKey, retry: false, gcTime: 0, staleTime: 0, queryFn: async context => {
          const result = await pool.read(controller.signal, async retain => {
            const observed = observeSettingsUnaryCompletion(transport, retain);
            const request = createQueryOptions(ResourceQuery.getResource, { kind, id }, { transport: observed });
            return request.queryFn({ ...context, queryKey: options.queryKey });
          });
          if (!result.resource) throw new Error("Missing routing display metadata");
          return projectMetadata(result.resource, kind, id);
        } });
      } catch (error) {
        if (!controller.signal.aborted) console.warn("delidev.routing_metadata.read_failed", {
          kind: kind === EntityKind.ACCOUNT ? "account" : "provider", classification: clientFailure(error).code,
        });
        throw error;
      } finally {
        client.removeQueries({ queryKey, exact: true });
      }
    };
    const providerRead = (id: string) => {
      let pending = providers.get(id);
      if (!pending) { pending = read(EntityKind.PROVIDER, id); providers.set(id, pending); }
      return pending;
    };
    const sourceReads = requestedSources.map(async source => {
      let metadata: RoutingAccountMetadata = { state: RoutingMetadataState.Unavailable };
      const service = source.startsWith("subscription:") ? subscriptionService(source.slice(13)) : undefined;
      if (service) metadata = { state: RoutingMetadataState.Ready, name: subscriptionServiceNames[service] };
      else if (source.startsWith("api:") && isEntityId(source.slice(4))) {
        try { metadata = { state: RoutingMetadataState.Ready, name: (await providerRead(source.slice(4))).name }; }
        catch { /* Keep explicit unavailable metadata without guessing a provider. */ }
      }
      if (!controller.signal.aborted) setState(previous => ({ key, entries: new Map(previous.entries).set(`source:${source}`, metadata) }));
    });
    let next = 0;
    const worker = async () => {
      while (!controller.signal.aborted) {
        const id = requested[next++];
        if (id === undefined) return;
        let metadata: RoutingAccountMetadata = { state: RoutingMetadataState.Unavailable };
        try {
          const account = await read(EntityKind.ACCOUNT, id);
          if (controller.signal.aborted) return;
          metadata = { state: RoutingMetadataState.Ready, name: account.name, source: account.source, sourceState: account.providerId ? RoutingMetadataState.Loading : RoutingMetadataState.Ready };
          if (account.providerId) setState(previous => ({ key, entries: new Map(previous.entries).set(id, metadata) }));
          if (account.providerId) {
            const provider = providerRead(account.providerId);
            try { metadata = { ...metadata, source: (await provider).name, sourceState: RoutingMetadataState.Ready }; }
            catch { metadata = { ...metadata, sourceState: RoutingMetadataState.Unavailable }; }
          }
        } catch { /* A display read failure cannot discard or change routing evidence. */ }
        if (controller.signal.aborted) return;
        setState(previous => ({ key, entries: new Map(previous.entries).set(id, metadata) }));
      }
    };
    void Promise.all([...sourceReads, ...Array.from({ length: Math.min(4, requested.length) }, () => worker())]);
    return () => controller.abort();
  }, [active, client, key, pool, requested, requestedSources, transport]);
  // Fence old names synchronously when project/result identity changes, before
  // the next effect starts. Late continuations also check their original abort.
  return active && state.key === key ? state.entries : new Map<string, RoutingAccountMetadata>();
}
