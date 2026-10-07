// SPDX-License-Identifier: Apache-2.0
import { useCallback } from "react";
import { ConnectError, Code } from "@connectrpc/connect";
import { EntityKind, ResourceQuery, SessionQuery, type Resource } from "@delinoio/delidev-api-client";
import { useConnectPaginationReader, usePaginationChain, usePaginationRefresh } from "./scroll-pagination-query";

export function validateConversationPage(rows: readonly Resource[], kind: EntityKind, sessionId: string): void {
  const ids = new Set<string>();
  for (const row of rows) {
    if (!row.id || row.revision < 1n || row.kind !== kind || row.sessionId !== sessionId || ids.has(row.id)) throw new ConnectError("The conversation page is unavailable.", Code.DataLoss);
    ids.add(row.id);
  }
}

/** Pagination retains only identities/revisions outside its three payload pages.
 * Generated query caches are request-local and never retain evicted documents. */
export function useConversationPages(kind: EntityKind, sessionId: string, active = true, pageSize = 50, validate?: (rows: Resource[]) => void, interval?: number) {
  const request = useCallback((token: string) => ({ filter: { kind, sessionId, pageSize, pageToken: token } }), [kind, sessionId, pageSize]);
  const queueRequest = useCallback((token: string) => ({ sessionId, pageSize, pageToken: token }), [sessionId, pageSize]);
  const project = useCallback((rows: Resource[], nextPageToken: string) => {
    if (rows.length > pageSize) throw new ConnectError("The conversation page exceeds its requested bound.", Code.DataLoss);
    validateConversationPage(rows, kind, sessionId);
    validate?.(rows);
    return { rows: rows.map(({ id, revision }) => ({ id, revision })), payload: rows, nextPageToken };
  }, [kind, sessionId, pageSize, validate]);
  const resources = useCallback((response: { resources: Resource[]; nextPageToken: string }) => project(response.resources, response.nextPageToken), [project]);
  const inputs = useCallback((response: { inputs: Resource[]; nextPageToken: string }) => project(response.inputs, response.nextPageToken), [project]);
  const resourceReader = useConnectPaginationReader(ResourceQuery.listResources, request, resources);
  const queueReader = useConnectPaginationReader(SessionQuery.listQueue, queueRequest, inputs);
  const query = usePaginationChain(`${kind}:${sessionId}`, active, kind === EntityKind.QUEUE ? queueReader : resourceReader);
  usePaginationRefresh(ResourceQuery.listResources, request(""), active && kind !== EntityKind.QUEUE, query.refresh, interval ?? false);
  usePaginationRefresh(SessionQuery.listQueue, queueRequest(""), active && kind === EntityKind.QUEUE, query.refresh, interval ?? false);
  return { ...query, data: query.loaded ? { resources: query.payloadPages.flatMap(page => page.payload), inputs: query.payloadPages.flatMap(page => page.payload), nextPageToken: query.nextPageToken } : undefined, isPending: !query.loaded && !query.error, isFetching: Boolean(query.loading), refetch: query.refreshExplicit };
}
