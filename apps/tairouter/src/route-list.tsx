// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef, useState, type ReactNode } from "react";
import {
  RoutingStrategy,
  type Route,
  type RouteTarget,
} from "@tairouter/client";
import { t as translate, useLocale, enumLabel } from "./i18n";
import { Badge, Button, Card } from "./ui";

/** Outcome of resolving one dependency ID against a complete enumeration query. */
export enum DependencyStatus {
  Found = "found",
  Loading = "loading",
  Failed = "failed",
  Missing = "missing",
}

/** Kind of dependency a route target references, derived from the protobuf oneof. */
export enum DependencyKind {
  Connection = "connection",
  Pool = "pool",
  Unavailable = "unavailable",
}

export type DependencyLookup = {
  status: DependencyStatus;
  /** Known name; present for Found, and for Failed when same-scope data was loaded earlier. */
  name?: string;
  /** True when the name comes from data whose latest refresh failed. */
  stale?: boolean;
};

/** Structural subset of a React Query result for a complete personal enumeration. */
export type DependencyQuery = {
  data?: readonly { id: string; name: string }[];
  isPending: boolean;
  isError: boolean;
  isFetching: boolean;
  refetch: () => unknown;
};

/**
 * Maps an enumeration query and an ID to one explicit state. Only a successfully completed
 * enumeration may report a dependency as missing; pending and failed queries never do.
 */
export function resolveDependency(
  query: Pick<DependencyQuery, "data" | "isPending" | "isError">,
  id: string,
): DependencyLookup {
  const known = query.data?.find((item) => item.id === id);
  if (query.isError)
    return {
      status: DependencyStatus.Failed,
      name: known?.name,
      stale: !!known,
    };
  if (query.data)
    return known
      ? { status: DependencyStatus.Found, name: known.name }
      : { status: DependencyStatus.Missing };
  return { status: DependencyStatus.Loading };
}

export function dependencyKind(target: RouteTarget): DependencyKind {
  switch (target.target.case) {
    case "connectionId":
      return DependencyKind.Connection;
    case "poolId":
      return DependencyKind.Pool;
    default:
      return DependencyKind.Unavailable;
  }
}

/** Saved strategy as display text. Unspecified and unrecognized values never read as a supported strategy. */
export function strategyLabel(strategy: number) {
  return strategy === RoutingStrategy.JEV
    ? translate("routeStrategyJev")
    : enumLabel(RoutingStrategy[strategy]);
}

const dependencyCopy = {
  [DependencyKind.Connection]: {
    loading: () => translate("loadingConnectionName"),
    failed: () => translate("connectionDetailsUnavailable"),
    missing: () => translate("unavailableConnection"),
    retry: () => translate("tryAgainLoadingConnections"),
    type: () => translate("routeTargetConnectionType"),
  },
  [DependencyKind.Pool]: {
    loading: () => translate("loadingPoolName"),
    failed: () => translate("poolDetailsUnavailable"),
    missing: () => translate("message_071b530243"),
    retry: () => translate("tryAgainLoadingPools"),
    type: () => translate("routeTargetPoolType"),
  },
};

function TargetItem({
  target,
  ordinal,
  connections,
  pools,
}: {
  target: RouteTarget;
  ordinal: number;
  connections: DependencyQuery;
  pools: DependencyQuery;
}) {
  const kind = dependencyKind(target);
  const query = kind === DependencyKind.Pool ? pools : connections;
  const lookup =
    kind === DependencyKind.Unavailable
      ? undefined
      : resolveDependency(query, String(target.target.value));
  const copy =
    kind === DependencyKind.Unavailable ? undefined : dependencyCopy[kind];
  const missing =
    kind === DependencyKind.Unavailable ||
    lookup?.status === DependencyStatus.Missing;
  let dependency: ReactNode;
  if (!copy || !lookup) {
    dependency = (
      <span className="route-target-warning">
        <span aria-hidden="true">⚠ </span>
        {translate("unavailableTarget")}
      </span>
    );
  } else if (lookup.status === DependencyStatus.Found) {
    dependency = lookup.name;
  } else if (lookup.status === DependencyStatus.Loading) {
    dependency = <span className="route-target-muted">{copy.loading()}</span>;
  } else if (lookup.status === DependencyStatus.Missing) {
    dependency = (
      <span className="route-target-warning">
        <span aria-hidden="true">⚠ </span>
        {copy.missing()}
      </span>
    );
  } else {
    dependency = (
      <>
        {lookup.name && <span>{lookup.name}</span>}
        <span className="route-target-muted">{copy.failed()}</span>
        {lookup.stale && (
          <span className="route-target-muted">
            {translate("routeDependencyStale")}
          </span>
        )}
        <Button
          className="route-target-retry"
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          {copy.retry()}
        </Button>
      </>
    );
  }
  return (
    <li className={`route-target${missing ? " route-target-missing" : ""}`}>
      <div className="route-target-order">
        <span className="route-target-label">
          {translate("routeTargetOrder")}
        </span>
        <span>{ordinal}</span>
      </div>
      <div className="route-target-dependency">
        <span className="route-target-label">
          {translate("connectionOrPool")}
        </span>
        <div className="route-target-name">{dependency}</div>
        {copy && <span className="route-target-type">{copy.type()}</span>}
      </div>
      <div className="route-target-model">
        <span className="route-target-label">
          {translate("routeTargetModel")}
        </span>
        <span>{target.modelId}</span>
      </div>
      <div className="route-target-weight">
        {translate("routeTargetWeight", { value0: String(target.weight) })}
      </div>
    </li>
  );
}

