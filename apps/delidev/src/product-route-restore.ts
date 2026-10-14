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
import { inaccessibleFailure } from "./search-session-routing";

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
      response.resource.kind === EntityKind.REPOSITORY
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
        schedule.revision > 0n &&
        supportsResourceSchema(schedule)
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
      response.view && currentInboxSource(response.view, id) ? id : undefined,
    signal,
  );
}
