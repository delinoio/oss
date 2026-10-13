import {
  createQueryOptions,
} from "@connectrpc/connect-query";
import type { Transport } from "@connectrpc/connect";
import type { QueryClient } from "@tanstack/react-query";
import {
  EntityKind,
  ResourceQuery,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { document, resourceName } from "./documents";
import { ConversationKind, conversationProvenance } from "./home-navigation";

/** Closed outcome of resolving which host a Search result opens under. */
export enum SearchSessionRoute {
  /** Ordinary sessions and independent Forks open as top-level sessions. */
  TopLevel = "top-level",
  /** A Sidechat child opens under its exact original parent. */
  Sidechat = "sidechat",
  /** Read failed, or the metadata is missing or malformed. */
  Unavailable = "unavailable",
}

const unavailable = (id: string, reason: string): SearchSessionResolution => {
  // Structured, content-free diagnostics: only the opaque session ID and reason.
  console.warn("delidev.search.session_route_unavailable", {
    sessionId: id,
    reason,
  });
  return { route: SearchSessionRoute.Unavailable };
};

/** Presentation state of one pending or failed Search-result opening. */
export enum SearchOpenStatus {
  Pending = "pending",
  Unavailable = "unavailable",
}

export type SearchSessionResolution =
  | { route: SearchSessionRoute.TopLevel }
  | { route: SearchSessionRoute.Sidechat; parent: string; name: string }
  | { route: SearchSessionRoute.Unavailable };

/**
 * Reads the exact target through the existing bounded GetResource operation and
 * validates its retained Fork/Sidechat parent metadata. Presentation only: the
 * result grants no mutation or native authority and is never cached.
 */
export async function resolveSearchSession(
  client: QueryClient,
  transport: Transport,
  id: string,
  signal?: AbortSignal,
): Promise<SearchSessionResolution> {
  const options = createQueryOptions(
    ResourceQuery.getResource,
    { kind: EntityKind.SESSION, id },
    { transport },
  );
  // A unique key keeps the metadata read out of ordinary Resource consumers.
  const queryKey = [...options.queryKey, { searchRoute: newRequestId() }];
  const abort = () => {
    void client.cancelQueries({ queryKey, exact: true });
  };
  signal?.addEventListener("abort", abort, { once: true });
  try {
    const response = await client.fetchQuery({
      queryKey,
      retry: false,
      gcTime: 0,
      staleTime: 0,
      queryFn: (context) => options.queryFn({ ...context, queryKey: options.queryKey }),
    });
    const resource = response.resource;
    if (!resource || resource.id !== id || resource.kind !== EntityKind.SESSION)
      return unavailable(id, "missing-or-mismatched-resource");
    const provenance = conversationProvenance(resource, document(resource));
    switch (provenance.conversationKind) {
      case ConversationKind.Sidechat:
        return provenance.sidechatParent
          ? {
              route: SearchSessionRoute.Sidechat,
              parent: provenance.sidechatParent,
              name: resourceName(resource),
            }
          : unavailable(id, "missing-sidechat-parent");
      case ConversationKind.Fork:
      case ConversationKind.GeneralChat:
      case ConversationKind.WorkSession:
        return { route: SearchSessionRoute.TopLevel };
      default:
        return unavailable(id, "malformed-parent-metadata");
    }
  } catch {
    return unavailable(id, "read-failed");
  } finally {
    signal?.removeEventListener("abort", abort);
  }
}
