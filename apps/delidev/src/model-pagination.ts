// SPDX-License-Identifier: Apache-2.0
import { useCallback } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { ProviderQuery, NativeModelQuery, EntityKind, ErrorDetailSchema, FailureCode, type ListProviderInventoryResponse, type SearchModelsResponse, type ListNativeModelsResponse, type ProviderInventoryEntry } from "@delinoio/delidev-api-client";
import { useConnectPaginationReader, usePaginationChain, usePaginationRefresh } from "./scroll-pagination-query";
import { resourceName, document, text, type Document } from "./documents";

export const inventoryIdentity = (entry: ProviderInventoryEntry) => entry.presetId ? `preset:${entry.presetId}` : entry.providerId;
function invalid(): never { throw new ConnectError("The model inventory page is malformed.", Code.DataLoss, undefined, [{ desc: ErrorDetailSchema, value: { code: FailureCode.Internal } }]); }
function identities(rows: { id: string; revision: bigint }[]) {
  if (rows.length > 50 || rows.some(row => !row.id || row.revision < 1n) || new Set(rows.map(row => row.id)).size !== rows.length) invalid();
  return rows;
}
export function useProviderPages(query: string, active: boolean, enabledOnly = false) {
  const request = useCallback((token: string) => ({ query, enabledOnly, pageSize: 50, pageToken: token }), [query, enabledOnly]);
  const project = useCallback((response: ListProviderInventoryResponse) => ({ rows: identities(response.entries.map(entry => ({ id: inventoryIdentity(entry), revision: entry.provider?.revision ?? 1n }))), payload: [response], nextPageToken: response.nextPageToken }), []);
  const reader = useConnectPaginationReader(ProviderQuery.listProviderInventory, request, project);
  const chain = usePaginationChain(`providers:${enabledOnly}:${query}`, active, reader);
  usePaginationRefresh(ProviderQuery.listProviderInventory, request(""), active, chain.refresh);
  return { ...chain, data: chain.payloadPages.at(-1)?.payload[0], isLoading: !chain.loaded && !chain.error, isFetching: Boolean(chain.loading), refetch: chain.refresh };
}
export function useModelPages(query: string, active: boolean) {
  const request = useCallback((token: string) => ({ query, providerId: "", includeHidden: true, pageSize: 50, pageToken: token, enabledProvidersOnly: true }), [query]);
  const project = useCallback((response: SearchModelsResponse) => {
    if (response.models.some(row => row.kind !== EntityKind.MODEL)) invalid();
    return { rows: identities(response.models.map(({ id, revision }) => ({ id, revision }))), payload: response.models.map(model => ({ id: model.id, revision: model.revision, model, providerName: resourceName(response.providers.find(provider => provider.id === text(document(model).provider_id))) })), nextPageToken: response.nextPageToken };
  }, []);
  const reader = useConnectPaginationReader(ProviderQuery.searchModels, request, project);
  const chain = usePaginationChain(`models:${query}`, active, reader);
  usePaginationRefresh(ProviderQuery.searchModels, request(""), active, chain.refresh);
  return { ...chain, data: chain.loaded ? { models: chain.payloadPages.flatMap(page => page.payload.map(row => row.model)) } : undefined, isLoading: !chain.loaded && !chain.error, isFetching: Boolean(chain.loading), refetch: chain.refresh };
}
export interface NativeModelPage { id: string; revision: bigint; job: NonNullable<ListNativeModelsResponse["job"]>; entry: Document }
export function useNativeModelPages(source: string, active: boolean) {
  const request = useCallback((token: string) => ({ jobId: source, pageSize: 50, pageToken: token }), [source]);
  const project = useCallback((response: ListNativeModelsResponse) => {
    let entries: Document[];
    try {
      if (response.modelsJson.byteLength > 768 * 1024 || response.job?.id !== source || response.job.revision < 1n) invalid();
      const data: unknown = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(response.modelsJson));
      if (!Array.isArray(data) || data.length > 50 || data.some(entry => !entry || typeof entry !== "object" || typeof entry.id !== "string" || typeof entry.model !== "string" || typeof entry.display_name !== "string" || entry.id.length > 256 || entry.model.length > 256 || entry.display_name.length > 256)) invalid();
      entries = data;
    } catch { invalid(); }
    return { rows: identities(entries.map(entry => ({ id: entry.id as string, revision: response.job!.revision }))), payload: entries.map(entry => ({ id: entry.id as string, revision: response.job!.revision, job: response.job!, entry })), nextPageToken: response.nextPageToken };
  }, [source]);
  const reader = useConnectPaginationReader(NativeModelQuery.listNativeModels, request, project);
  const chain = usePaginationChain(`native-models:${source}`, active, reader);
  usePaginationRefresh(NativeModelQuery.listNativeModels, request(""), active, chain.refresh);
  return chain;
}
