// SPDX-License-Identifier: Apache-2.0
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
        title="Account pools"
        description={`Only yours in ${organization.name}.`}
        action={
          <Button variant="primary" onClick={() => setEdit(true)}>
            Create pool
          </Button>
        }
      />
      <Notice>
        Pools group your own connections. Strategy and provider policies
        determine which account serves a request.
      </Notice>
      {action.feedback}
      <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      {query.isPending ? (
        <Loading />
      ) : query.data?.pools.length === 0 ? (
        <EmptyState title="Create your first pool">
          Connect providers before adding them to a pool.
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
                        {accounts.data?.find((c) => c.id === m.connectionId)
                          ?.name ??
                          `Unavailable connection (${m.connectionId})`}{" "}
                        · Weight {m.weight}
                      </li>
                    ))}
                </ol>
              </div>
              <div className="actions">
                <Button onClick={() => setEdit(pool)}>Edit pool</Button>
                <Button variant="danger" onClick={() => setRemove(pool)}>
                  Delete
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
          title={`Delete ${remove.name}?`}
          confirmLabel="Delete pool"
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
              "Pool deleted.",
            );
          }}
        >
          Connections remain available. Routes using this pool must be repaired
          before future requests can use them.{" "}
          <a href={pageHref("routing", organization.id)}>Review routing</a>.
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
    <Dialog title={pool ? "Edit pool" : "Create pool"} onClose={onClose}>
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
        <Field label="Pool name">
          <input
            required
            autoFocus
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field label="Strategy">
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
          Keep generic OAuth sessions on the same account
        </label>
        <p className="muted">
          Opt in for providers that support generic OAuth session affinity.
          Codex and Anthropic retain their provider-specific session rules.
        </p>
        <fieldset>
          <legend>Your connections, in priority order</legend>
          {members.map((m, i) => (
            <div key={m.connectionId} className="target-row">
              <strong>
                {accounts.find((a) => a.id === m.connectionId)?.name ??
                  "Unavailable connection"}
              </strong>
              <Field label="Weight">
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
                Move up
              </Button>
              <Button
                onClick={() =>
                  setMembers((old) => old.filter((_, j) => j !== i))
                }
              >
                Remove
              </Button>
            </div>
          ))}
          <Field label="Add a connection">
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
              <option value="">Choose your connection</option>
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
            Only the first {pageSize} connections are available in this editor.
          </Notice>
        )}
        <ErrorNotice error={error} />
        {existingNames.includes(name.trim()) && (
          <Notice tone="danger">
            This name is already in use. Choose a different name.
          </Notice>
        )}
        <div className="actions">
          <Button onClick={onClose}>Cancel</Button>
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
            Save pool
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
export function RoutingPage({ organization }: PageProps) {
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
        title="Your routing"
        description={`Only yours in ${organization.name}.`}
        action={
          <Button variant="primary" onClick={() => setEdit(true)}>
            Create route
          </Button>
        }
      />
      <Notice>
        Model aliases choose your primary and fallback connections. Retry
        behavior follows each provider's policy.
      </Notice>
      {action.feedback}
      <ErrorNotice error={query.error} retry={() => void query.refetch()} />
      {query.isPending ? (
        <Loading />
      ) : query.data?.routes.length === 0 ? (
        <EmptyState title="Configure your first model route">
          Add a provider connection, then map a model alias to it.
        </EmptyState>
      ) : (
        <div className="records">
          {query.data?.routes.map((route) => (
            <Card key={route.id} className="record">
              <div>
                <h2>{route.modelId}</h2>
                <Badge>{route.enabled ? "Enabled" : "Disabled"}</Badge>
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
                        {t.target.case === "connectionId"
                          ? (accounts.data?.find((c) => c.id === t.target.value)
                              ?.name ?? "Unavailable connection")
                          : (pools.data?.find((p) => p.id === t.target.value)
                              ?.name ?? "Unavailable pool")}{" "}
                        → {t.modelId} · Weight {t.weight}
                      </li>
                    ))}
                </ol>
              </div>
              <div className="actions">
                <Button onClick={() => setEdit(route)}>Edit route</Button>
                <Button variant="danger" onClick={() => setRemove(route)}>
                  Delete
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
          title={`Delete route ${remove.modelId}?`}
          confirmLabel="Delete route"
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
              "Route deleted.",
            );
          }}
        >
          New requests for this alias can no longer use this route. Provider
          connections and pools remain available.
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
}) {
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
      title={route ? "Edit routing" : "Create route"}
      onClose={onClose}
      wide
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
        <Field label="Model alias">
          <input
            autoFocus
            required
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field label="Routing strategy">
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
          Enabled
        </label>
        {strategy === RoutingStrategy.JEV && (
          <fieldset>
            <legend>JEV decision settings</legend>
            <p>
              Choose an independent personal decision service or an ordinary
              routing model. Decision routes cannot use JEV chains or cycles.
            </p>
            <Field label="Decision source">
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
                <option value="connection">Connection</option>
                <option value="route">Route alias</option>
              </select>
            </Field>
            {decisionSource === "connection" ? (
              <>
                <Field
                  label="Decision connection"
                  hint="Choose a JEV or custom decision-service connection. The server checks eligibility."
                >
                  <select
                    required
                    value={jev.decisionConnectionId}
                    onChange={(e) =>
                      setJev({ ...jev, decisionConnectionId: e.target.value })
                    }
                  >
                    <option value="">Choose a connection</option>
                    {jev.decisionConnectionId &&
                      !accounts.some(
                        (a) => a.id === jev.decisionConnectionId,
                      ) && (
                        <option value={jev.decisionConnectionId}>
                          Saved decision connection unavailable
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
                  label="Decision service model"
                  hint="Custom SystemOne backend model. The canonical JEV service uses jev-latest."
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
              <Field label="Decision route alias">
                <select
                  required
                  value={jev.decisionModel}
                  onChange={(e) =>
                    setJev({ ...jev, decisionModel: e.target.value })
                  }
                >
                  <option value="">Choose an ordinary personal route</option>
                  {jev.decisionModel &&
                    !decisionRoutes.some(
                      (r) =>
                        r.modelId === jev.decisionModel &&
                        r.id !== route?.id &&
                        r.strategy !== RoutingStrategy.JEV,
                    ) && (
                      <option value={jev.decisionModel}>
                        Saved route unavailable ({jev.decisionModel})
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
              label="Decision timeout (ms)"
              hint="Zero uses 4000 ms. Otherwise 1000–120000 ms."
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
              label="Session pin duration (seconds)"
              hint="Zero disables pinning. Otherwise 60–86400 seconds."
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
              <summary>Advisory quota signals</summary>
              <label className="check">
                <input
                  type="checkbox"
                  checked={jev.decisionQuotaSignals}
                  onChange={(e) =>
                    setJev({ ...jev, decisionQuotaSignals: e.target.checked })
                  }
                />{" "}
                Enable advisory quota signals
              </label>
              <p>
                Share available quota signals with the decision service. Unknown
                quota remains unknown; it is not treated as exhausted.
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
                />{" "}
                Override quota thresholds
              </label>
              {tiers && (
                <>
                  <p>
                    Thresholds are quota used, in percent. Each tier starts at
                    its threshold. Overrides alone do not enable signals and
                    must remain valid when signals are off.
                  </p>
                  {(
                    [
                      [
                        "moderate",
                        "Moderate quota used (%)",
                        "Empty: no moderate tier.",
                      ],
                      ["limited", "Limited quota used (%)", "Empty: 70%."],
                      [
                        "nearlyExhausted",
                        "Nearly exhausted quota used (%)",
                        "Empty: 90%.",
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
                  Use finite percentages from 0 to 100, with moderate (if set)
                  below limited, and limited below nearly exhausted. Empty
                  limited and nearly exhausted values use 70 and 90.
                </Notice>
              )}
            </details>
            {!validJev && (
              <Notice tone="warning">
                Select a decision source and use the allowed timeout and pin
                duration.
              </Notice>
            )}
          </fieldset>
        )}
        <p>
          Saving replaces the complete route. Changing away from JEV removes its
          decision configuration.
        </p>
        <details>
          <summary>Reasoning retention</summary>
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
            Configure reasoning retention
          </label>
          <p>Removing this configuration disables retention when saved.</p>
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
                Retain readable reasoning during compaction
              </label>
              <Field
                label="Context limit (%)"
                hint="Empty or zero uses 20% of the model context window."
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
                label="Token limit"
                hint="Empty or zero uses 100,000 tokens."
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
                  Use a percentage from 0 to 100 and a whole token limit from 0
                  to 4,294,967,295.
                </Notice>
              )}
            </>
          )}
        </details>
        <h3>Ordered targets</h3>
        {targets.map((t, i) => (
          <fieldset key={i}>
            <legend>{i === 0 ? "Primary" : `Fallback ${i}`}</legend>
            <Field label="Connection or pool">
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
                <option value="">Choose a target</option>
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
                      Unavailable target
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
                    Pool: {p.name}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="Provider model ID">
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
            <Field label="Weight">
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
              <summary>Target reasoning and model profile</summary>
              <fieldset>
                <legend>Allowed reasoning efforts</legend>
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
                label="Model profile"
                hint="Optional model description for JEV decisions, up to 512 characters. Do not include credentials or request content."
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
                Move up
              </Button>
              <Button
                onClick={() =>
                  setTargets((old) => old.filter((_, j) => j !== i))
                }
              >
                Remove target
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
          Add target
        </Button>
        {strategy === RoutingStrategy.DIRECT && targets.length !== 1 && (
          <Notice tone="warning">
            Direct routing needs exactly one target.
          </Notice>
        )}
        {partial && (
          <Notice tone="warning">
            This editor shows the first {pageSize} connections and pools.
          </Notice>
        )}
        <ErrorNotice error={error} />
        {existingNames.includes(name.trim()) && (
          <Notice tone="danger">
            This name is already in use. Choose a different name.
          </Notice>
        )}
        <div className="actions">
          <Button onClick={onClose}>Cancel</Button>
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
            Save routing
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
