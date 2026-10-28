// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useRef, useState } from "react";
import { PoolStrategy, type Pool } from "@tairouter/client";
import { t as translate, useLocale, enumLabel } from "./i18n";
import { Badge, Button, Card } from "./ui";
import {
  DependencyStatus,
  resolveDependency,
  type DependencyQuery,
} from "./route-list";

/** Display state of the complete personal connection inventory, used by the first-use panel. */
export enum InventoryState {
  Loading = "loading",
  Failed = "failed",
  Empty = "empty",
  Populated = "populated",
}

/**
 * Classifies the complete connection enumeration. A failed lookup is never reported as empty,
 * even when earlier same-scope rows were retained, so the first-use panel cannot claim a
 * confirmed-empty inventory from stale or missing data.
 */
export function inventoryState(
  query: Pick<DependencyQuery, "data" | "isError">,
): InventoryState {
  if (query.isError) return InventoryState.Failed;
  if (!query.data) return InventoryState.Loading;
  return query.data.length === 0
    ? InventoryState.Empty
    : InventoryState.Populated;
}

/** Strategy as display text. The zero enum reads Unspecified and unmapped numbers read Unknown. */
export function poolStrategyLabel(strategy: number) {
  return enumLabel(PoolStrategy[strategy]);
}

/** Members sorted ascending by generic priority. A copy is sorted, equal priorities keep source order. */
export function orderedMembers(pool: Pool) {
  return pool.members.toSorted((a, b) => a.priority - b.priority);
}

function MemberItem({
  connectionId,
  weight,
  ordinal,
  connections,
}: {
  connectionId: string;
  weight: number;
  ordinal: number;
  connections: DependencyQuery;
}) {
  const lookup = resolveDependency(connections, connectionId);
  const missing = lookup.status === DependencyStatus.Missing;
  let name;
  if (lookup.status === DependencyStatus.Found) {
    name = lookup.name;
  } else if (lookup.status === DependencyStatus.Loading) {
    name = (
      <span className="pool-member-muted">
        {translate("loadingConnectionNameWithId", { value0: connectionId })}
      </span>
    );
  } else if (missing) {
    name = (
      <>
        <span className="pool-member-warning">
          <span aria-hidden="true">⚠ </span>
          {translate("unavailableConnectionWithId", { value0: connectionId })}
        </span>
        <span className="pool-member-muted">
          {translate("connectionNotFound")}
        </span>
      </>
    );
  } else {
    name = (
      <>
        {lookup.name ? (
          <span>{lookup.name}</span>
        ) : (
          <span className="pool-member-muted">
            {translate("connectionNameUnavailableWithId", {
              value0: connectionId,
            })}
          </span>
        )}
        {lookup.stale && (
          <span className="pool-member-muted">
            {translate("routeDependencyStale")}
          </span>
        )}
      </>
    );
  }
  return (
    <li className={`pool-member${missing ? " pool-member-missing" : ""}`}>
      <div className="pool-member-order">
        <span className="pool-member-label">
          {translate("poolMemberOrder")}
        </span>
        <span>{ordinal}</span>
      </div>
      <div className="pool-member-connection">
        <span className="pool-member-label">
          {translate("poolMemberConnection")}
        </span>
        <div className="pool-member-name">{name}</div>
      </div>
      <div className="pool-member-weight">
        {translate("poolMemberWeight", { value0: String(weight) })}
      </div>
    </li>
  );
}

