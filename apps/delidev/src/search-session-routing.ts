import { createQueryOptions } from "@connectrpc/connect-query";
import type { Transport } from "@connectrpc/connect";
import type { QueryClient } from "@tanstack/react-query";
import {
  EntityKind,
  FailureCode,
  ResourceQuery,
  clientFailure,
  newRequestId,
  supportsResourceSchema,
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

const unavailable = (
  id: string,
  reason: string,
  transient = false,
): SearchSessionResolution => {
  // Structured, content-free diagnostics: only the opaque session ID and reason.
  console.warn("delidev.search.session_route_unavailable", {
    sessionId: id,
    reason,
  });
  return { route: SearchSessionRoute.Unavailable, transient };
};

/** How route restoration treats one failed read of a route selection. */
export enum RouteFailureDisposition {
  /**
   * The server definitively rejected the selection (missing, denied, or an ID
   * that is not a valid selection of this kind): clear it to the requested
   * screen's list.
   */
  Clear = "clear",
  /**
   * The read may succeed on retry or says nothing about the selection: keep it
   * so the mounted screen's own retry and error state applies.
   */
  Retain = "retain",
}

/**
 * The single classification of every failure code a route-restoration read can
 * return (GetResource, GetSchedule and GetInboxEntry), shared by every restored
 * kind: Session, Sidechat, Inbox entry, Schedule and repository. The Settings
 * category and Usage tab need no read. Typed as an exhaustive record so a new
 * code cannot ship without an explicit decision.
 *
 * Transport-level outcomes arrive through `clientFailure`: gRPC
 * `failed_precondition` is `RecoveryRequired`; `deadline_exceeded`, `internal`
 * and unmapped transport errors are `ServerUnavailable`/`Internal`; a cancelled
 * read is `Canceled`.
 */
export const routeFailureDispositions: Readonly<
  Record<FailureCode, RouteFailureDisposition>
> = {
  // A syntactically valid ID of another entity kind (for example a Schedule ID
  // supplied as a repository) is rejected as invalid, never retryable.
  [FailureCode.InvalidArgument]: RouteFailureDisposition.Clear,
  [FailureCode.NotFound]: RouteFailureDisposition.Clear,
  [FailureCode.Unauthenticated]: RouteFailureDisposition.Clear,
  [FailureCode.PermissionDenied]: RouteFailureDisposition.Clear,
  [FailureCode.Conflict]: RouteFailureDisposition.Retain,
  [FailureCode.Unavailable]: RouteFailureDisposition.Retain,
  [FailureCode.ServerUnavailable]: RouteFailureDisposition.Retain,
  [FailureCode.ConfirmationRequired]: RouteFailureDisposition.Retain,
  [FailureCode.MissingInput]: RouteFailureDisposition.Retain,
  [FailureCode.Unsupported]: RouteFailureDisposition.Retain,
  [FailureCode.RecoveryRequired]: RouteFailureDisposition.Retain,
  [FailureCode.BudgetReached]: RouteFailureDisposition.Retain,
  [FailureCode.ResourceExhausted]: RouteFailureDisposition.Retain,
  [FailureCode.CursorExpired]: RouteFailureDisposition.Retain,
  [FailureCode.ProviderDisabled]: RouteFailureDisposition.Retain,
  [FailureCode.Canceled]: RouteFailureDisposition.Retain,
  [FailureCode.Internal]: RouteFailureDisposition.Retain,
};

/** Classifies one failed route-restoration read through the shared table. */
export function routeFailureDisposition(
  error: unknown,
): RouteFailureDisposition {
  return routeFailureDispositions[clientFailure(error).code];
}

/** A definitive rejection of the selection, as opposed to a retryable failure. */
export function inaccessibleFailure(error: unknown): boolean {
  return routeFailureDisposition(error) === RouteFailureDisposition.Clear;
}

/** Presentation state of one pending or failed Search-result opening. */
export enum SearchOpenStatus {
  Pending = "pending",
  Unavailable = "unavailable",
}

export type SearchSessionResolution =
  | { route: SearchSessionRoute.TopLevel }
  | { route: SearchSessionRoute.Sidechat; parent: string; name: string }
  /**
   * `transient` marks a read failure that may succeed on retry. It is false for
   * a missing, denied or malformed target, which is definitively unavailable.
   */
  | { route: SearchSessionRoute.Unavailable; transient?: boolean };

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
      queryFn: (context) =>
        options.queryFn({ ...context, queryKey: options.queryKey }),
    });
    const resource = response.resource;
    if (!resource || resource.id !== id || resource.kind !== EntityKind.SESSION)
      return unavailable(id, "missing-or-mismatched-resource");
    // Apply the same envelope checks as the mounted Session view's
    // synchronization boundary before trusting any Fork/Sidechat metadata.
    if (
      resource.sessionId !== id ||
      typeof resource.revision !== "bigint" ||
      resource.revision <= 0n ||
      !supportsResourceSchema(resource)
    )
      return unavailable(id, "invalid-resource-envelope");
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
  } catch (error) {
    // A cancelled read belongs to a superseded opening: nothing to report.
    if (signal?.aborted)
      return { route: SearchSessionRoute.Unavailable, transient: true };
    return unavailable(id, "read-failed", !inaccessibleFailure(error));
  } finally {
    signal?.removeEventListener("abort", abort);
  }
}
