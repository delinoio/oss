// SPDX-License-Identifier: Apache-2.0
import { useCallback, useMemo, useRef } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { EntityKind, ResourceQuery, ResourceSchema, ErrorDetailSchema, FailureCode, type ClientFailure, type Resource } from "@delinoio/delidev-api-client";
import { create } from "@bufbuild/protobuf";
import { document as resourceDocument, encode, object } from "./documents";
import { useConnectPaginationReader, usePaginationChain, usePaginationRefresh } from "./scroll-pagination-query";

// Observe the actual containing scroller, including task/dialog and sidebar
// bodies. The wrapper does not add a second scrolling region.
export function useGitHubScrollRoot() {
  const root = useRef<HTMLElement | null>(null);
  // The shared observer resolves overflow ownership at read time, so portals
  // and compact reflow can move this content without changing its chain.
  const bindRoot = useCallback((node: HTMLElement | null) => { root.current = node; }, []);
  return { root, bindRoot };
}
export function invalidGitHubPage(): never { throw new ConnectError("The retained page could not be verified.", Code.FailedPrecondition, undefined, [{ desc: ErrorDetailSchema, value: { code: FailureCode.CursorExpired, guidance: "Reload this list to accept a new chain." } }]); }
export function resourceProjection(resource: Resource) { return { id: resource.id, revision: resource.revision }; }

export function visiblePageIds<T extends { id: string; revision: bigint }>(pages: { rows: T[] }[], rows: T[]) {
  const owners = new Map<string, T>();
  for (const page of pages) for (const row of page.rows) {
    const owner = owners.get(row.id);
    if (!owner) owners.set(row.id, row);
  }
  return new Set(rows.filter(row => owners.get(row.id) === row).map(row => row.id));
}

// Catalog projections contain display/selection metadata only. Operations fetch
// the selected authoritative record independently of the accepted page chain.
export function useGitHubCatalog(kind: EntityKind, active: boolean) {
  const request = useCallback((token: string) => ({ filter: { kind, pageSize: 50, pageToken: token } }), [kind]);
  const project = useCallback((reply: { resources: Resource[]; nextPageToken: string }) => {
    if (reply.resources.length > 50 || new Set(reply.resources.map(row => row.id)).size !== reply.resources.length || reply.resources.some(row => row.kind !== kind || !row.id)) invalidGitHubPage();
    const rows = reply.resources.map(row => {
      const data = resourceDocument(row);
      const display = kind === EntityKind.INTEGRATION ? { name: data.name, provider: data.provider, connection: data.connection ? { generation_id: object(data.connection).generation_id } : undefined, pending: Boolean(data.pending) } : { name: data.name, github_owner: data.github_owner, github_name: data.github_name };
      return { id: row.id, revision: row.revision, resource: create(ResourceSchema, { id: row.id, kind, revision: row.revision, schemaVersion: row.schemaVersion, documentJson: encode(display) }) };
    });
    return { rows, nextPageToken: reply.nextPageToken };
  }, [kind]);
  const reader = useConnectPaginationReader(ResourceQuery.listResources, request, project);
  const chain = usePaginationChain('github-catalog:' + kind, active, reader);
  usePaginationRefresh(ResourceQuery.listResources, request(""), active, chain.refresh);
  return { ...chain, data: chain.loaded ? { resources: chain.rows.map(row => row.resource), nextPageToken: chain.nextPageToken } : undefined, isFetching: Boolean(chain.loading), isPending: !chain.loaded && !chain.error, refetch: chain.error ? chain.error.stalled ? chain.reload : chain.retry : chain.refresh };
}

export function paginationError(failure?: ClientFailure) {
  return failure ? new ConnectError(failure.message, Code.Unknown, undefined, [{ desc: ErrorDetailSchema, value: { code: failure.code, guidance: failure.guidance, correlationId: failure.correlationId ?? "" } }]) : undefined;
}

export function useStablePageRevisions(scope: string) {
  const pages = useMemo(() => new Map<string, { id: string; revision: bigint }[]>(), [scope]);
  return useCallback((token: string, rows: { id: string; revision: bigint }[]) => {
    if (!token) pages.clear();
    for (const [otherToken, otherRows] of pages) if (otherToken !== token) for (const row of rows) {
      const previous = otherRows.find(other => other.id === row.id);
      if (previous && previous.revision !== row.revision) throw new ConnectError("The loaded list boundaries changed.", Code.FailedPrecondition, undefined, [{ desc: ErrorDetailSchema, value: { code: FailureCode.CursorExpired, guidance: "Reload this list to accept a new chain." } }]);
    }
    pages.set(token, rows);
  }, [pages]);
}