/** One full-width pool card. Members are listed from a stably sorted copy; the pool is never mutated. */
export function PoolCard({
  pool,
  connections,
  disabled,
  editRef,
  onEdit,
  onDelete,
}: {
  pool: Pool;
  connections: DependencyQuery;
  /** True while the list is known unavailable, so mutating entry points stay inert. */
  disabled: boolean;
  editRef: (element: HTMLButtonElement | null) => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  useLocale();
  const nameId = useId();
  const membersId = useId();
  const members = orderedMembers(pool);
  return (
    <Card className="pool-card">
      <div className="pool-card-header">
        <div className="pool-card-title">
          <h2 id={nameId}>{pool.name}</h2>
          <Badge>{poolStrategyLabel(pool.strategy)}</Badge>
        </div>
        <div className="pool-card-actions">
          <Button
            ref={editRef}
            disabled={disabled}
            aria-describedby={nameId}
            onClick={onEdit}
          >
            {translate("editPool")}
          </Button>
          <Button
            variant="danger"
            disabled={disabled}
            aria-describedby={nameId}
            onClick={onDelete}
          >
            {translate("delete")}
          </Button>
        </div>
      </div>
      <p id={membersId} className="pool-members-title">
        {translate("membersInConfiguredOrder")}
      </p>
      {members.length === 0 ? (
        <p className="pool-members-empty">
          {translate("noConnectionsConfigured")}
        </p>
      ) : (
        <>
          <div className="pool-members-header" aria-hidden="true">
            <span>{translate("poolMemberOrder")}</span>
            <span>{translate("poolMemberConnection")}</span>
            <span>{translate("poolMemberWeightHeading")}</span>
          </div>
          <ol className="pool-members" aria-labelledby={membersId}>
            {members.map((member, index) => (
              <MemberItem
                key={index}
                connectionId={member.connectionId}
                weight={member.weight}
                ordinal={index + 1}
                connections={connections}
              />
            ))}
          </ol>
        </>
      )}
      <p className="pool-members-note">{translate("poolMemberOrderNote")}</p>
    </Card>
  );
}

/** Structural subset of a React Query result needed to know that a list load settled. */
export type PoolListQuery = {
  data: unknown;
  isPending: boolean;
  isError: boolean;
  fetchStatus: "fetching" | "paused" | "idle";
  dataUpdatedAt: number;
  errorUpdatedAt: number;
};

/**
 * Focus and announcement rules for the Account pools list.
 * - After explicit page navigation settles, "Pools loaded." is announced once. Focus moves to the
 *   collection heading only when the activating paging control disappeared or became unusable.
 * - After a confirmed deletion removes the invoking control, focus moves to the next card action,
 *   otherwise the preceding card action, otherwise Create pool.
 */
export function usePoolsFocus(query: PoolListQuery) {
  const listHeadingRef = useRef<HTMLHeadingElement>(null);
  const emptyHeadingRef = useRef<HTMLHeadingElement>(null);
  const createRef = useRef<HTMLButtonElement>(null);
  const editButtons = useRef(new Map<string, HTMLButtonElement>());
  const [navigating, setNavigating] = useState(false);
  const [announcement, setAnnouncement] = useState("");
  const [restore, setRestore] = useState<{
    at: number;
    nextId?: string;
    previousId?: string;
  }>();
  const settled = !query.isPending && query.fetchStatus === "idle";
  useEffect(() => {
    if (!navigating || !settled) return;
    const active = document.activeElement;
    const unusable =
      !active ||
      active === document.body ||
      !active.isConnected ||
      (active instanceof HTMLButtonElement && active.disabled);
    if (unusable) (emptyHeadingRef.current ?? listHeadingRef.current)?.focus();
    if (!query.isError) setAnnouncement(translate("poolsLoaded"));
    setNavigating(false);
  }, [navigating, settled]);
  useEffect(() => {
    if (!restore) return;
    const active = document.activeElement;
    if (!active || active === document.body || !active.isConnected) {
      const candidates = [
        restore.nextId ? editButtons.current.get(restore.nextId) : undefined,
        restore.previousId
          ? editButtons.current.get(restore.previousId)
          : undefined,
        createRef.current,
      ];
      candidates.find((item) => item?.isConnected && !item.disabled)?.focus();
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
    createRef,
    announcement,
    editRef: (id: string) => (element: HTMLButtonElement | null) => {
      if (element) editButtons.current.set(id, element);
      else editButtons.current.delete(id);
    },
    /** Call when the user explicitly moves to another page of pools. */
    beginNavigation: () => {
      setAnnouncement("");
      setNavigating(true);
    },
    /** Call after a confirmed deletion with the neighbors of the deleted pool. */
    requestRestore: (nextId?: string, previousId?: string) =>
      setRestore({ at: Date.now(), nextId, previousId }),
  };
}
