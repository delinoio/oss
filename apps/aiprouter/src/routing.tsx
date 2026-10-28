// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "./i18n";
import { pageHref } from "./routes";
import { useState } from "react";
import {
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
} from "@aiprouter/client";
import { useApi, type PageProps, pageSize } from "./api";
import { useAction } from "./actions";
import { usePersonalResources } from "./resources";
import {
  PageHeader,
  Button,
  Card,
  Field,
  Dialog,
  DialogSize,
  ConfirmDialog,
  ErrorNotice,
  Loading,
  EmptyState,
  Notice,
  Pagination,
  Badge,
  enumLabel,
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
  return (
    <>
      <PageHeader
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
          pool={edit === true ? undefined : edit}
          existingNames={(pools.data ?? [])
            .filter((p) => edit === true || p.id !== edit.id)
            .map((p) => p.name)}
          accounts={accounts.data ?? []}
          partial={false}
          error={action.error ?? accounts.error}
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
        <ConfirmDialog
          title={translate("delete_3d83de", { value0: remove.name })}
          confirmLabel={translate("deletePool")}
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
              () => setRemove(undefined),
              translate("poolDeleted"),
            );
          }}
        >
          {translate("connectionsRemainAvailableRoutesUsingThisPool")}
          <a href={pageHref("routing", organization.id)}>
            {translate("reviewRouting")}
          </a>
          .
        </ConfirmDialog>
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
function PoolEditor({
  existingNames,
  pool,
  accounts,
  partial,
  onClose,
  onSave,
  pending,
  error,
}: {
  existingNames: string[];
  pool?: Pool;
  accounts: ProviderConnection[];
  partial: boolean;
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
  return (
    <Dialog
      title={pool ? translate("editPool") : translate("createPool")}
      onClose={onClose}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          onSave({
            name: name.trim(),
            strategy,
            affinityEnabled,
            nativeOptions,
            members: members.map((m, i) => ({ ...m, priority: i })),
          });
        }}
      >
        <Field label={translate("poolName")}>
          <input
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
        <label className="check">
          <input
            type="checkbox"
            checked={affinityEnabled}
            onChange={(event) => setAffinityEnabled(event.target.checked)}
          />
          {translate("keepGenericOauthSessionsOnTheSame")}
        </label>
        <p className="muted">
          {translate("optInForProvidersThatSupportGeneric")}
        </p>
        <fieldset>
          <legend>{translate("yourConnectionsInPriorityOrder")}</legend>
          {members.map((m, i) => (
            <div key={m.connectionId} className="target-row">
              <strong>
                {accounts.find((a) => a.id === m.connectionId)?.name ??
                  translate("unavailableConnection")}
              </strong>
              <Field label={translate("weight_81d27e")}>
                <input
                  type="number"
                  min={1}
                  max={1000}
                  value={m.weight}
                  onChange={(e) =>
                    setMembers((old) =>
                      old.map((x, j) =>
                        j === i ? { ...x, weight: Number(e.target.value) } : x,
                      ),
                    )
                  }
                />
              </Field>
              <Button
                disabled={i === 0}
                onClick={() =>
                  setMembers((old) => {
                    const next = [...old];
                    [next[i - 1], next[i]] = [next[i], next[i - 1]];
                    return next;
                  })
                }
              >
                {translate("moveUp")}
              </Button>
              <Button
                onClick={() =>
                  setMembers((old) => old.filter((_, j) => j !== i))
                }
              >
                {translate("remove")}
              </Button>
            </div>
          ))}
          <Field label={translate("addAConnection")}>
            <select
              value=""
              onChange={(e) => {
                if (e.target.value)
                  setMembers((old) => [
                    ...old,
                    {
                      connectionId: e.target.value,
                      priority: old.length,
                      weight: 1,
                    },
                  ]);
              }}
            >
              <option value="">{translate("chooseYourConnection")}</option>
              {accounts
                .filter(
                  (a) =>
                    a.state === ConnectionState.READY &&
                    !members.some((m) => m.connectionId === a.id),
                )
                .map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                  </option>
                ))}
            </select>
          </Field>
        </fieldset>
        <NativePoolFields
          options={nativeOptions}
          onChange={setNativeOptions}
          members={members}
          onMembers={setMembers}
          accounts={accounts}
          isNew={!pool}
        />
        {partial && (
          <Notice tone="warning">
            {translate("onlyTheFirstConnectionsAreAvailableIn", {
              value0: pageSize,
            })}
          </Notice>
        )}
        <ErrorNotice error={error} />
        {existingNames.includes(name.trim()) && (
          <Notice tone="danger">
            {translate("thisNameIsAlreadyInUseChoose")}
          </Notice>
        )}
        <div className="actions">
          <Button onClick={onClose}>{translate("cancel")}</Button>
          <Button
            type="submit"
            variant="primary"
            disabled={
              pending ||
              existingNames.includes(name.trim()) ||
              !name.trim() ||
              !members.length ||
              !!nativePoolError(
                nativeOptions,
                members,
                nativeProvider(members, accounts),
              ) ||
              members.some(
                (m) => !accounts.some((a) => a.id === m.connectionId),
              )
            }
          >
            {translate("savePool")}
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
  return (
    <>
      <PageHeader
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
      <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      {query.isPending ? (
        <Loading />
      ) : query.data?.routes.length === 0 ? (
        <EmptyState title={translate("configureYourFirstModelRoute")}>
          {translate("addAProviderConnectionThenMapA")}
        </EmptyState>
      ) : (
        <div className="records">
          {query.data?.routes.map((route) => (
            <Card key={route.id} className="record">
              <div>
                <h2>{route.modelId}</h2>
                <Badge>
                  {route.enabled ? translate("enabled") : translate("disabled")}
                </Badge>
                <p>
                  {route.strategy === RoutingStrategy.JEV
                    ? "JEV"
                    : enumLabel(RoutingStrategy[route.strategy])}
                </p>
                <ol>
                  {route.targets
                    .toSorted((a, b) => a.priority - b.priority)
                    .map((t, i) => (
                      <li key={i}>
                        {translate("weight_4e5c69", {
                          value0:
                            t.target.case === "connectionId"
                              ? (accounts.data?.find(
                                  (c) => c.id === t.target.value,
                                )?.name ?? translate("unavailableConnection"))
                              : (pools.data?.find(
                                  (p) => p.id === t.target.value,
                                )?.name ?? translate("message_071b530243")),
                          value1: t.modelId,
                          value2: t.weight,
                        })}
                      </li>
                    ))}
                </ol>
              </div>
              <div className="actions">
                <Button onClick={() => setEdit(route)}>
                  {translate("editRoute")}
                </Button>
                <Button variant="danger" onClick={() => setRemove(route)}>
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
        <ConfirmDialog
          title={translate("deleteRoute", { value0: remove.modelId })}
          confirmLabel={translate("deleteRoute_032ad8")}
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
              () => setRemove(undefined),
              translate("routeDeleted"),
            );
          }}
        >
          {translate("newRequestsForThisAliasCanNo")}
        </ConfirmDialog>
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
  const validRetention =
    !retention ||
    ((retention.maxContextPercent === undefined ||
      (Number.isFinite(retention.maxContextPercent) &&
        retention.maxContextPercent >= 0 &&
        retention.maxContextPercent <= 100)) &&
      (retention.maxTokens === undefined ||
        (Number.isInteger(retention.maxTokens) &&
          retention.maxTokens >= 0 &&
          retention.maxTokens <= 4294967295)));
  const [decisionSource, setDecisionSource] = useState(
    route?.jevDecision?.decisionModel ? "route" : "connection",
  );
  const tiers = jev.decisionQuotaTiers;
  const effectiveLimited = tiers?.limited ?? 70;
  const effectiveNearlyExhausted = tiers?.nearlyExhausted ?? 90;
  const validQuota =
    [tiers?.moderate, tiers?.limited, tiers?.nearlyExhausted].every(
      (value) =>
        value === undefined ||
        (Number.isFinite(value) && value >= 0 && value <= 100),
    ) &&
    (tiers?.moderate === undefined || tiers.moderate < effectiveLimited) &&
    effectiveLimited < effectiveNearlyExhausted;
  const validJev =
    (jev.decisionTimeoutMs === 0 ||
      (jev.decisionTimeoutMs >= 1000 && jev.decisionTimeoutMs <= 120000)) &&
    Number.isInteger(jev.decisionTimeoutMs) &&
    (jev.sessionPinTtlSeconds === 0 ||
      (jev.sessionPinTtlSeconds >= 60 && jev.sessionPinTtlSeconds <= 86400)) &&
    Number.isInteger(jev.sessionPinTtlSeconds) &&
    (decisionSource === "route"
      ? !!jev.decisionModel
      : !!jev.decisionConnectionId);
  const [strategy, setStrategy] = useState(
    route?.strategy ?? RoutingStrategy.FALLBACK,
  );
  const [enabled, setEnabled] = useState(route?.enabled ?? true);
  const [targets, setTargets] = useState<TargetValue[]>(
    route?.targets
      .filter(
        (t): t is RouteTarget & { target: TargetValue["target"] } =>
          !!t.target.case,
      )
      .map((t) => ({
        target: t.target,
        modelId: t.modelId,
        reasoningEfforts: [...t.reasoningEfforts],
        modelProfile: t.modelProfile,
        priority: t.priority,
        weight: t.weight,
      }))
      .toSorted((a, b) => a.priority - b.priority) ?? [],
  );
  return (
    <Dialog
      title={route ? translate("editRouting") : translate("createRoute")}
      subtitle={translate("onlyYoursIn", { value0: organizationName })}
      onClose={onClose}
      size={DialogSize.Large}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          onSave({
            reasoningRetention: retention,
            modelId: name.trim(),
            jevDecision: strategy === RoutingStrategy.JEV ? jev : undefined,
            strategy,
            enabled,
            targets: targets.map((t, i) => ({ ...t, priority: i })),
          });
        }}
      >
        <Field label={translate("modelAlias")}>
          <input
            autoFocus
            required
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field label={translate("routingStrategy")}>
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
        <label className="check">
          <input
            type="checkbox"
            checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)}
          />
          {translate("enabled")}
        </label>
        {strategy === RoutingStrategy.JEV && (
          <fieldset>
            <legend>{translate("jevDecisionSettings")}</legend>
            <p>{translate("chooseAnIndependentPersonalDecisionServiceOr")}</p>
            <Field label={translate("decisionSource")}>
              <select
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
              <>
                <Field
                  label={translate("decisionConnection")}
                  hint={translate("chooseAJevOrCustomDecisionService")}
                >
                  <select
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
                  hint={translate("customSystemoneBackendModelTheCanonicalJev")}
                >
                  <input
                    maxLength={512}
                    value={jev.decisionServiceModel}
                    onChange={(e) =>
                      setJev({ ...jev, decisionServiceModel: e.target.value })
                    }
                  />
                </Field>
              </>
            ) : (
              <Field label={translate("decisionRouteAlias")}>
                <select
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
            <Field
              label={translate("decisionTimeoutMs")}
              hint={translate("zeroUses4000MsOtherwise1000120000")}
            >
              <input
                type="number"
                min={0}
                max={120000}
                step={1}
                value={jev.decisionTimeoutMs}
                onChange={(e) =>
                  setJev({ ...jev, decisionTimeoutMs: Number(e.target.value) })
                }
              />
            </Field>
            <Field
              label={translate("sessionPinDurationSeconds")}
              hint={translate("zeroDisablesPinningOtherwise6086400Seconds")}
            >
              <input
                type="number"
                min={0}
                max={86400}
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
            <details>
              <summary>{translate("advisoryQuotaSignals")}</summary>
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
              <p>{translate("shareAvailableQuotaSignalsWithTheDecision")}</p>
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
                  <p>{translate("thresholdsAreQuotaUsedInPercentEach")}</p>
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
                    <Field key={key} label={label} hint={hint}>
                      <input
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
                </>
              )}
              {!validQuota && (
                <Notice tone="warning">
                  {translate("useFinitePercentagesFrom0To100")}
                </Notice>
              )}
            </details>
            {!validJev && (
              <Notice tone="warning">
                {translate("selectADecisionSourceAndUseThe")}
              </Notice>
            )}
          </fieldset>
        )}
        <p>{translate("savingReplacesTheCompleteRouteChangingAway")}</p>
        <details>
          <summary>{translate("reasoningRetention")}</summary>
          <label>
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
          <p>
            {translate("removingThisConfigurationDisablesRetentionWhenSaved")}
          </p>
          {retention && (
            <>
              <label>
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
              <Field
                label={translate("contextLimit")}
                hint={translate("emptyOrZeroUses20OfThe")}
              >
                <input
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
              >
                <input
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
              {!validRetention && (
                <Notice tone="warning">
                  {translate("useAPercentageFrom0To100")}
                </Notice>
              )}
            </>
          )}
        </details>
        <h3>{translate("orderedTargets")}</h3>
        {targets.map((t, i) => (
          <fieldset key={i}>
            <legend>
              {i === 0
                ? translate("message_efe10c80ec")
                : translate("fallback", { value0: i })}
            </legend>
            <Field label={translate("connectionOrPool")}>
              <select
                value={`${t.target.case}:${t.target.value}`}
                required
                onChange={(e) => {
                  const [kind, ...value] = e.target.value.split(":");
                  setTargets((old) =>
                    old.map((x, j) =>
                      j === i
                        ? {
                            ...x,
                            target: {
                              case: kind as "connectionId" | "poolId",
                              value: value.join(":"),
                            },
                          }
                        : x,
                    ),
                  );
                }}
              >
                <option value="">{translate("chooseATarget")}</option>
                {!accounts.some(
                  (a) =>
                    t.target.case === "connectionId" && a.id === t.target.value,
                ) &&
                  !pools.some(
                    (p) =>
                      t.target.case === "poolId" && p.id === t.target.value,
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
                      {a.name}
                      {a.state !== ConnectionState.READY
                        ? " (unavailable)"
                        : ""}
                    </option>
                  ))}
                {pools.map((p) => (
                  <option key={p.id} value={`poolId:${p.id}`}>
                    {translate("pool", { value0: p.name })}
                  </option>
                ))}
              </select>
            </Field>
            <Field label={translate("providerModelId")}>
              <input
                required
                value={t.modelId}
                onChange={(e) =>
                  setTargets((old) =>
                    old.map((x, j) =>
                      j === i ? { ...x, modelId: e.target.value } : x,
                    ),
                  )
                }
              />
            </Field>
            <Field label={translate("weight_81d27e")}>
              <input
                type="number"
                min={1}
                max={1000}
                value={t.weight}
                onChange={(e) =>
                  setTargets((old) =>
                    old.map((x, j) =>
                      j === i ? { ...x, weight: Number(e.target.value) } : x,
                    ),
                  )
                }
              />
            </Field>
            <details>
              <summary>{translate("targetReasoningAndModelProfile")}</summary>
              <fieldset>
                <legend>{translate("allowedReasoningEfforts")}</legend>
                {Object.values(JevEffort).map((effort) => (
                  <label className="check" key={effort}>
                    <input
                      type="checkbox"
                      checked={t.reasoningEfforts.includes(effort)}
                      onChange={(e) =>
                        setTargets((old) =>
                          old.map((x, j) =>
                            j === i
                              ? {
                                  ...x,
                                  reasoningEfforts: e.target.checked
                                    ? [...x.reasoningEfforts, effort]
                                    : x.reasoningEfforts.filter(
                                        (v) => v !== effort,
                                      ),
                                }
                              : x,
                          ),
                        )
                      }
                    />
                    {effort}
                  </label>
                ))}
              </fieldset>
              <Field
                label={translate("modelProfile")}
                hint={translate("optionalModelDescriptionForJevDecisionsUp")}
              >
                <textarea
                  maxLength={512}
                  value={t.modelProfile}
                  onChange={(e) =>
                    setTargets((old) =>
                      old.map((x, j) =>
                        j === i ? { ...x, modelProfile: e.target.value } : x,
                      ),
                    )
                  }
                />
              </Field>
            </details>
            <div className="actions">
              <Button
                disabled={i === 0}
                onClick={() =>
                  setTargets((old) => {
                    const next = [...old];
                    [next[i - 1], next[i]] = [next[i], next[i - 1]];
                    return next;
                  })
                }
              >
                {translate("moveUp")}
              </Button>
              <Button
                onClick={() =>
                  setTargets((old) => old.filter((_, j) => j !== i))
                }
              >
                {translate("removeTarget")}
              </Button>
            </div>
          </fieldset>
        ))}
        <Button
          onClick={() =>
            setTargets((old) => [
              ...old,
              {
                target: { case: "connectionId", value: "" },
                modelId: "",
                reasoningEfforts: [],
                modelProfile: "",
                priority: old.length,
                weight: 1,
              },
            ])
          }
        >
          {translate("addTarget")}
        </Button>
        {strategy === RoutingStrategy.DIRECT && targets.length !== 1 && (
          <Notice tone="warning">
            {translate("directRoutingNeedsExactlyOneTarget")}
          </Notice>
        )}
        {partial && (
          <Notice tone="warning">
            {translate("thisEditorShowsTheFirstConnectionsAnd", {
              value0: pageSize,
            })}
          </Notice>
        )}
        <ErrorNotice error={error} />
        {existingNames.includes(name.trim()) && (
          <Notice tone="danger">
            {translate("thisNameIsAlreadyInUseChoose")}
          </Notice>
        )}
        <div className="actions">
          <Button onClick={onClose}>{translate("cancel")}</Button>
          <Button
            type="submit"
            variant="primary"
            disabled={
              pending ||
              !validRetention ||
              !name.trim() ||
              existingNames.includes(name.trim()) ||
              !targets.length ||
              (strategy === RoutingStrategy.JEV &&
                (!validJev || !validQuota || targets.length > 64)) ||
              targets.some((t) =>
                t.target.case === "connectionId"
                  ? !accounts.some((a) => a.id === t.target.value)
                  : !pools.some((p) => p.id === t.target.value),
              ) ||
              targets.some((t) => !t.target.value || !t.modelId.trim()) ||
              (strategy === RoutingStrategy.DIRECT && targets.length !== 1)
            }
          >
            {translate("saveRouting")}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
