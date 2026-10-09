// SPDX-License-Identifier: Apache-2.0
import { useCallback, useMemo } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { ErrorDetailSchema, FailureCode, AccountTypeFilter, ApiProtocol, EntityKind, ResourceQuery, type Resource } from "@delinoio/delidev-api-client";
import { SourceKind, sameSource, sourceKey, wireService, type Source } from "./worker-source";
import { useConnectPaginationReader, usePaginationChain, usePaginationRefresh } from "./scroll-pagination-query";

function validate(rows: Resource[], kind: EntityKind, source?: Source) {
  if (rows.length > 50 || new Set(rows.map(row => row.id)).size !== rows.length || rows.some(row => !row.id || row.revision < 1n || row.kind !== kind || !sameSource(row, source))) throw new ConnectError("The selected source inventory is unavailable.", Code.DataLoss, undefined, [{ desc: ErrorDetailSchema, value: { code: FailureCode.Internal } }]);
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
