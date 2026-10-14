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
  type SearchSessionResolution,
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
 * Closed classification of one Session-slot or Sidechat-slot read, relative to
 * the Session the route requested as parent (when there is one).
 */
export enum SessionReadKind {
  /** A Session that is not a Sidechat (work Session, General Chat, Fork). */
  TopLevel = "top-level",
  /** A Sidechat whose retained original parent is the requested parent. */
  SidechatOfRequestedParent = "sidechat-of-requested-parent",
  /** A Sidechat whose retained original parent is another Session. */
  SidechatOfOtherParent = "sidechat-of-other-parent",
  /**
   * A definitive rejection: deleted, denied, another connection's ID, an ID of
   * another entity kind, malformed or unknown provenance (missing workspace,
   * invalid Fork metadata) or an invalid envelope.
   */
  Rejected = "rejected",
  /** The read failed and may succeed on retry. */
  Transient = "transient",
}

/** What route restoration does for one row of the decision tables. */
export enum SessionRestoreAction {
  /** Open nothing and clear the selection from the URL. */
  Clear = "clear",
  /** Open the verified top-level Session only. */
  OpenSession = "open-session",
  /** Session slot only: the parent is verified, so read the Sidechat slot. */
  VerifySidechat = "verify-sidechat",
  /** Open the verified Sidechat under its verified parent. */
  OpenSidechat = "open-sidechat",
  /** Retryable parent failure, no Sidechat: open the Session unverified. */
  RetainSession = "retain-session",
  /**
   * Retryable failure: open the requested Sidechat under the requested parent
   * with a placeholder name; the pane verifies it or discards the placeholder.
   */
  RetainSidechat = "retain-sidechat",
}

/**
 * Decision table for the top-level `session` slot, one row per read. A Sidechat
 * is never a valid top-level selection, whatever parent it retains: that parent
 * was never read here, so trusting it would mount an unverified Session. Only a
 * Sidechat in the `sidechat` slot, under a parent verified as top-level, is
 * valid. `alone` applies without a `sidechat` selection, `withSidechat` with one.
 *
 * | Session-slot read           | alone          | with sidechat     |
 * | --------------------------- | -------------- | ----------------- |
 * | TopLevel                    | OpenSession    | VerifySidechat    |
 * | SidechatOfRequestedParent   | Clear          | Clear             |
 * | SidechatOfOtherParent       | Clear          | Clear             |
 * | Rejected                    | Clear          | Clear             |
 * | Transient                   | RetainSession  | RetainSidechat    |
 */
export const sessionSlotTable: Readonly<
  Record<
    SessionReadKind,
    {
      alone: SessionRestoreAction;
      withSidechat: SessionRestoreAction;
    }
  >
> = {
  [SessionReadKind.TopLevel]: {
    alone: SessionRestoreAction.OpenSession,
    withSidechat: SessionRestoreAction.VerifySidechat,
  },
  [SessionReadKind.SidechatOfRequestedParent]: {
    alone: SessionRestoreAction.Clear,
    withSidechat: SessionRestoreAction.Clear,
  },
  [SessionReadKind.SidechatOfOtherParent]: {
    alone: SessionRestoreAction.Clear,
    withSidechat: SessionRestoreAction.Clear,
  },
  [SessionReadKind.Rejected]: {
    alone: SessionRestoreAction.Clear,
    withSidechat: SessionRestoreAction.Clear,
  },
  [SessionReadKind.Transient]: {
    alone: SessionRestoreAction.RetainSession,
    withSidechat: SessionRestoreAction.RetainSidechat,
  },
};

/**
 * Decision table for the `sidechat` slot. It is consulted only after the parent
 * was read as a top-level Session (the parent rows above decide every other
 * parent outcome, and the child is not read then). Anything that is not
 * positively a Sidechat of the requested parent falls back to the verified
 * parent; a retryable failure retains the requested Sidechat.
 *
 * | Sidechat-slot read          | action          |
 * | --------------------------- | --------------- |
 * | TopLevel                    | OpenSession     |
 * | SidechatOfRequestedParent   | OpenSidechat    |
 * | SidechatOfOtherParent       | OpenSession     |
 * | Rejected                    | OpenSession     |
 * | Transient                   | RetainSidechat  |
 */
