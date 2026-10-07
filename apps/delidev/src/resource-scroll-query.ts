// SPDX-License-Identifier: Apache-2.0
import { useCallback } from "react";
import { EntityKind, ResourceQuery } from "@delinoio/delidev-api-client";
import { resourceName } from "./documents";
import { useConnectPaginationReader, usePaginationChain, usePaginationRefresh } from "./scroll-pagination-query";

/** Inventories retain only display metadata outside the bounded payload window.
 * Editors and mutations continue to use original full revision-bearing resources. */
export function useResourceScrollQuery(kind: EntityKind, active: boolean, scope = "", interval: number | false = false) {
  const request = useCallback((token: string) => ({ filter: { kind, pageSize: 50, pageToken: token } }), [kind]);
  const project = useCallback((response: { resources: import("@delinoio/delidev-api-client").Resource[]; nextPageToken: string }, token: string) => {
    if (response.resources.some(row => !row.id || row.kind !== kind || row.revision <= 0n)) throw new Error("Invalid resource inventory page");
    return { token, nextPageToken: response.nextPageToken, rows: response.resources.map(row => ({ id: row.id, revision: row.revision, name: resourceName(row) })), payload: response.resources };
  }, [kind]);
  const reader = useConnectPaginationReader(ResourceQuery.listResources, request, project);
  const query = usePaginationChain(`resource-inventory:${kind}:${scope}`, active, reader);
  usePaginationRefresh(ResourceQuery.listResources, request(""), active, query.refresh, interval);
  return query;
}
