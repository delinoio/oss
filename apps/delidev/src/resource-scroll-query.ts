// SPDX-License-Identifier: Apache-2.0
import { useCallback } from "react";
import { AccountTypeFilter, EntityKind, ResourceQuery, type Resource } from "@delinoio/delidev-api-client";
import { resourceName } from "./documents";
import { useConnectPaginationReader, usePaginationChain, usePaginationRefresh } from "./scroll-pagination-query";

/** Inventories retain only display metadata outside the bounded payload window.
 * Editors and mutations continue to use original full revision-bearing resources. */
export function useResourceScrollQuery(kind: EntityKind, active: boolean, scope = "", interval: number | false = false, accountType = AccountTypeFilter.UNSPECIFIED, providerId = "", validate?: (row: Resource) => boolean, retainPayloadOnSuspend = false) {
  const request = useCallback((token: string) => ({ filter: { kind, pageSize: 50, pageToken: token }, accountType, providerId }), [kind, accountType, providerId]);
  const project = useCallback((response: { resources: import("@delinoio/delidev-api-client").Resource[]; nextPageToken: string }, token: string) => {
    if (response.resources.length > 50 || new Set(response.resources.map(row => row.id)).size !== response.resources.length || response.resources.some(row => !row.id || row.kind !== kind || row.revision <= 0n || validate && !validate(row))) throw new Error("Invalid resource inventory page");
    return { token, nextPageToken: response.nextPageToken, rows: response.resources.map(row => ({ id: row.id, revision: row.revision, name: resourceName(row) })), payload: response.resources };
  }, [kind, validate]);
  const reader = useConnectPaginationReader(ResourceQuery.listResources, request, project);
  const query = usePaginationChain(`resource-inventory:${kind}:${scope}:${accountType}:${providerId}`, active, reader, true, retainPayloadOnSuspend);
  usePaginationRefresh(ResourceQuery.listResources, request(""), active, query.refresh, interval);
  return query;
}
