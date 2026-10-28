// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "./i18n";
import { pageHref } from "./routes";
import { useEffect, useRef, useState } from "react";
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
import { utf8ByteLength } from "./validation";
import {
  PageHeader,
  Button,
  Card,
  Field,
  Dialog,
  DialogSize,
  ConfirmDialog,
  ErrorNotice,
  ErrorSummary,
  type ErrorSummaryItem,
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
  if (strategy === RoutingStrategy.JEV && !validJev)
    summary.push({
      key: "jev",
      message: translate("selectADecisionSourceAndUseThe"),
    });
  if (strategy === RoutingStrategy.JEV && !validQuota)
    summary.push({
      key: "quota",
      message: translate("useValidQuotaThresholdsModerateBelowLimited"),
    });
  if (!validRetention)
    summary.push({
      key: "retention",
      message: translate("useARetentionContextPercentageFrom0"),
    });
  const canSave =
    !pending &&
    !aliasError &&
    !countError &&
    rowErrors.every((e) => !e.source && !e.model && !e.weight) &&
    validRetention &&
    (strategy !== RoutingStrategy.JEV || (validJev && validQuota));
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
        <p>{translate("savingReplacesTheCompleteRouteChangingAway")}</p>
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