/** One full-width route card. Targets are listed from a stably sorted copy; the route is never mutated. */
export function RouteCard({
  route,
  connections,
  pools,
  headingRef,
  onEdit,
  onDelete,
}: {
  route: Route;
  connections: DependencyQuery;
  pools: DependencyQuery;
  headingRef: (element: HTMLHeadingElement | null) => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  useLocale();
  const targets = route.targets.toSorted((a, b) => a.priority - b.priority);
  return (
    <Card className="route-card">
      <div className="route-card-header">
        <div className="route-card-identity">
          <div className="route-card-title">
            <h2 ref={headingRef} tabIndex={-1}>
              {route.modelId}
            </h2>
            <Badge tone={route.enabled ? "info" : "neutral"}>
              {route.enabled ? translate("enabled") : translate("disabled")}
            </Badge>
          </div>
          <p className="route-card-strategy">
            {translate("routeStrategyLabel", {
              value0: strategyLabel(route.strategy),
            })}
          </p>
        </div>
        <div className="route-card-actions">
          <Button
            aria-label={translate("editRouteFor", { value0: route.modelId })}
            onClick={onEdit}
          >
            {translate("editRoute")}
          </Button>
          <Button
            variant="danger"
            aria-label={translate("deleteRouteFor", { value0: route.modelId })}
            onClick={onDelete}
          >
            {translate("delete")}
          </Button>
        </div>
      </div>
      {targets.length === 0 ? (
        <p className="route-targets-empty">
          {translate("noTargetsConfigured")}
        </p>
      ) : (
        <>
          <div className="route-targets-header" aria-hidden="true">
            <span>{translate("routeTargetOrder")}</span>
            <span>{translate("connectionOrPool")}</span>
            <span>{translate("routeTargetModel")}</span>
            <span>{translate("routeTargetWeightHeading")}</span>
          </div>
          <ol
            className="route-targets"
            aria-label={translate("targetsForRoute", { value0: route.modelId })}
          >
            {targets.map((target, index) => (
              <TargetItem
                key={index}
                target={target}
                ordinal={index + 1}
                connections={connections}
                pools={pools}
              />
            ))}
          </ol>
        </>
      )}
    </Card>
  );
}

/** Structural subset of a React Query result needed to know that a list load settled. */
export type RouteListQuery = {
  data: unknown;
  isPending: boolean;
  fetchStatus: "fetching" | "paused" | "idle";
  dataUpdatedAt: number;
  errorUpdatedAt: number;
  isError: boolean;
};

/**
 * Focus and announcement rules for the routing list.
 * - After explicit page navigation settles, the list (or empty/error) heading takes focus once and a
 *   polite "Routing page loaded" message is announced.
 * - After a confirmed deletion removes the invoking control, focus moves to the next route heading,
 *   otherwise to the list or first-use heading.
 */
export function useRoutingFocus(query: RouteListQuery) {
  const listHeadingRef = useRef<HTMLHeadingElement>(null);
  const emptyHeadingRef = useRef<HTMLHeadingElement>(null);
  const routeHeadings = useRef(new Map<string, HTMLHeadingElement>());
  const [navigating, setNavigating] = useState(false);
  const [announcement, setAnnouncement] = useState("");
  const [restore, setRestore] = useState<{ at: number; nextId?: string }>();
  const settled = !query.isPending && query.fetchStatus === "idle";
  const listTarget = () => emptyHeadingRef.current ?? listHeadingRef.current;
  useEffect(() => {
    if (!navigating || !settled) return;
    listTarget()?.focus();
    if (!query.isError) setAnnouncement(translate("routingPageLoaded"));
    setNavigating(false);
  }, [navigating, settled]);
  useEffect(() => {
    if (!restore) return;
    const active = document.activeElement;
    if (!active || active === document.body || !active.isConnected) {
      const next = restore.nextId
        ? routeHeadings.current.get(restore.nextId)
        : undefined;
      (next?.isConnected ? next : listTarget())?.focus();
      setRestore(undefined);
    } else if (
      query.fetchStatus === "idle" &&
      Math.max(query.dataUpdatedAt, query.errorUpdatedAt) >= restore.at
    ) {
      setRestore(undefined);
    }
  }, [
    restore,
    query.data,
    query.fetchStatus,
    query.dataUpdatedAt,
    query.errorUpdatedAt,
  ]);
  return {
    listHeadingRef,
    emptyHeadingRef,
    announcement,
    routeHeadingRef: (id: string) => (element: HTMLHeadingElement | null) => {
      if (element) routeHeadings.current.set(id, element);
      else routeHeadings.current.delete(id);
    },
    /** Call when the user explicitly moves to another page of routes. */
    beginNavigation: () => {
      setAnnouncement("");
      setNavigating(true);
    },
    /** Call after a confirmed deletion; nextId is the route that followed the deleted one. */
    requestRestore: (nextId?: string) => setRestore({ at: Date.now(), nextId }),
  };
}
