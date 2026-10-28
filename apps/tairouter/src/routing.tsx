// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "./i18n";
import { useEffect, useRef, useState, type ReactNode } from "react";
import {
  NATIVE_SETTINGS_SUMMARY_ID,
  NativePoolFields,
  nativePoolError,
  nativeProvider,
  type NativeMember,
} from "./native-pool";
import { create } from "@bufbuild/protobuf";
import { useQuery } from "@connectrpc/connect-query";
import {
  PoolService,
  RoutingService,
  ProviderService,
  PoolStrategy,
  RoutingStrategy,
  JevDecisionConfigSchema,
  JevQuotaTiersSchema,
  ReasoningRetentionConfigSchema,
  type ReasoningRetentionConfig,
  type JevDecisionConfig,
  ConnectionState,
  type Pool,
  type NativePoolOptions,
  type Route,
  type ProviderConnection,
  type RouteTarget,
} from "@tairouter/client";
import { useApi, type PageProps, pageSize } from "./api";
import { useAction } from "./actions";
import { usePersonalResources } from "./resources";
import { PoolDeleteDialog } from "./pool-delete";
import { useRestoreHeadingFocus } from "./focus-restore";
import { RouteCard, useRoutingFocus } from "./route-list";
import { pageHref } from "./routes";
import { Code, ConnectError } from "@connectrpc/connect";
import { RouteDeleteDialog } from "./route-delete";
import { utf8ByteLength } from "./validation";
import {
  PageHeader,
  Button,
  Card,
  Field,
  Dialog,
  DialogSize,
  ErrorNotice,
  ErrorSummary,
  type ErrorSummaryItem,
  Loading,
  EmptyState,
  Notice,
  Pagination,
  enumLabel,
  useOnline,
} from "./ui";
export function PoolsPage({ organization }: PageProps) {
  useLocale();
  const api = useApi();
  const action = useAction();
  const [token, setToken] = useState("");
  const [edit, setEdit] = useState<Pool | true>();
  const [remove, setRemove] = useState<Pool>();
  const query = useQuery(PoolService.method.listPools, {
    organizationId: organization.id,
    page: { pageSize, pageToken: token },
  });
  const {
    connections: accounts,
    pools,
    routes,
  } = usePersonalResources(organization.id);
  const { headingRef, requestRestore } = useRestoreHeadingFocus(query);
  // A confirmation is bound to the organization it was opened in.
  useEffect(() => {
    setRemove(undefined);
  }, [organization.id]);
  return (
    <>
      <PageHeader
        headingRef={headingRef}
        title={translate("accountPools")}
        description={translate("onlyYoursIn", { value0: organization.name })}
        action={
          <Button variant="primary" onClick={() => setEdit(true)}>
            {translate("createPool")}
          </Button>
        }
      />
      <Notice>{translate("poolsGroupYourOwnConnectionsStrategyAnd")}</Notice>
      {action.feedback}
      <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      {query.isPending ? (
        <Loading />
      ) : query.data?.pools.length === 0 ? (
        <EmptyState title={translate("createYourFirstPool")}>
          {translate("connectProvidersBeforeAddingThemToA")}
        </EmptyState>
      ) : (
        <div className="records">
          {query.data?.pools.map((pool) => (
            <Card key={pool.id} className="record">
              <div>
                <h2>{pool.name}</h2>
                <p>{enumLabel(PoolStrategy[pool.strategy])}</p>
                <ol>
                  {pool.members
                    .toSorted((a, b) => a.priority - b.priority)
                    .map((m) => (
                      <li key={m.connectionId}>
                        {translate("weight", {
                          value0:
                            accounts.data?.find((c) => c.id === m.connectionId)
                              ?.name ??
                            `Unavailable connection (${m.connectionId})`,
                          value1: m.weight,
                        })}
                      </li>
                    ))}
                </ol>
              </div>
              <div className="actions">
                <Button onClick={() => setEdit(pool)}>
                  {translate("editPool")}
                </Button>
                <Button variant="danger" onClick={() => setRemove(pool)}>
                  {translate("delete")}
                </Button>
              </div>
            </Card>
          ))}
        </div>
      )}
      <Pagination
        next={query.data?.page?.nextPageToken}
        onNext={setToken}
        onFirst={() => setToken("")}
        hasPrevious={!!token}
      />
      {edit && (
        <PoolEditor
          organizationName={organization.name}
          pool={edit === true ? undefined : edit}
          existingNames={(pools.data ?? [])
            .filter((p) => edit === true || p.id !== edit.id)
            .map((p) => p.name)}
          accounts={accounts.data ?? []}
          partial={false}
          loading={accounts.isPending || pools.isPending}
          loadError={accounts.error ?? pools.error}
          onRetry={() => {
            void accounts.refetch();
            void pools.refetch();
          }}
          error={action.error}
          pending={action.pending}
          onClose={() => setEdit(undefined)}
          onSave={(v) => {
            void action.run<unknown>(
              () =>
                edit === true
                  ? api.pool.createPool({
                      organizationId: organization.id,
                      ...v,
                    })
                  : api.pool.updatePool({
                      organizationId: organization.id,
                      poolId: edit.id,
                      ...v,
                    }),
              () => setEdit(undefined),
            );
          }}
        />
      )}
      {remove && (
        <PoolDeleteDialog
          pool={remove}
          organizationId={organization.id}
          organizationName={organization.name}
          onClose={() => setRemove(undefined)}
          pending={action.pending}
          error={action.error}
          onConfirm={() => {
            void action.run(
              () =>
                api.pool.deletePool({
                  organizationId: organization.id,
                  poolId: remove.id,
                }),
              () => {
                setRemove(undefined);
                requestRestore();
              },
              translate("poolDeleted"),
            );
          }}
        />
      )}
    </>
  );
}
type PoolValues = {
  name: string;
  strategy: PoolStrategy;
  affinityEnabled: boolean;
  members: NativeMember[];
  nativeOptions?: NativePoolOptions;
};
/** Service-enforced pool limits mirrored by the editor. */
enum PoolLimit {
  NameBytes = 200,
  MinMembers = 1,
  MaxMembers = 100,
  MinWeight = 1,
  MaxWeight = 1000,
}
const poolMessage = {
  nameRequired: () => translate("poolNameIsRequired"),
  nameBytes: () =>
    translate("poolNameMustBeUtf8Bytes", { value0: PoolLimit.NameBytes }),
  nameTaken: () => translate("thisNameIsAlreadyInUseChoose"),
  noMembers: () => translate("addAtLeastOneConnection"),
  tooManyMembers: () =>
    translate("aPoolCanContainAtMostConnections", {
      value0: PoolLimit.MaxMembers,
    }),
  weight: () => translate("weightMustBeAWholeNumberFrom"),
  unavailable: () => translate("thisConnectionIsUnavailableRemoveItThen"),
};
function PoolEditor({
  organizationName,
  existingNames,
  pool,
  accounts,
  partial,
  loading,
  loadError,
  onRetry,
  onClose,
  onSave,
  pending,
  error,
}: {
  organizationName: string;
  existingNames: string[];
  pool?: Pool;
  accounts: ProviderConnection[];
  partial: boolean;
  /** True while connections or existing pool names are still loading. */
  loading: boolean;
  /** Error from loading connections or existing pool names. */
  loadError?: unknown;
  onRetry?: () => void;
  onClose: () => void;
  onSave: (v: PoolValues) => void;
  pending: boolean;
  error: unknown;
}) {
  useLocale();
  const [name, setName] = useState(pool?.name ?? "");
  const [nativeOptions, setNativeOptions] = useState(pool?.nativeOptions);
  const [affinityEnabled, setAffinityEnabled] = useState(
    pool?.affinityEnabled ?? false,
  );
  const [strategy, setStrategy] = useState(
    pool?.strategy ?? PoolStrategy.PRIORITY,
  );
  const [members, setMembers] = useState<NativeMember[]>(
    pool?.members.map((m) => ({
      connectionId: m.connectionId,
      nativeOptions: m.nativeOptions,
      priority: m.priority,
      weight: m.weight,
    })) ?? [],
  );
  // Required-field errors stay quiet until the user interacts with the form.
  const [touched, setTouched] = useState(false);
  const [announcement, setAnnouncement] = useState("");
  const focusRequest = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (focusRequest.current) {
      document.getElementById(focusRequest.current)?.focus();
      focusRequest.current = undefined;
    }
  });
  const trimmedName = name.trim();
  const nameError = !trimmedName
    ? { message: poolMessage.nameRequired(), required: true }
    : utf8ByteLength(trimmedName) > PoolLimit.NameBytes
      ? { message: poolMessage.nameBytes(), required: false }
      : existingNames.includes(trimmedName)
        ? { message: poolMessage.nameTaken(), required: false }
        : undefined;
  const shown = (message: string | undefined, required: boolean) =>
    message && (touched || !required) ? message : undefined;
  const dependenciesReady = !loading && !loadError;
  const memberLabel = (m: NativeMember) =>
    accounts.find((a) => a.id === m.connectionId)?.name ??
    (loading
      ? translate("loadingConnection")
      : translate("unavailableConnection"));
  const memberErrors = members.map((m) => ({
    unavailable:
      dependenciesReady && !accounts.some((a) => a.id === m.connectionId),
    weight:
      Number.isInteger(m.weight) &&
      m.weight >= PoolLimit.MinWeight &&
      m.weight <= PoolLimit.MaxWeight
        ? undefined
        : poolMessage.weight(),
  }));
  const countError =
    members.length < PoolLimit.MinMembers
      ? poolMessage.noMembers()
      : members.length > PoolLimit.MaxMembers
        ? poolMessage.tooManyMembers()
        : undefined;
  const nativeError = nativePoolError(
    nativeOptions,
    members,
    nativeProvider(members, accounts),
  );
  const readyConnections = accounts.filter(
    (a) => a.state === ConnectionState.READY,
  );
  const addable = readyConnections.filter(
    (a) => !members.some((m) => m.connectionId === a.id),
  );
  const addHint = loading
    ? translate("loadingYourConnections")
    : addable.length > 0
      ? undefined
      : readyConnections.length === 0
        ? translate("noAvailableConnectionsToAdd")
        : translate("allAvailableConnectionsAreInThisPool");
  const summary: ErrorSummaryItem[] = [];
  if (nameError && shown(nameError.message, nameError.required))
    summary.push({
      key: "name",
      message: nameError.message,
      fieldId: "pool-name",
    });
  if (shown(countError, true))
    summary.push({
      key: "member-count",
      message: countError!,
      fieldId: "pool-add-connection",
    });
  members.forEach((m, i) => {
    const e = memberErrors[i]!;
    const label = translate("connection_aeeb37", {
      value0: i + 1,
      value1: memberLabel(m),
    });
    if (e.unavailable)
      summary.push({
        key: `${m.connectionId}-unavailable`,
        message: translate("message_6a776812d7", {
          value0: label,
          value1: poolMessage.unavailable(),
        }),
        fieldId: `pool-member-${m.connectionId}-remove`,
      });
    if (e.weight)
      summary.push({
        key: `${m.connectionId}-weight`,
        message: translate("message_6a776812d7", {
          value0: label,
          value1: e.weight,
        }),
        fieldId: `pool-member-${m.connectionId}-weight`,
      });
  });
  // The full native message is shown inside the (auto-opened) disclosure; the
  // summary links to it so keyboard users can reach the fix.
  if (nativeError)
    summary.push({
      key: "native",
      message: `Native OAuth pool settings: ${nativeError}`,
      fieldId: NATIVE_SETTINGS_SUMMARY_ID,
    });
  const canSave =
    !pending &&
    dependenciesReady &&
    !nameError &&
    !countError &&
    memberErrors.every((e) => !e.unavailable && !e.weight) &&
    !nativeError;
  const updateMember = (connectionId: string, patch: Partial<NativeMember>) =>
    setMembers((old) =>
      old.map((x) =>
        x.connectionId === connectionId ? { ...x, ...patch } : x,
      ),
    );
  return (
    <Dialog
      title={pool ? translate("editPool") : translate("createPool")}
      subtitle={translate("onlyYoursIn", { value0: organizationName })}
      onClose={onClose}
      size={DialogSize.Editor}
    >
      <form
        aria-busy={pending}
        onChange={() => setTouched(true)}
        onSubmit={(e) => {
          e.preventDefault();
          if (!canSave) return;
          onSave({
            name: trimmedName,
            strategy,
            affinityEnabled,
            nativeOptions,
            members: members.map((m, i) => ({ ...m, priority: i })),
          });
        }}
      >
        <section className="route-section" aria-labelledby="pool-details">
          <h3 id="pool-details">{translate("poolDetails")}</h3>
          <div className="field-grid">
            <Field
              label={translate("poolName")}
              hint={translate("caseSensitiveUpToUtf8Bytes", {
                value0: PoolLimit.NameBytes,
              })}
              error={shown(nameError?.message, nameError?.required ?? false)}
            >
              <input
                id="pool-name"
                required
                autoFocus
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
            <Field label={translate("strategy")}>
              <select
                value={strategy}
                onChange={(e) => setStrategy(Number(e.target.value))}
              >
                {[
                  PoolStrategy.PRIORITY,
                  PoolStrategy.ROUND_ROBIN,
                  PoolStrategy.LEAST_RECENTLY_USED,
                ].map((s) => (
                  <option key={s} value={s}>
                    {enumLabel(PoolStrategy[s])}
                  </option>
                ))}
              </select>
            </Field>
          </div>
        </section>
        <section className="route-section" aria-labelledby="pool-affinity">
          <h3 id="pool-affinity">{translate("sessionAffinity")}</h3>
          <label className="check">
            <input
              type="checkbox"
              aria-describedby="pool-affinity-help"
              checked={affinityEnabled}
              onChange={(event) => setAffinityEnabled(event.target.checked)}
            />
            {translate("keepGenericOauthSessionsOnTheSame")}
          </label>
          <p id="pool-affinity-help" className="section-hint">
            {translate("optInForProvidersThatSupportGeneric")}
          </p>
        </section>
        <section
          className="route-section"
          role="group"
          aria-labelledby="pool-connections-heading"
          aria-describedby={
            shown(countError, true) ? "pool-connections-error" : undefined
          }
        >
          <h3 id="pool-connections-heading">
            {translate("yourConnectionsInPriorityOrder")}
          </h3>
          <p className="section-hint">
            {translate("topConnectionHasFirstPriority")}
          </p>
          <ErrorNotice error={loadError} retry={onRetry} />
          {loading && (
            <div role="status" className="section-hint">
              {translate("loadingYourConnections")}
            </div>
          )}
          {members.map((m, i) => {
            const rowId = `pool-member-${m.connectionId}`;
            const label = memberLabel(m);
            const e = memberErrors[i]!;
            return (
              <fieldset
                className="target-card member-card"
                key={m.connectionId}
              >
                <legend>
                  {translate("disconnectScopeLine", {
                    value0: i + 1,
                    value1: label,
                  })}
                </legend>
                <Field
                  label={translate("weight_81d27e")}
                  hint={translate("wholeNumberFrom1To1000")}
                  error={e.weight}
                >
                  <input
                    id={`${rowId}-weight`}
                    type="number"
                    min={PoolLimit.MinWeight}
                    max={PoolLimit.MaxWeight}
                    step={1}
                    value={m.weight}
                    onChange={(ev) =>
                      updateMember(m.connectionId, {
                        weight: Number(ev.target.value),
                      })
                    }
                  />
                </Field>
                {e.unavailable && (
                  <p className="group-error">{poolMessage.unavailable()}</p>
                )}
                <div className="actions">
                  <Button
                    id={`${rowId}-up`}
                    aria-label={translate("moveUp_a90393", { value0: label })}
                    disabled={i === 0}
                    onClick={() => {
                      setTouched(true);
                      setMembers((old) => {
                        const next = [...old];
                        [next[i - 1], next[i]] = [next[i]!, next[i - 1]!];
                        return next;
                      });
                      focusRequest.current =
                        i - 1 === 0 ? `${rowId}-remove` : `${rowId}-up`;
                      setAnnouncement(
                        translate("movedUpToPosition", {
                          value0: label,
                          value1: i,
                        }),
                      );
                    }}
                  >
                    {translate("moveUp")}
                  </Button>
                  <Button
                    id={`${rowId}-remove`}
                    variant="danger"
                    aria-label={translate("remove_3028bf", { value0: label })}
                    onClick={() => {
                      setTouched(true);
                      const neighbor = members[i + 1] ?? members[i - 1];
                      focusRequest.current = neighbor
                        ? `pool-member-${neighbor.connectionId}-remove`
                        : "pool-add-connection";
                      setMembers((old) =>
                        old.filter((x) => x.connectionId !== m.connectionId),
                      );
                      const left = members.length - 1;
                      setAnnouncement(
                        translate(
                          left === 1
                            ? "removedConnectionRemaining"
                            : "removedConnectionsRemaining",
                          { value0: label, value1: left },
                        ),
                      );
                    }}
                  >
                    {translate("remove")}
                  </Button>
                </div>
              </fieldset>
            );
          })}
          <Field label={translate("addAConnection")} hint={addHint}>
            <select
              id="pool-add-connection"
              aria-describedby={
                shown(countError, true) ? "pool-connections-error" : undefined
              }
              value=""
              disabled={loading}
              onChange={(e) => {
                const connectionId = e.target.value;
                if (!connectionId) return;
                setTouched(true);
                setMembers((old) => [
                  ...old,
                  { connectionId, priority: old.length, weight: 1 },
                ]);
                setAnnouncement(
                  translate(
                    members.length + 1 === 1
                      ? "addedConnectionInThisPool"
                      : "addedConnectionsInThisPool",
                    {
                      value0:
                        accounts.find((a) => a.id === connectionId)?.name ??
                        translate("connection_b38d9d"),
                      value1: members.length + 1,
                    },
                  ),
                );
              }}
            >
              <option value="">{translate("chooseYourConnection")}</option>
              {addable.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
          </Field>
          {shown(countError, true) && (
            <p id="pool-connections-error" className="group-error">
              {countError}
            </p>
          )}
        </section>
        <NativePoolFields
          options={nativeOptions}
          onChange={setNativeOptions}
          members={members}
          onMembers={setMembers}
          accounts={accounts}
          isNew={!pool}
        />
        <ErrorSummary errors={summary} />
        {partial && (
          <Notice tone="warning">
            {translate("onlyTheFirstConnectionsAreAvailableIn", {
              value0: pageSize,
            })}
          </Notice>
        )}
        <ErrorNotice error={error} />
        <div
          className="visually-hidden"
          role="status"
          aria-live="polite"
          aria-atomic="true"
        >
          {pending ? translate("savingPool") : announcement}
        </div>
        <div className="actions">
          <Button onClick={onClose}>{translate("cancel")}</Button>
          <Button type="submit" variant="primary" disabled={!canSave}>
            {pending ? translate("message_23e39291d6") : translate("savePool")}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
export function RoutingPage({ organization }: PageProps) {
  useLocale();
  const api = useApi();
  const action = useAction();
  const online = useOnline();
  const [token, setToken] = useState("");
  const [edit, setEdit] = useState<Route | true>();
  const [remove, setRemove] = useState<Route>();
  const query = useQuery(RoutingService.method.listRoutes, {
    organizationId: organization.id,
    page: { pageSize, pageToken: token },
  });
  const {
    connections: accounts,
    pools,
    routes,
  } = usePersonalResources(organization.id);
  // A confirmation is bound to the organization it was opened in.
  useEffect(() => {
    setRemove(undefined);
  }, [organization.id]);
  const focus = useRoutingFocus(query);
  const listCode = query.error
    ? ConnectError.from(query.error).code
    : undefined;
  const accessDenied =
    listCode === Code.PermissionDenied || listCode === Code.Unauthenticated;
  // Cached rows are not authorization; they are hidden once access is denied.
  const rows = accessDenied ? undefined : query.data?.routes;
  const stale = !!rows && (!!query.error || !online);
  const navigate = (next: string) => {
    focus.beginNavigation();
    setToken(next);
  };
  return (
    <>
      <PageHeader
        headingRef={focus.listHeadingRef}
        title={translate("yourRouting")}
        description={translate("onlyYoursIn", { value0: organization.name })}
        action={
          <Button variant="primary" onClick={() => setEdit(true)}>
            {translate("createRoute")}
          </Button>
        }
      />
      <Notice>{translate("modelAliasesChooseYourPrimaryAndFallback")}</Notice>
      {action.feedback}
      <ErrorNotice
        error={query.error}
        retry={accessDenied ? undefined : () => void query.refetch()}
      />
      {stale && (
        <Notice tone="warning">{translate("routingMayBeOutOfDate")}</Notice>
      )}
      {query.isPending ? (
        <div className="loading-state" role="status" aria-busy="true">
          <p>{translate("loadingRouting")}</p>
          <div className="route-skeletons" aria-hidden="true">
            <div className="skeleton" />
            <div className="skeleton" />
            <div className="skeleton" />
          </div>
        </div>
      ) : rows && rows.length === 0 ? (
        <Card className="empty route-empty">
          <h2 ref={focus.emptyHeadingRef} tabIndex={-1}>
            {token
              ? translate("noRoutesOnThisPage")
              : translate("configureYourFirstModelRoute")}
          </h2>
          <p>
            {token
              ? translate("useFirstPageToReturnToYourRoutes")
              : translate("addAProviderConnectionThenMapA")}
          </p>
          {!token && (
            <>
              {[
                {
                  key: "connections",
                  result: accounts,
                  message: translate("connectionDetailsUnavailable"),
                  retry: translate("tryAgainLoadingConnections"),
                },
                {
                  key: "pools",
                  result: pools,
                  message: translate("poolDetailsUnavailable"),
                  retry: translate("tryAgainLoadingPools"),
                },
              ].map(
                ({ key, result, message, retry }) =>
                  result.isError && (
                    <Notice key={key} tone="warning">
                      {message}
                      <div className="actions">
                        <Button
                          disabled={result.isFetching}
                          onClick={() => void result.refetch()}
                        >
                          {retry}
                        </Button>
                      </div>
                    </Notice>
                  ),
              )}
              <div className="actions">
                <a
                  className="button"
                  href={pageHref("providers", organization.id)}
                >
                  {translate("viewProviders")}
                </a>
              </div>
            </>
          )}
        </Card>
      ) : rows ? (
        <ul className="route-list" aria-label={translate("yourRoutes")}>
          {rows.map((route) => (
            <li key={route.id}>
              <RouteCard
                route={route}
                connections={accounts}
                pools={pools}
                headingRef={focus.routeHeadingRef(route.id)}
                onEdit={() => setEdit(route)}
                onDelete={() => setRemove(route)}
              />
            </li>
          ))}
        </ul>
      ) : null}
      <Pagination
        next={rows ? query.data?.page?.nextPageToken : undefined}
        onNext={navigate}
        onFirst={() => navigate("")}
        hasPrevious={!!token}
        pending={query.isFetching}
      />
      <p className="visually-hidden" role="status" aria-live="polite">
        {focus.announcement}
      </p>
      {edit && (
        <RouteEditor
          organizationName={organization.name}
          decisionRoutes={routes.data ?? []}
          route={edit === true ? undefined : edit}
          existingNames={(routes.data ?? [])
            .filter((r) => edit === true || r.id !== edit.id)
            .map((r) => r.modelId)}
          accounts={accounts.data ?? []}
          pools={pools.data ?? []}
          partial={false}
          pending={action.pending}
          error={action.error ?? accounts.error ?? pools.error}
          onClose={() => setEdit(undefined)}
          onSave={(v) => {
            void action.run<unknown>(
              () =>
                edit === true
                  ? api.routing.createRoute({
                      organizationId: organization.id,
                      ...v,
                    })
                  : api.routing.updateRoute({
                      organizationId: organization.id,
                      routeId: edit.id,
                      ...v,
                    }),
              () => setEdit(undefined),
            );
          }}
        />
      )}
      {remove && (
        <RouteDeleteDialog
          route={remove}
          organizationName={organization.name}
          onClose={() => setRemove(undefined)}
          pending={action.pending}
          error={action.error}
          onConfirm={() => {
            void action.run(
              () =>
                api.routing.deleteRoute({
                  organizationId: organization.id,
                  routeId: remove.id,
                }),
              () => {
                const list = query.data?.routes ?? [];
                const at = list.findIndex((item) => item.id === remove.id);
                setRemove(undefined);
                focus.requestRestore(list[at + 1]?.id);
              },
              translate("routeDeleted"),
            );
          }}
        />
      )}
    </>
  );
}
enum JevEffort {
  Low = "low",
  Medium = "medium",
  High = "high",
  ExtraHigh = "xhigh",
  Max = "max",
  Ultra = "ultra",
}
type TargetValue = {
  target:
    { case: "connectionId"; value: string } | { case: "poolId"; value: string };
  modelId: string;
  reasoningEfforts: string[];
  modelProfile: string;
  priority: number;
  weight: number;
};
/** Service-enforced route limits mirrored by the editor. */
enum RouteLimit {
  AliasBytes = 512,
  ModelIdBytes = 512,
  MaxTargets = 100,
  MaxJevTargets = 64,
  MinWeight = 1,
  MaxWeight = 1000,
}
const targetCountMessage = {
  direct: () => translate("directRoutingNeedsExactlyOneTarget"),
  jev: () =>
    translate("jevRoutingNeeds1ToTargets", {
      value0: RouteLimit.MaxJevTargets,
    }),
  ordinary: () =>
    translate("thisRoutingStrategyNeeds1ToTargets", {
      value0: RouteLimit.MaxTargets,
    }),
};
/** Returns the group error for the draft target count, if any. */
function targetCountError(
  strategy: RoutingStrategy,
  count: number,
): string | undefined {
  if (strategy === RoutingStrategy.DIRECT)
    return count === 1 ? undefined : targetCountMessage.direct();
  if (strategy === RoutingStrategy.JEV)
    return count >= 1 && count <= RouteLimit.MaxJevTargets
      ? undefined
      : targetCountMessage.jev();
  return count >= 1 && count <= RouteLimit.MaxTargets
    ? undefined
    : targetCountMessage.ordinary();
}
type TargetErrors = {
  source?: string;
  model?: string;
  weight?: string;
  sourceRequired: boolean;
  modelRequired: boolean;
};
/** Validates one draft target. Required-field errors are flagged separately. */
function targetErrors(
  t: TargetValue,
  accounts: ProviderConnection[],
  pools: Pool[],
): TargetErrors {
  const known =
    t.target.case === "connectionId"
      ? accounts.some((a) => a.id === t.target.value)
      : pools.some((p) => p.id === t.target.value);
  return {
    sourceRequired: !t.target.value,
    modelRequired: !t.modelId.trim(),
    source: !t.target.value
      ? translate("chooseAConnectionOrPool")
      : known
        ? undefined
        : translate("thisConnectionOrPoolIsUnavailableChoose"),
    model: !t.modelId.trim()
      ? translate("providerModelIdIsRequired")
      : utf8ByteLength(t.modelId) > RouteLimit.ModelIdBytes
        ? translate("providerModelIdMustBeUtf8", {
            value0: RouteLimit.ModelIdBytes,
          })
        : undefined,
    weight:
      Number.isInteger(t.weight) &&
      t.weight >= RouteLimit.MinWeight &&
      t.weight <= RouteLimit.MaxWeight
        ? undefined
        : translate("weightMustBeAWholeNumberFrom"),
  };
}
/** Service-enforced JEV decision and reasoning retention bounds. */
enum JevLimit {
  TimeoutMinMs = 1000,
  TimeoutMaxMs = 120000,
  PinMinSeconds = 60,
  PinMaxSeconds = 86400,
  ServiceModelUnits = 512,
  DefaultLimitedPercent = 70,
  DefaultNearlyExhaustedPercent = 90,
}
enum RetentionLimit {
  MaxPercent = 100,
  MaxTokens = 4294967295,
}
type JevDecisionErrors = {
  source?: string;
  serviceModel?: string;
  timeout?: string;
  pin?: string;
  moderate?: string;
  limited?: string;
  nearlyExhausted?: string;
};
const percentError = () => translate("useAFinitePercentageFrom0To");
const isPercent = (value: number) =>
  Number.isFinite(value) && value >= 0 && value <= RetentionLimit.MaxPercent;
/**
 * Validates the JEV decision draft. Quota thresholds are validated even when
 * advisory signals are off, and an explicit zero stays distinct from omitted.
 */
function jevDecisionErrors(
  jev: JevDecisionConfig,
  source: string,
): JevDecisionErrors {
  const tiers = jev.decisionQuotaTiers;
  const errors: JevDecisionErrors = {};
  if (source === "route") {
    if (!jev.decisionModel)
      errors.source = translate("chooseADecisionRouteAlias");
  } else {
    if (!jev.decisionConnectionId)
      errors.source = translate("chooseADecisionConnection");
    if (jev.decisionServiceModel.length > JevLimit.ServiceModelUnits)
      errors.serviceModel = translate(
        "decisionServiceModelMustBeCharactersOr",
        {
          value0: JevLimit.ServiceModelUnits,
        },
      );
  }
  const timeout = jev.decisionTimeoutMs;
  if (
    !Number.isInteger(timeout) ||
    !(
      timeout === 0 ||
      (timeout >= JevLimit.TimeoutMinMs && timeout <= JevLimit.TimeoutMaxMs)
    )
  )
    errors.timeout = translate("decisionTimeoutMustBe0OrA");
  const pin = jev.sessionPinTtlSeconds;
  if (
    !Number.isInteger(pin) ||
    !(
      pin === 0 ||
      (pin >= JevLimit.PinMinSeconds && pin <= JevLimit.PinMaxSeconds)
    )
  )
    errors.pin = translate("sessionPinDurationMustBe0Or");
  if (!tiers) return errors;
  if (tiers.moderate !== undefined && !isPercent(tiers.moderate))
    errors.moderate = percentError();
  if (tiers.limited !== undefined && !isPercent(tiers.limited))
    errors.limited = percentError();
  if (tiers.nearlyExhausted !== undefined && !isPercent(tiers.nearlyExhausted))
    errors.nearlyExhausted = percentError();
  const limited = tiers.limited ?? JevLimit.DefaultLimitedPercent;
  const nearly =
    tiers.nearlyExhausted ?? JevLimit.DefaultNearlyExhaustedPercent;
  if (
    !errors.moderate &&
    tiers.moderate !== undefined &&
    Number.isFinite(limited) &&
    tiers.moderate >= limited
  )
    errors.moderate = translate("moderateMustBeBelowLimitedEffective", {
      value0: limited,
    });
  if (
    !errors.limited &&
    !errors.nearlyExhausted &&
    Number.isFinite(limited) &&
    Number.isFinite(nearly) &&
    limited >= nearly
  ) {
    // Blame the field the user actually set; both may be defaults only when
    // the defaults themselves are ordered, so one of them is always explicit.
    if (tiers.limited !== undefined)
      errors.limited = translate("limitedMustBeBelowNearlyExhaustedEffective", {
        value0: nearly,
      });
    else
      errors.nearlyExhausted = translate(
        "nearlyExhaustedMustBeAboveLimitedEffective",
        { value0: limited },
      );
  }
  return errors;
}
type RetentionErrors = { percent?: string; tokens?: string };
/** Validates optional retention limits, including while retention is disabled. */
function retentionLimitErrors(
  retention: ReasoningRetentionConfig | undefined,
): RetentionErrors {
  const errors: RetentionErrors = {};
  if (!retention) return errors;
  if (
    retention.maxContextPercent !== undefined &&
    !isPercent(retention.maxContextPercent)
  )
    errors.percent = translate("contextLimitMustBeAPercentageFrom");
  if (
    retention.maxTokens !== undefined &&
    !(
      Number.isInteger(retention.maxTokens) &&
      retention.maxTokens >= 0 &&
      retention.maxTokens <= RetentionLimit.MaxTokens
    )
  )
    errors.tokens = translate("tokenLimitMustBeAWholeNumber");
  return errors;
}
/**
 * A details disclosure that opens itself when it contains a blocking error so
 * the error and its control are never hidden.
 */
function Disclosure({
  summary,
  forceOpen,
  children,
}: {
  summary: string;
  forceOpen: boolean;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (forceOpen) setOpen(true);
  }, [forceOpen]);
  return (
    <details
      className="route-disclosure"
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary>{summary}</summary>
      {children}
    </details>
  );
}
/** Draft target with a stable row key so reordering keeps row identity. */
type TargetDraft = TargetValue & { key: number };
type RouteValues = {
  reasoningRetention?: ReasoningRetentionConfig;
  jevDecision?: JevDecisionConfig;
  modelId: string;
  strategy: RoutingStrategy;
  targets: TargetValue[];
  enabled: boolean;
};
function RouteEditor({
  organizationName,
  decisionRoutes,
  existingNames,
  route,
  accounts,
  pools,
  partial,
  pending,
  error,
  onClose,
  onSave,
}: {
  existingNames: string[];
  route?: Route;
  accounts: ProviderConnection[];
  pools: Pool[];
  partial: boolean;
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onSave: (v: RouteValues) => void;
  decisionRoutes: Route[];
  organizationName: string;
}) {
  useLocale();
  const [name, setName] = useState(route?.modelId ?? "");
  const [jev, setJev] = useState(() =>
    create(JevDecisionConfigSchema, route?.jevDecision),
  );
  const [retention, setRetention] = useState<
    ReasoningRetentionConfig | undefined
  >(route?.reasoningRetention);
  const [decisionSource, setDecisionSource] = useState(
    route?.jevDecision?.decisionModel ? "route" : "connection",
  );
  const tiers = jev.decisionQuotaTiers;
  const jevErrors = jevDecisionErrors(jev, decisionSource);
  const validJev = !Object.values(jevErrors).some(Boolean);
  const retentionErrors = retentionLimitErrors(retention);
  const validRetention = !retentionErrors.percent && !retentionErrors.tokens;
  const [strategy, setStrategy] = useState(
    route?.strategy ?? RoutingStrategy.FALLBACK,
  );
  const [enabled, setEnabled] = useState(route?.enabled ?? true);
  const nextKey = useRef(0);
  const [targets, setTargets] = useState<TargetDraft[]>(
    () =>
      route?.targets
        .filter(
          (t): t is RouteTarget & { target: TargetValue["target"] } =>
            !!t.target.case,
        )
        .map((t) => ({
          key: nextKey.current++,
          target: t.target,
          modelId: t.modelId,
          reasoningEfforts: [...t.reasoningEfforts],
          modelProfile: t.modelProfile,
          priority: t.priority,
          weight: t.weight,
        }))
        .toSorted((a, b) => a.priority - b.priority) ?? [],
  );
  // Required-field errors stay quiet until the user interacts with the form.
  const [touched, setTouched] = useState(false);
  const [announcement, setAnnouncement] = useState("");
  const focusRequest = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (focusRequest.current) {
      document.getElementById(focusRequest.current)?.focus();
      focusRequest.current = undefined;
    }
  });
  const trimmedName = name.trim();
  const aliasError = !trimmedName
    ? { message: translate("modelAliasIsRequired"), required: true }
    : utf8ByteLength(trimmedName) > RouteLimit.AliasBytes
      ? {
          message: translate("modelAliasMustBeUtf8Bytes", {
            value0: RouteLimit.AliasBytes,
          }),
          required: false,
        }
      : existingNames.includes(trimmedName)
        ? {
            message: translate("thisNameIsAlreadyInUseChoose"),
            required: false,
          }
        : undefined;
  const countError = targetCountError(strategy, targets.length);
  const rowErrors = targets.map((t) => targetErrors(t, accounts, pools));
  const shown = (message: string | undefined, required: boolean) =>
    message && (touched || !required) ? message : undefined;
  const summary: ErrorSummaryItem[] = [];
  if (aliasError && shown(aliasError.message, aliasError.required))
    summary.push({
      key: "alias",
      message: aliasError.message,
      fieldId: "route-alias",
    });
  if (shown(countError, true))
    summary.push({
      key: "target-count",
      message: countError!,
      fieldId: "route-add-target",
    });
  targets.forEach((t, i) => {
    const e = rowErrors[i]!;
    const label = translate("target_5a2ba5", { value0: i + 1 });
    if (shown(e.source, e.sourceRequired))
      summary.push({
        key: `${t.key}-source`,
        message: translate("message_6a776812d7", {
          value0: label,
          value1: e.source,
        }),
        fieldId: `route-target-${t.key}-source`,
      });
    if (shown(e.model, e.modelRequired))
      summary.push({
        key: `${t.key}-model`,
        message: translate("message_6a776812d7", {
          value0: label,
          value1: e.model,
        }),
        fieldId: `route-target-${t.key}-model`,
      });
    if (e.weight)
      summary.push({
        key: `${t.key}-weight`,
        message: translate("message_6a776812d7", {
          value0: label,
          value1: e.weight,
        }),
        fieldId: `route-target-${t.key}-weight`,
      });
  });
  if (strategy === RoutingStrategy.JEV) {
    const jevItems: [string, string | undefined, string][] = [
      [
        "jev-source",
        shown(jevErrors.source, true),
        decisionSource === "route" ? "route-jev-route" : "route-jev-connection",
      ],
      ["jev-service-model", jevErrors.serviceModel, "route-jev-service-model"],
      ["jev-timeout", jevErrors.timeout, "route-jev-timeout"],
      ["jev-pin", jevErrors.pin, "route-jev-pin"],
      ["quota-moderate", jevErrors.moderate, "route-quota-moderate"],
      ["quota-limited", jevErrors.limited, "route-quota-limited"],
      [
        "quota-nearly",
        jevErrors.nearlyExhausted,
        "route-quota-nearlyExhausted",
      ],
    ];
    for (const [key, message, fieldId] of jevItems)
      if (message) summary.push({ key, message, fieldId });
  }
  if (retentionErrors.percent)
    summary.push({
      key: "retention-percent",
      message: retentionErrors.percent,
      fieldId: "route-retention-percent",
    });
  if (retentionErrors.tokens)
    summary.push({
      key: "retention-tokens",
      message: retentionErrors.tokens,
      fieldId: "route-retention-tokens",
    });
  const canSave =
    !pending &&
    !aliasError &&
    !countError &&
    rowErrors.every((e) => !e.source && !e.model && !e.weight) &&
    validRetention &&
    (strategy !== RoutingStrategy.JEV || validJev);
  const updateTarget = (key: number, patch: Partial<TargetValue>) =>
    setTargets((old) =>
      old.map((x) => (x.key === key ? { ...x, ...patch } : x)),
    );
  return (
    <Dialog
      title={route ? translate("editRouting") : translate("createRoute")}
      subtitle={translate("onlyYoursIn", { value0: organizationName })}
      onClose={onClose}
      size={DialogSize.Large}
    >
      <form
        onChange={() => setTouched(true)}
        onSubmit={(e) => {
          e.preventDefault();
          if (!canSave) return;
          onSave({
            reasoningRetention: retention,
            modelId: trimmedName,
            jevDecision: strategy === RoutingStrategy.JEV ? jev : undefined,
            strategy,
            enabled,
            targets: targets.map(({ key: _key, ...t }, i) => ({
              ...t,
              priority: i,
            })),
          });
        }}
      >
        <section className="route-section" aria-labelledby="route-details">
          <h3 id="route-details">{translate("routeDetails")}</h3>
          <div className="field-grid">
            <Field
              label={translate("modelAlias")}
              error={shown(aliasError?.message, aliasError?.required ?? false)}
            >
              <input
                id="route-alias"
                autoFocus
                required
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
            <Field
              label={translate("routingStrategy")}
              hint={translate("retryBehaviorFollowsEachProviderSPolicy")}
            >
              <select
                value={strategy}
                onChange={(e) => {
                  const next = Number(e.target.value);
                  setStrategy(next);
                  if (next !== RoutingStrategy.JEV) {
                    setJev(create(JevDecisionConfigSchema));
                    setDecisionSource("connection");
                  }
                }}
              >
                {[
                  RoutingStrategy.DIRECT,
                  RoutingStrategy.FALLBACK,
                  RoutingStrategy.WEIGHTED,
                  RoutingStrategy.ROUND_ROBIN,
                  RoutingStrategy.JEV,
                ].map((s) => (
                  <option key={s} value={s}>
                    {s === RoutingStrategy.JEV
                      ? "JEV"
                      : enumLabel(RoutingStrategy[s])}
                  </option>
                ))}
              </select>
            </Field>
          </div>
          <label className="check">
            <input
              type="checkbox"
              checked={enabled}
              onChange={(e) => setEnabled(e.target.checked)}
            />
            {translate("enabled")}
          </label>
        </section>
        {strategy === RoutingStrategy.JEV && (
          <fieldset
            className="route-section jev-section"
            aria-describedby="route-jev-help"
          >
            <legend>{translate("jevDecisionSettings")}</legend>
            <p id="route-jev-help" className="section-hint">
              {translate("chooseAnIndependentPersonalDecisionServiceOr")}
            </p>
            <Field label={translate("decisionSource")}>
              <select
                id="route-jev-source"
                value={decisionSource}
                onChange={(e) => {
                  setDecisionSource(e.target.value);
                  setJev({
                    ...jev,
                    decisionConnectionId: "",
                    decisionModel: "",
                    decisionServiceModel: "",
                  });
                }}
              >
                <option value="connection">
                  {translate("connection_639a40")}
                </option>
                <option value="route">{translate("routeAlias")}</option>
              </select>
            </Field>
            {decisionSource === "connection" ? (
              <div className="field-grid">
                <Field
                  label={translate("decisionConnection")}
                  hint={translate("chooseAJevOrCustomDecisionService")}
                  error={shown(jevErrors.source, true)}
                >
                  <select
                    id="route-jev-connection"
                    required
                    value={jev.decisionConnectionId}
                    onChange={(e) =>
                      setJev({ ...jev, decisionConnectionId: e.target.value })
                    }
                  >
                    <option value="">{translate("chooseAConnection")}</option>
                    {jev.decisionConnectionId &&
                      !accounts.some(
                        (a) => a.id === jev.decisionConnectionId,
                      ) && (
                        <option value={jev.decisionConnectionId}>
                          {translate("savedDecisionConnectionUnavailable")}
                        </option>
                      )}
                    {accounts.map((a) => (
                      <option key={a.id} value={a.id}>
                        {a.name}
                        {a.state !== ConnectionState.READY
                          ? " (unavailable)"
                          : ""}
                      </option>
                    ))}
                  </select>
                </Field>
                <Field
                  label={translate("decisionServiceModel")}
                  hint={translate("customSystemoneBackendModelUpTo512")}
                  error={jevErrors.serviceModel}
                >
                  <input
                    id="route-jev-service-model"
                    maxLength={JevLimit.ServiceModelUnits}
                    value={jev.decisionServiceModel}
                    onChange={(e) =>
                      setJev({ ...jev, decisionServiceModel: e.target.value })
                    }
                  />
                </Field>
              </div>
            ) : (
              <Field
                label={translate("decisionRouteAlias")}
                hint={translate("onlyOrdinaryPersonalRoutesAreListedThe")}
                error={shown(jevErrors.source, true)}
              >
                <select
                  id="route-jev-route"
                  required
                  value={jev.decisionModel}
                  onChange={(e) =>
                    setJev({ ...jev, decisionModel: e.target.value })
                  }
                >
                  <option value="">
                    {translate("chooseAnOrdinaryPersonalRoute")}
                  </option>
                  {jev.decisionModel &&
                    !decisionRoutes.some(
                      (r) =>
                        r.modelId === jev.decisionModel &&
                        r.id !== route?.id &&
                        r.strategy !== RoutingStrategy.JEV,
                    ) && (
                      <option value={jev.decisionModel}>
                        {translate("savedRouteUnavailable", {
                          value0: jev.decisionModel,
                        })}
                      </option>
                    )}
                  {decisionRoutes
                    .filter(
                      (r) =>
                        r.id !== route?.id &&
                        r.strategy !== RoutingStrategy.JEV,
                    )
                    .map((r) => (
                      <option key={r.id} value={r.modelId}>
                        {r.modelId}
                        {!r.enabled ? " (disabled)" : ""}
                      </option>
                    ))}
                </select>
              </Field>
            )}
            <div className="field-grid">
              <Field
                label={translate("decisionTimeoutMs")}
                hint={translate("zeroUses4000MsOtherwise1000120000")}
                error={jevErrors.timeout}
              >
                <input
                  id="route-jev-timeout"
                  type="number"
                  min={0}
                  max={JevLimit.TimeoutMaxMs}
                  step={1}
                  value={jev.decisionTimeoutMs}
                  onChange={(e) =>
                    setJev({
                      ...jev,
                      decisionTimeoutMs: Number(e.target.value),
                    })
                  }
                />
              </Field>
              <Field
                label={translate("sessionPinDurationSeconds")}
                hint={translate("zeroDisablesPinningOtherwise6086400Seconds")}
                error={jevErrors.pin}
              >
                <input
                  id="route-jev-pin"
                  type="number"
                  min={0}
                  max={JevLimit.PinMaxSeconds}
                  step={1}
                  value={jev.sessionPinTtlSeconds}
                  onChange={(e) =>
                    setJev({
                      ...jev,
                      sessionPinTtlSeconds: Number(e.target.value),
                    })
                  }
                />
              </Field>
            </div>
            <Disclosure
              summary={translate("advisoryQuotaSignals")}
              forceOpen={
                !!(
                  jevErrors.moderate ||
                  jevErrors.limited ||
                  jevErrors.nearlyExhausted
                )
              }
            >
              <label className="check">
                <input
                  type="checkbox"
                  checked={jev.decisionQuotaSignals}
                  onChange={(e) =>
                    setJev({ ...jev, decisionQuotaSignals: e.target.checked })
                  }
                />
                {translate("enableAdvisoryQuotaSignals")}
              </label>
              <p className="section-hint">
                {translate("shareAvailableQuotaSignalsWithTheDecision")}
              </p>
              <label className="check">
                <input
                  type="checkbox"
                  checked={!!tiers}
                  onChange={(e) =>
                    setJev({
                      ...jev,
                      decisionQuotaTiers: e.target.checked
                        ? create(JevQuotaTiersSchema)
                        : undefined,
                    })
                  }
                />
                {translate("overrideQuotaThresholds")}
              </label>
              {tiers && (
                <>
                  <p className="section-hint">
                    {translate("thresholdsAreQuotaUsedInPercentFrom")}
                  </p>
                  <div className="field-grid tier-grid">
                    {(
                      [
                        [
                          "moderate",
                          translate("moderateQuotaUsed"),
                          translate("emptyNoModerateTier"),
                        ],
                        [
                          "limited",
                          translate("limitedQuotaUsed"),
                          translate("empty70"),
                        ],
                        [
                          "nearlyExhausted",
                          translate("nearlyExhaustedQuotaUsed"),
                          translate("empty90"),
                        ],
                      ] as const
                    ).map(([key, label, hint]) => (
                      <Field
                        key={key}
                        label={label}
                        hint={hint}
                        error={jevErrors[key]}
                      >
                        <input
                          id={`route-quota-${key}`}
                          type="number"
                          min={0}
                          max={100}
                          step="any"
                          value={tiers[key] ?? ""}
                          onChange={(e) =>
                            setJev({
                              ...jev,
                              decisionQuotaTiers: create(JevQuotaTiersSchema, {
                                ...tiers,
                                [key]:
                                  e.target.value === ""
                                    ? undefined
                                    : Number(e.target.value),
                              }),
                            })
                          }
                        />
                      </Field>
                    ))}
                  </div>
                </>
              )}
            </Disclosure>
          </fieldset>
        )}
        <section
          className="route-section"
          aria-labelledby="route-targets-heading"
          aria-describedby={countError ? "route-targets-error" : undefined}
          role="group"
        >
          <h3 id="route-targets-heading">{translate("orderedTargets")}</h3>
          <p className="section-hint">
            {strategy === RoutingStrategy.DIRECT
              ? translate("directRoutingUsesExactlyOneTarget")
              : strategy === RoutingStrategy.JEV
                ? translate("jevSupportsUpToTargets", {
                    value0: RouteLimit.MaxJevTargets,
                  })
                : translate("upToTargetsTargetsAreListedIn", {
                    value0: RouteLimit.MaxTargets,
                  })}
          </p>
          {targets.map((t, i) => {
            const rowId = `route-target-${t.key}`;
            const e = rowErrors[i]!;
            return (
              <fieldset className="target-card" key={t.key}>
                <legend>
                  {translate("disconnectScopeLine", {
                    value0: i + 1,
                    value1:
                      i === 0
                        ? translate("message_efe10c80ec")
                        : translate("fallback", { value0: i }),
                  })}
                </legend>
                <div className="field-grid">
                  <Field
                    label={translate("connectionOrPool")}
                    error={shown(e.source, e.sourceRequired)}
                  >
                    <select
                      id={`${rowId}-source`}
                      value={`${t.target.case}:${t.target.value}`}
                      required
                      onChange={(ev) => {
                        const [kind, ...value] = ev.target.value.split(":");
                        updateTarget(t.key, {
                          target: {
                            case: kind as "connectionId" | "poolId",
                            value: value.join(":"),
                          },
                        });
                      }}
                    >
                      <option value="">{translate("chooseATarget")}</option>
                      {!accounts.some(
                        (a) =>
                          t.target.case === "connectionId" &&
                          a.id === t.target.value,
                      ) &&
                        !pools.some(
                          (p) =>
                            t.target.case === "poolId" &&
                            p.id === t.target.value,
                        ) &&
                        t.target.value && (
                          <option value={`${t.target.case}:${t.target.value}`}>
                            {translate("unavailableTarget")}
                          </option>
                        )}
                      {accounts
                        .filter(
                          (a) =>
                            a.state === ConnectionState.READY ||
                            t.target.value === a.id,
                        )
                        .map((a) => (
                          <option key={a.id} value={`connectionId:${a.id}`}>
                            {a.state !== ConnectionState.READY
                              ? translate("unavailable", { value0: a.name })
                              : a.name}
                          </option>
                        ))}
                      {pools.map((p) => (
                        <option key={p.id} value={`poolId:${p.id}`}>
                          {translate("pool", { value0: p.name })}
                        </option>
                      ))}
                    </select>
                  </Field>
                  <Field
                    label={translate("providerModelId")}
                    error={shown(e.model, e.modelRequired)}
                  >
                    <input
                      id={`${rowId}-model`}
                      required
                      value={t.modelId}
                      onChange={(ev) =>
                        updateTarget(t.key, { modelId: ev.target.value })
                      }
                    />
                  </Field>
                </div>
                <Field
                  label={translate("weight_81d27e")}
                  hint={translate("wholeNumberFrom1To1000Weighted")}
                  error={e.weight}
                >
                  <input
                    id={`${rowId}-weight`}
                    type="number"
                    min={RouteLimit.MinWeight}
                    max={RouteLimit.MaxWeight}
                    value={t.weight}
                    onChange={(ev) =>
                      updateTarget(t.key, { weight: Number(ev.target.value) })
                    }
                  />
                </Field>
                <details>
                  <summary>
                    {translate("targetReasoningAndModelProfile")}
                  </summary>
                  <fieldset>
                    <legend>{translate("allowedReasoningEfforts")}</legend>
                    {Object.values(JevEffort).map((effort) => (
                      <label className="check" key={effort}>
                        <input
                          type="checkbox"
                          checked={t.reasoningEfforts.includes(effort)}
                          onChange={(ev) =>
                            updateTarget(t.key, {
                              reasoningEfforts: ev.target.checked
                                ? [...t.reasoningEfforts, effort]
                                : t.reasoningEfforts.filter(
                                    (v) => v !== effort,
                                  ),
                            })
                          }
                        />
                        {effort}
                      </label>
                    ))}
                  </fieldset>
                  <Field
                    label={translate("modelProfile")}
                    hint={translate(
                      "optionalModelDescriptionForJevDecisionsUp",
                    )}
                  >
                    <textarea
                      maxLength={512}
                      value={t.modelProfile}
                      onChange={(ev) =>
                        updateTarget(t.key, { modelProfile: ev.target.value })
                      }
                    />
                  </Field>
                </details>
                <div className="actions">
                  <Button
                    id={`${rowId}-up`}
                    disabled={i === 0}
                    onClick={() => {
                      setTouched(true);
                      setTargets((old) => {
                        const next = [...old];
                        [next[i - 1], next[i]] = [next[i]!, next[i - 1]!];
                        return next;
                      });
                      focusRequest.current =
                        i - 1 === 0 ? `${rowId}-remove` : `${rowId}-up`;
                      setAnnouncement(
                        translate("movedTargetUpToPosition", {
                          value0: i + 1,
                          value1: i,
                        }),
                      );
                    }}
                  >
                    {translate("moveUp")}
                  </Button>
                  <Button
                    id={`${rowId}-remove`}
                    onClick={() => {
                      setTouched(true);
                      const neighbor = targets[i + 1] ?? targets[i - 1];
                      focusRequest.current = neighbor
                        ? `route-target-${neighbor.key}-remove`
                        : "route-add-target";
                      setTargets((old) => old.filter((x) => x.key !== t.key));
                      setAnnouncement(
                        translate(
                          targets.length - 1 === 1
                            ? "removedTargetTargetRemaining"
                            : "removedTargetTargetsRemaining",
                          { value0: i + 1, value1: targets.length - 1 },
                        ),
                      );
                    }}
                  >
                    {translate("removeTarget")}
                  </Button>
                </div>
              </fieldset>
            );
          })}
          <Button
            id="route-add-target"
            aria-describedby={countError ? "route-targets-error" : undefined}
            onClick={() => {
              const key = nextKey.current++;
              setTouched(true);
              setTargets((old) => [
                ...old,
                {
                  key,
                  target: { case: "connectionId", value: "" },
                  modelId: "",
                  reasoningEfforts: [],
                  modelProfile: "",
                  priority: old.length,
                  weight: 1,
                },
              ]);
              focusRequest.current = `route-target-${key}-source`;
              setAnnouncement(
                translate("addedTarget", { value0: targets.length + 1 }),
              );
            }}
          >
            {translate("addTarget")}
          </Button>
          {shown(countError, true) && (
            <p id="route-targets-error" className="group-error">
              {countError}
            </p>
          )}
        </section>
        <Disclosure
          summary={translate("reasoningRetention")}
          forceOpen={!!(retentionErrors.percent || retentionErrors.tokens)}
        >
          <label className="check">
            <input
              type="checkbox"
              checked={retention !== undefined}
              onChange={(e) =>
                setRetention(
                  e.target.checked
                    ? create(ReasoningRetentionConfigSchema)
                    : undefined,
                )
              }
            />
            {translate("configureReasoningRetention")}
          </label>
          <p className="section-hint">
            {translate("removingThisConfigurationDisablesRetentionWhenSaved")}
          </p>
          {retention && (
            <>
              <label className="check">
                <input
                  type="checkbox"
                  checked={retention.enabled}
                  onChange={(e) =>
                    setRetention(
                      create(ReasoningRetentionConfigSchema, {
                        ...retention,
                        enabled: e.target.checked,
                      }),
                    )
                  }
                />
                {translate("retainReadableReasoningDuringCompaction")}
              </label>
              <div className="field-grid">
                <Field
                  label={translate("contextLimit")}
                  hint={translate("emptyOrZeroUses20OfThe")}
                  error={retentionErrors.percent}
                >
                  <input
                    id="route-retention-percent"
                    type="number"
                    min="0"
                    max="100"
                    step="any"
                    value={retention.maxContextPercent ?? ""}
                    onChange={(e) =>
                      setRetention(
                        create(ReasoningRetentionConfigSchema, {
                          ...retention,
                          maxContextPercent:
                            e.target.value === ""
                              ? undefined
                              : Number(e.target.value),
                        }),
                      )
                    }
                  />
                </Field>
                <Field
                  label={translate("tokenLimit")}
                  hint={translate("emptyOrZeroUses100000Tokens")}
                  error={retentionErrors.tokens}
                >
                  <input
                    id="route-retention-tokens"
                    type="number"
                    min="0"
                    max="4294967295"
                    step="1"
                    value={retention.maxTokens ?? ""}
                    onChange={(e) =>
                      setRetention(
                        create(ReasoningRetentionConfigSchema, {
                          ...retention,
                          maxTokens:
                            e.target.value === ""
                              ? undefined
                              : Number(e.target.value),
                        }),
                      )
                    }
                  />
                </Field>
              </div>
            </>
          )}
        </Disclosure>
        <p className="section-hint">
          {translate("savingReplacesTheCompleteRouteChangingAway")}
        </p>
        <ErrorSummary errors={summary} />
        {partial && (
          <Notice tone="warning">
            {translate("thisEditorShowsTheFirstConnectionsAnd", {
              value0: pageSize,
            })}
          </Notice>
        )}
        <ErrorNotice error={error} />
        <div
          className="visually-hidden"
          role="status"
          aria-live="polite"
          aria-atomic="true"
        >
          {pending ? translate("savingRouting") : announcement}
        </div>
        <div className="actions">
          <Button onClick={onClose}>{translate("cancel")}</Button>
          <Button type="submit" variant="primary" disabled={!canSave}>
            {translate("saveRouting")}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