export const sidechatSlotTable: Readonly<
  Record<SessionReadKind, SessionRestoreAction>
> = {
  [SessionReadKind.TopLevel]: SessionRestoreAction.OpenSession,
  [SessionReadKind.SidechatOfRequestedParent]:
    SessionRestoreAction.OpenSidechat,
  [SessionReadKind.SidechatOfOtherParent]: SessionRestoreAction.OpenSession,
  [SessionReadKind.Rejected]: SessionRestoreAction.OpenSession,
  [SessionReadKind.Transient]: SessionRestoreAction.RetainSidechat,
};

/**
 * Classifies one Session read relative to the requested parent. Without a
 * requested parent (the `session` slot) every Sidechat is of another parent.
 */
export function classifySessionRead(
  resolution: SearchSessionResolution,
  requestedParent?: string,
): SessionReadKind {
  switch (resolution.route) {
    case SearchSessionRoute.TopLevel:
      return SessionReadKind.TopLevel;
    case SearchSessionRoute.Sidechat:
      return requestedParent !== undefined &&
        resolution.parent === requestedParent
        ? SessionReadKind.SidechatOfRequestedParent
        : SessionReadKind.SidechatOfOtherParent;
    case SearchSessionRoute.Unavailable:
      return resolution.transient
        ? SessionReadKind.Transient
        : SessionReadKind.Rejected;
  }
}

/**
 * Verifies a Session selection and its Sidechat with fresh reads, strictly from
 * `sessionSlotTable` and `sidechatSlotTable`: a definitive rejection of the
 * parent, or a Sidechat in the top-level slot, clears the selection; a
 * Sidechat that is not positively a child of the verified parent falls back to
 * that parent; a transient failure of either read retains the deepest requested
 * selection so it is never lost to a temporary outage.
 */
export async function readRouteSession(
  client: QueryClient,
  transport: Transport,
  selection: { id: string; sidechat?: string },
  signal?: AbortSignal,
): Promise<RouteSessionRead> {
  const parentRead = classifySessionRead(
    await resolveSearchSession(client, transport, selection.id, signal),
  );
  const parentRow = sessionSlotTable[parentRead];
  let action = selection.sidechat ? parentRow.withSidechat : parentRow.alone;
  let child: SearchSessionResolution | undefined;
  if (action === SessionRestoreAction.VerifySidechat) {
    if (signal?.aborted) action = SessionRestoreAction.OpenSession;
    else {
      child = await resolveSearchSession(
        client,
        transport,
        selection.sidechat!,
        signal,
      );
      action = sidechatSlotTable[classifySessionRead(child, selection.id)];
    }
  }
  switch (action) {
    case SessionRestoreAction.Clear:
      return missing;
    case SessionRestoreAction.OpenSession:
      return {
        outcome: RouteReadOutcome.Found,
        open: { id: selection.id },
      };
    case SessionRestoreAction.RetainSession:
      return {
        outcome: RouteReadOutcome.Failed,
        open: { id: selection.id },
      };
    case SessionRestoreAction.RetainSidechat:
      return {
        outcome: RouteReadOutcome.Failed,
        open: { id: selection.sidechat!, parent: selection.id },
      };
    case SessionRestoreAction.OpenSidechat:
      return {
        outcome: RouteReadOutcome.Found,
        open: {
          id: selection.sidechat!,
          parent: selection.id,
          name:
            child?.route === SearchSessionRoute.Sidechat
              ? child.name
              : undefined,
        },
      };
    case SessionRestoreAction.VerifySidechat:
      // Unreachable: resolved above. Fail closed rather than trust anything.
      return missing;
  }
}
