// SPDX-License-Identifier: Apache-2.0
import { useCallback, useMemo } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { AccountTypeFilter, ApiProtocol, EntityKind, ProviderQuery, ResourceQuery, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, resourceName, text } from "./documents";
import { SourceKind, modelSource, sameSource, sourceKey, wireService, type Source } from "./worker-source";
import { useConnectPaginationReader, usePaginationChain, usePaginationRefresh } from "./scroll-pagination-query";

function validate(rows: Resource[], kind: EntityKind, source?: Source) {
  if (rows.length > 50 || new Set(rows.map(row => row.id)).size !== rows.length || rows.some(row => !row.id || row.revision < 1n || row.kind !== kind || (kind === EntityKind.ACCOUNT ? !sameSource(row, source) : !supportsResourceSchema(row) || sourceKey(modelSource(row)) !== sourceKey(source)))) throw new ConnectError("The selected source inventory is unavailable.", Code.DataLoss);
}
export function useWizardAccounts(source: Source | undefined, protocol: ApiProtocol, active: boolean) {
  const request = useCallback((token: string) => ({ filter: { kind: EntityKind.ACCOUNT, pageSize: 50, pageToken: token }, providerId: source?.kind === SourceKind.Api ? source.id : "", accountType: source?.kind === SourceKind.Api ? AccountTypeFilter.API : AccountTypeFilter.SUBSCRIPTION, subscriptionService: wireService(source), apiProtocol: protocol }), [source, protocol]);
  const project = useCallback((response: { resources: Resource[]; nextPageToken: string }) => { validate(response.resources, EntityKind.ACCOUNT, source); return { rows: response.resources.map(({ id, revision }) => ({ id, revision })), payload: response.resources, nextPageToken: response.nextPageToken }; }, [source]);
  const reader = useConnectPaginationReader(ResourceQuery.listResources, request, project);
  const chain = usePaginationChain(`wizard-accounts:${sourceKey(source)}:${protocol}`, active, reader);
  usePaginationRefresh(ResourceQuery.listResources, request(""), active, chain.refresh);
  const data = useMemo(() => chain.loaded ? { resources: chain.payloadPages.flatMap(page => page.payload), nextPageToken: chain.nextPageToken } : undefined, [chain.loaded, chain.payloadPages, chain.nextPageToken]);
  return { ...chain, data, isLoading: !chain.loaded && !chain.error, isFetching: Boolean(chain.loading), refetch: chain.refresh };
}
export function useWizardModels(query: string, source: Source | undefined, active: boolean) {
  const request = useCallback((token: string) => ({ query, providerId: source?.kind === SourceKind.Api ? source.id : "", subscriptionService: wireService(source), includeHidden: true, enabledProvidersOnly: true, pageSize: 50, pageToken: token }), [query, source]);
  const project = useCallback((response: { models: Resource[]; nextPageToken: string }) => { validate(response.models, EntityKind.MODEL, source); return { rows: response.models.map(row => ({ id: row.id, revision: row.revision, nativeId: text(document(row).native_id), name: resourceName(row), hidden: document(row).hidden === true })), payload: response.models, nextPageToken: response.nextPageToken }; }, [source]);
  const reader = useConnectPaginationReader(ProviderQuery.searchModels, request, project);
  const chain = usePaginationChain(`wizard-models:${sourceKey(source)}:${query}`, active, reader);
  usePaginationRefresh(ProviderQuery.searchModels, request(""), active, chain.refresh);
  const data = useMemo(() => chain.loaded ? { models: chain.payloadPages.flatMap(page => page.payload), nextPageToken: chain.nextPageToken } : undefined, [chain.loaded, chain.payloadPages, chain.nextPageToken]);
  return { ...chain, data, isLoading: !chain.loaded && !chain.error, isFetching: Boolean(chain.loading), refetch: chain.refresh };
}
