// SPDX-License-Identifier: Apache-2.0
import { useId } from "react";
import type { Pool, ProviderConnection, Route } from "@tairouter/client";
import { t as translate, useLocale } from "./i18n";
import { pageHref } from "./routes";
import { Button, Dialog, ErrorNotice } from "./ui";

// Presentation state of one dependency category. Only Ready with no rows may render "None".
export enum DependencyState {
  Checking = "checking",
  Failed = "failed",
  Ready = "ready",
  Refreshing = "refreshing",
  Stale = "stale",
}
export enum DependencyCategory {
  AccountPools = "account-pools",
  DirectRoutes = "direct-routes",
}
// Structural subset of a React Query result so the state machine stays testable.
export type DependencyQuery<T> = {
  data: T[] | undefined;
  isError: boolean;
  fetchStatus: "fetching" | "paused" | "idle";
};
export function dependencyState<T>(query: DependencyQuery<T>): DependencyState {
  if (query.data === undefined)
    return query.isError ? DependencyState.Failed : DependencyState.Checking;
  if (query.fetchStatus === "fetching") return DependencyState.Refreshing;
  // A failed or paused (offline) refresh keeps last loaded rows but never proves a current result.
  if (query.isError || query.fetchStatus === "paused")
    return DependencyState.Stale;
  return DependencyState.Ready;
}
export type DependencyRow = { id: string; label: string };
export function poolRows(
  pools: Pool[] | undefined,
  connectionId: string,
): DependencyRow[] {
  return (pools ?? [])
    .filter((pool) =>
      pool.members.some((member) => member.connectionId === connectionId),
    )
    .map((pool) => ({ id: pool.id, label: pool.name || pool.id }));
}
export function routeRows(
  routes: Route[] | undefined,
  connectionId: string,
): DependencyRow[] {
  return (routes ?? [])
    .filter((route) =>
      route.targets.some(
        (target) =>
          target.target.case === "connectionId" &&
          target.target.value === connectionId,
      ),
    )
    .map((route) => ({ id: route.id, label: route.modelId || route.id }));
}
function DependencyGroup({
  category,
  state,
  rows,
}: {
  category: DependencyCategory;
  state: DependencyState;
  rows: DependencyRow[];
}) {
  const headingId = useId();
  const pools = category === DependencyCategory.AccountPools;
  const status =
    state === DependencyState.Checking
      ? translate("message_8bf7d8fc28")
      : state === DependencyState.Failed
        ? translate(
            pools ? "couldNotLoadAccountPools" : "couldNotLoadDirectRoutes",
          )
        : state === DependencyState.Refreshing
          ? translate("refreshingDependencies")
          : state === DependencyState.Stale
            ? translate("lastLoadedDataCouldNotRefresh")
            : undefined;
  const showRows = state !== DependencyState.Checking && rows.length > 0;
  return (
    <section
      className="disconnect-category"
      aria-labelledby={headingId}
      data-category={category}
      data-state={state}
    >
      <h4 id={headingId}>
        {pools ? translate("accountPools") : translate("directRoutes")}
      </h4>
      {status && (
        <p className="disconnect-status" role="status">
          {status}
        </p>
      )}
      {showRows && (
        <ul>
          {rows.map((row) => (
            <li key={row.id}>{row.label}</li>
          ))}
        </ul>
      )}
      {state === DependencyState.Ready && rows.length === 0 && (
        <p>{translate("message_dc937b5989")}</p>
      )}
    </section>
  );
}
export function DisconnectDialog({
  connection,
  providerLabel,
  organizationId,
  organizationName,
  pools,
  routes,
  pending,
  busy,
  error,
  onClose,
  onConfirm,
}: {
  connection: ProviderConnection;
  providerLabel: string;
  organizationId: string;
  organizationName: string;
  pools: DependencyQuery<Pool>;
  routes: DependencyQuery<Route>;
  /** True only while this connection's own disconnect is outstanding; scopes the label and status. */
  pending: boolean;
  /** True while any shared action is outstanding; keeps confirmation disabled so a submit cannot be dropped. */
  busy: boolean;
  error: unknown;
  onClose: () => void;
  onConfirm: () => void;
}) {
  useLocale();
  const consequenceId = useId();
  const poolState = dependencyState(pools);
  const routeState = dependencyState(routes);
  const failed =
    poolState === DependencyState.Failed ||
    routeState === DependencyState.Failed;
  const stale =
    poolState === DependencyState.Stale ||
    routeState === DependencyState.Stale ||
    pools.fetchStatus === "paused" ||
    routes.fetchStatus === "paused";
  return (
    <Dialog
      title={translate("disconnect_51710a", { value0: connection.name })}
      onClose={onClose}
      initialFocus="[data-cancel]"
      className="disconnect-dialog"
      describedBy={consequenceId}
    >
      <div className="disconnect-identity">
        <p className="disconnect-scope">
          {translate("disconnectScopeLine", {
            value0: providerLabel,
            value1: organizationName,
          })}
        </p>
        <p className="disconnect-secondary">
          {translate("onlyYourConnectionInThisOrganization")}
        </p>
        <p className="disconnect-secondary disconnect-id">
          {translate("connectionIdValue", { value0: connection.id })}
        </p>
      </div>
      <p id={consequenceId}>{translate("disconnectConsequence")}</p>
      <section className="disconnect-affected">
        <h3>{translate("affectedConfiguration")}</h3>
        <DependencyGroup
          category={DependencyCategory.AccountPools}
          state={poolState}
          rows={poolRows(pools.data, connection.id)}
        />
        <DependencyGroup
          category={DependencyCategory.DirectRoutes}
          state={routeState}
          rows={routeRows(routes.data, connection.id)}
        />
        <div className="disconnect-warning">
          <p>{translate("routesThroughPoolsNeedReview")}</p>
          <p>{translate("disconnectDoesNotRepairRouting")}</p>
          {failed && (
            <p role="status">
              {translate("dependenciesCouldNotBeLoadedReviewRouting")}
            </p>
          )}
          {stale && (
            <p role="status">{translate("dependenciesMayBeOutOfDate")}</p>
          )}
        </div>
        <a
          className="disconnect-link"
          href={pageHref("routing", organizationId)}
        >
          {translate("reviewRouting")}
        </a>
      </section>
      <ErrorNotice error={error} />
      <p className="disconnect-status" role="status">
        {pending ? translate("closingDoesNotCancelSubmittedChange") : ""}
      </p>
      <div className="actions disconnect-footer">
        <Button data-cancel onClick={onClose}>
          {translate("cancel")}
        </Button>
        <Button variant="danger" disabled={pending || busy} onClick={onConfirm}>
          {pending
            ? translate("disconnecting")
            : translate("disconnectProvider")}
        </Button>
      </div>
    </Dialog>
  );
}
