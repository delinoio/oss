// SPDX-License-Identifier: Apache-2.0
import { useEffect, useMemo, useRef, useState } from "react";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, ResourceQuery, clientFailure, isEntityId, newRequestId, subscriptionService, subscriptionServiceNames, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { useSettingsTaskDismiss } from "./settings-task-context";
import { document, text } from "./documents";

export enum RoutingMetadataState { Loading = "loading", Ready = "ready", Unavailable = "unavailable" }
export interface RoutingAccountMetadata { state: RoutingMetadataState; name?: string; source?: string; sourceState?: RoutingMetadataState }
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
export function useRoutingAccountMetadata(ids: readonly string[], active: boolean, revision: string) {
  const transport = useTransport(), client = useQueryClient();
  const idsKey = JSON.stringify([...new Set(ids)]);
  const requested = useMemo<string[]>(() => JSON.parse(idsKey), [idsKey]);
  const key = `${idsKey}:${revision}`;
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
      const abort = () => { void client.cancelQueries({ queryKey, exact: true }); };
      controller.signal.addEventListener("abort", abort, { once: true });
      try {
        return await client.fetchQuery({ queryKey, retry: false, gcTime: 0, staleTime: 0, queryFn: async context => {
          const result = await options.queryFn({ ...context, queryKey: options.queryKey });
          if (!result.resource) throw new Error("Missing routing display metadata");
          return projectMetadata(result.resource, kind, id);
        } });
      } catch (error) {
        if (!controller.signal.aborted) console.warn("delidev.routing_metadata.read_failed", {
          kind: kind === EntityKind.ACCOUNT ? "account" : "provider", classification: clientFailure(error).code,
        });
        throw error;
      } finally {
        controller.signal.removeEventListener("abort", abort);
        client.removeQueries({ queryKey, exact: true });
      }
    };
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
            let provider = providers.get(account.providerId);
            if (!provider) { provider = read(EntityKind.PROVIDER, account.providerId); providers.set(account.providerId, provider); }
            try { metadata = { ...metadata, source: (await provider).name, sourceState: RoutingMetadataState.Ready }; }
            catch { metadata = { ...metadata, sourceState: RoutingMetadataState.Unavailable }; }
          }
        } catch { /* A display read failure cannot discard or change routing evidence. */ }
        if (controller.signal.aborted) return;
        setState(previous => ({ key, entries: new Map(previous.entries).set(id, metadata) }));
      }
    };
    void Promise.all(Array.from({ length: Math.min(4, requested.length) }, () => worker()));
    return () => controller.abort();
  }, [active, client, key, requested, transport]);
  // Fence old names synchronously when project/result identity changes, before
  // the next effect starts. Late continuations also check their original abort.
  return active && state.key === key ? state.entries : new Map<string, RoutingAccountMetadata>();
}
