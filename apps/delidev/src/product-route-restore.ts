// SPDX-License-Identifier: Apache-2.0
import { createQueryOptions } from "@connectrpc/connect-query";
import type { Transport } from "@connectrpc/connect";
import type { QueryClient } from "@tanstack/react-query";
import {
  EntityKind,
  InboxQuery,
  ResourceQuery,
  ScheduleQuery,
  newRequestId,
  supportsResourceSchema,
  type Resource,
} from "@delinoio/delidev-api-client";
import { currentInboxSource } from "./inbox-source";
import {
  SearchSessionRoute,
  inaccessibleFailure,
  resolveSearchSession,
} from "./search-session-routing";

/** Closed outcome of verifying one route selection with a fresh read. */
export enum RouteReadOutcome {
  /** The selection exists on this connection and was read just now. */
  Found = "found",
  /** Deleted, denied, malformed or owned by another connection. */
  Missing = "missing",
  /** The read failed and may succeed on retry. */
  Failed = "failed",
}

export type RouteRead<T> =
  | { outcome: RouteReadOutcome.Found; value: T }
  | { outcome: RouteReadOutcome.Missing | RouteReadOutcome.Failed };

/**
 * Applies the envelope checks every mounted consumer applies before trusting a
 * resource: a positive revision and a supported schema version. A response that
 * fails them does not describe a restorable selection.
 */
function validEnvelope(resource: Resource): boolean {
  return (
    typeof resource.revision === "bigint" &&
    resource.revision > 0n &&
    supportsResourceSchema(resource)
  );
}

const missing = { outcome: RouteReadOutcome.Missing } as const;
const failed = { outcome: RouteReadOutcome.Failed } as const;

interface FreshRead<Response> {
  queryKey: readonly unknown[];
  queryFn: (context: never) => Response | Promise<Response>;
}

/**
 * Runs one read through a unique, never-cached query key so route verification
 * cannot serve or seed ordinary consumers and always reflects a fresh server
 * answer. `select` returns undefined for a response that does not describe the
 * requested selection.
 */
async function readFresh<Response, Value>(
  client: QueryClient,
  options: FreshRead<Response>,
  select: (response: Response) => Value | undefined,
  signal?: AbortSignal,
): Promise<RouteRead<Value>> {
  const queryKey = [...options.queryKey, { productRoute: newRequestId() }];
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
        (options.queryFn as (context: unknown) => Response | Promise<Response>)(
          {
            ...(context as object),
            queryKey: options.queryKey,
          },
        ),
    });
    const value = select(response);
    return value === undefined
      ? missing
      : { outcome: RouteReadOutcome.Found, value };
  } catch (error) {
    if (signal?.aborted) return failed;
    return inaccessibleFailure(error) ? missing : failed;
  } finally {
    signal?.removeEventListener("abort", abort);
  }
}

/** Verifies one Repository through the bounded GetResource operation. */
export function readRouteRepository(
  client: QueryClient,
  transport: Transport,
  id: string,
  signal?: AbortSignal,
): Promise<RouteRead<string>> {
  return readFresh(
    client,
    createQueryOptions(
      ResourceQuery.getResource,
      { kind: EntityKind.REPOSITORY, id },
      { transport },
    ),
    (response) =>
      response.resource?.id === id &&
      response.resource.kind === EntityKind.REPOSITORY &&
      validEnvelope(response.resource)
        ? id
        : undefined,
    signal,
  );
}

/** Reads one Schedule through the existing GetSchedule operation. */
export function readRouteSchedule(
  client: QueryClient,
  transport: Transport,
  id: string,
  signal?: AbortSignal,
): Promise<RouteRead<Resource>> {
  return readFresh(
    client,
    createQueryOptions(ScheduleQuery.getSchedule, { id }, { transport }),
    (response) => {
      const schedule = response.schedule;
      return schedule &&
        schedule.id === id &&
        schedule.kind === EntityKind.SCHEDULE &&
        validEnvelope(schedule)
        ? schedule
        : undefined;
    },
    signal,
  );
}

/** Verifies one Inbox entry through the existing GetInboxEntry read. */
export function readRouteInboxEntry(
  client: QueryClient,
  transport: Transport,
  id: string,
  signal?: AbortSignal,
): Promise<RouteRead<string>> {
  return readFresh(
    client,
    createQueryOptions(InboxQuery.getInboxEntry, { id }, { transport }),
    (response) =>
      response.view?.entry &&
      validEnvelope(response.view.entry) &&
      currentInboxSource(response.view, id)
        ? id
        : undefined,
    signal,
  );
}

/**
 * How the Shell opens a verified Session selection: the ID to show and, for a
 * Sidechat, its original parent and display name (when known).
 */
export interface RouteSessionOpen {
  id: string;
  parent?: string;
  name?: string;
}

/**
 * Result of verifying a Session selection and its optional Sidechat. `Missing`
 * opens nothing. `Found` and `Failed` both carry what to open: a `Failed` read
 * is retryable, so the selection is still opened (the mounted view owns its
 * retry and error state) instead of being dropped from the URL.
 */
export type RouteSessionRead =
  | { outcome: RouteReadOutcome.Missing }
  | {
      outcome: RouteReadOutcome.Found | RouteReadOutcome.Failed;
      open: RouteSessionOpen;
    };

/**
 * Verifies a Session selection and its Sidechat with fresh reads and
 * classifies every nested read in one place: a definitive rejection of the
 * parent clears the selection, a definitive rejection of the child falls back
 * to the verified parent, and a transient failure of either read retains the
 * deepest requested selection so it is never lost to a temporary outage.
 */
export async function readRouteSession(
  client: QueryClient,
  transport: Transport,
  selection: { id: string; sidechat?: string },
  signal?: AbortSignal,
): Promise<RouteSessionRead> {
  const top = await resolveSearchSession(
    client,
    transport,
    selection.id,
    signal,
  );
  if (top.route === SearchSessionRoute.Sidechat)
    return {
      outcome: RouteReadOutcome.Found,
      open: { id: selection.id, parent: top.parent, name: top.name },
    };
  if (top.route === SearchSessionRoute.Unavailable) {
    if (!top.transient) return missing;
    // The parent could not be verified yet, so neither could its Sidechat:
    // retain both and let the mounted views own the retry and error state.
    return {
      outcome: RouteReadOutcome.Failed,
      open: selection.sidechat
        ? { id: selection.sidechat, parent: selection.id }
        : { id: selection.id },
    };
  }
  if (!selection.sidechat || signal?.aborted)
    return { outcome: RouteReadOutcome.Found, open: { id: selection.id } };
  const child = await resolveSearchSession(
    client,
    transport,
    selection.sidechat,
    signal,
  );
  if (
    child.route === SearchSessionRoute.Sidechat &&
    child.parent === selection.id
  )
    return {
      outcome: RouteReadOutcome.Found,
      open: { id: selection.sidechat, parent: child.parent, name: child.name },
    };
  if (child.route === SearchSessionRoute.Unavailable && child.transient)
    return {
      outcome: RouteReadOutcome.Failed,
      open: { id: selection.sidechat, parent: selection.id },
    };
  // Definitively not a Sidechat of this Session: open the verified parent only.
  return { outcome: RouteReadOutcome.Found, open: { id: selection.id } };
}
