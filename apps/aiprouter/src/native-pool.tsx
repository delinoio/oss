// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import {
  NativePoolOptionsSchema,
  NativePoolMemberOptionsSchema,
  NativeModelRouteSchema,
  NativePoolStrategy,
  NativeQuotaWindow,
  NativePinnedTransientPolicy,
  ProviderAuthKind,
  type NativePoolOptions,
  type NativePoolMemberOptions,
  type ProviderConnection,
} from "@aiprouter/client";
import { Button, Field, Notice, enumLabel } from "./ui";
export type NativeMember = {
  connectionId: string;
  priority: number;
  weight: number;
  nativeOptions?: NativePoolMemberOptions;
};
export function nativeProvider(
  members: NativeMember[],
  accounts: ProviderConnection[],
) {
  const selected = members.map((m) =>
    accounts.find((a) => a.id === m.connectionId),
  );
  const first = selected[0];
  if (
    !first ||
    !["openai", "anthropic", "anthropic2"].includes(first.providerId) ||
    selected.some(
      (a) =>
        !a ||
        a.authKind !== ProviderAuthKind.OAUTH ||
        a.providerId !== first.providerId,
    )
  )
    return undefined;
  return first.providerId;
}
export function nativePoolError(
  options: NativePoolOptions | undefined,
  members: NativeMember[],
  provider: string | undefined,
): string | undefined {
  if (!options && !members.some((m) => m.nativeOptions)) return;
  if (!provider)
    return "Native settings require OAuth connections from one provider: OpenAI, Anthropic, or Anthropic 2. These providers cannot be mixed.";
  const bounded = (n: number | undefined, min: number, max: number) =>
    n === undefined || (Number.isInteger(n) && n >= min && n <= max);
  for (const m of members) {
    const o = m.nativeOptions;
    if (
      o &&
      (!bounded(o.autoSwitchThreshold, 0, 100) ||
        !bounded(o.priority, -100, 100) ||
        (o.allowCredits && provider !== "openai"))
    )
      return "Check member thresholds, native priority, and provider-specific credit settings.";
  }
  if (!options) return;
  if (!bounded(options.upstreamFailoverThreshold, 0, 20))
    return "Upstream failover threshold must be 0–20, or empty for the default of 3.";
  if (
    options.pinnedTransientPolicy !== undefined &&
    ![
      NativePinnedTransientPolicy.UNSPECIFIED,
      NativePinnedTransientPolicy.KEEP,
      NativePinnedTransientPolicy.DETOUR_NEW_THREADS,
    ].includes(options.pinnedTransientPolicy)
  )
    return "Choose a supported pinned transient policy.";
  if (
    provider !== "openai" &&
    (options.upstreamFailoverThreshold !== undefined ||
      options.failureWindow !== undefined ||
      options.pinnedTransientPolicy !== undefined)
  )
    return "Upstream failover, failure window, and pinned transient policy require a Codex pool. Clear incompatible settings before saving.";

  if (
    !bounded(options.stickyLimit, 1, 100) ||
    !bounded(options.autoSwitchThreshold, 0, 100)
  )
    return "Sticky limit must be 1–100 and switching threshold 0–100, or empty for defaults.";
  if (
    provider === "openai" &&
    (options.quotaWindow !== NativeQuotaWindow.UNSPECIFIED ||
      options.nativeMessages !== undefined ||
      options.routes.length)
  )
    return "Quota windows, native messages, and model rules require an Anthropic pool. Clear incompatible settings before saving.";
  if (
    provider !== "openai" &&
    (options.strategy === NativePoolStrategy.RESET_FIRST ||
      options.cacheAffinity !== undefined ||
      options.excludedPlans.length)
  )
    return "Reset-first, cache affinity, and excluded plans require an OpenAI pool. Clear incompatible settings before saving.";
  if (
    options.excludedPlans.length > 100 ||
    new Set(options.excludedPlans.map((p) => p.toLowerCase())).size !==
      options.excludedPlans.length ||
    options.excludedPlans.some((p) => !p.trim() || p.length > 128)
  )
    return "Use up to 100 nonempty unique excluded plans, each at most 128 characters (case-insensitive).";
  if (
    options.routes.length > 32 ||
    new Set(options.routes.map((r) => r.name)).size !== options.routes.length ||
    new Set(options.routes.map((r) => r.match)).size !== options.routes.length
  )
    return "Use at most 32 model rules with unique names and patterns.";
  for (const r of options.routes) {
    if (
      !/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(r.name) ||
      !r.match ||
      r.match.length > 128 ||
      !/^[A-Za-z0-9_./:*?-]+$/.test(r.match) ||
      r.connectionIds.length < 1 ||
      r.connectionIds.length > 32 ||
      new Set(r.connectionIds).size !== r.connectionIds.length ||
      r.connectionIds.some((id) => !members.some((m) => m.connectionId === id))
    )
      return "Each model rule needs a safe unique name, a valid model pattern, and 1–32 distinct current pool connections.";
  }
}
function OptionalNumber({
  label,
  value,
  onChange,
  min,
  max,
  hint,
}: {
  label: string;
  value?: number;
  onChange: (n: number | undefined) => void;
  min: number;
  max: number;
  hint: string;
}) {
  return (
    <Field label={label} hint={hint}>
      <input
        type="number"
        min={min}
        max={max}
        step={1}
        value={value ?? ""}
        onChange={(e) =>
          onChange(e.target.value === "" ? undefined : Number(e.target.value))
        }
      />
    </Field>
  );
}
function OptionalBoolean({
  label,
  value,
  onChange,
  hint,
}: {
  label: string;
  value?: boolean;
  onChange: (n: boolean | undefined) => void;
  hint: string;
}) {
  return (
    <Field label={label} hint={hint}>
      <select
        value={value === undefined ? "" : String(value)}
        onChange={(e) =>
          onChange(
            e.target.value === "" ? undefined : e.target.value === "true",
          )
        }
      >
        <option value="">Default</option>
        <option value="true">Enabled</option>
        <option value="false">Disabled</option>
      </select>
    </Field>
  );
}
export function NativePoolFields({
  options,
  onChange,
  members,
  onMembers,
  accounts,
  isNew,
}: {
  options?: NativePoolOptions;
  onChange: (o: NativePoolOptions | undefined) => void;
  members: NativeMember[];
  onMembers: (m: NativeMember[]) => void;
  accounts: ProviderConnection[];
  isNew: boolean;
}) {
  const provider = nativeProvider(members, accounts),
    codex = provider === "openai";
  const error = nativePoolError(options, members, provider);
  const name = (id: string) =>
    accounts.find((a) => a.id === id)?.name ?? "Unavailable connection";
  return (
    <details>
      <summary>Native OAuth pool settings</summary>
      <p>
        Applies to pools containing OAuth connections from exactly one provider:
        OpenAI (Codex), Anthropic, or Anthropic 2. Generic session affinity
        above remains separate.
      </p>
      {!provider && (
        <Notice>
          Add connections from one supported OAuth provider to configure native
          pooling.
        </Notice>
      )}
      <label className="check">
        <input
          type="checkbox"
          disabled={!provider && !options}
          checked={!!options}
          onChange={(e) =>
            onChange(
              e.target.checked ? create(NativePoolOptionsSchema) : undefined,
            )
          }
        />{" "}
        Configure pool-level native settings
      </label>
      {options && provider && (
        <>
          <OptionalBoolean
            label="Native pooling"
            value={options.enabled}
            hint={
              isNew
                ? "Default enables native pooling when this pool is created. Choose Disabled to opt out."
                : "An unspecified saved value is disabled. Choose Enabled to opt in."
            }
            onChange={(enabled) => onChange({ ...options, enabled })}
          />
          <Field label="Native selection strategy">
            <select
              value={options.strategy}
              onChange={(e) =>
                onChange({ ...options, strategy: Number(e.target.value) })
              }
            >
              <option value={NativePoolStrategy.UNSPECIFIED}>
                Default (quota)
              </option>
              {[
                NativePoolStrategy.QUOTA,
                NativePoolStrategy.ROUND_ROBIN,
                NativePoolStrategy.FILL_FIRST,
                ...(codex ? [NativePoolStrategy.RESET_FIRST] : []),
              ].map((s) => (
                <option key={s} value={s}>
                  {enumLabel(NativePoolStrategy[s])}
                </option>
              ))}
            </select>
          </Field>
          <OptionalNumber
            label="Sticky account limit"
            value={options.stickyLimit}
            min={1}
            max={100}
            hint="Empty uses 1."
            onChange={(stickyLimit) => onChange({ ...options, stickyLimit })}
          />
          <OptionalNumber
            label="Pool switching threshold (%)"
            value={options.autoSwitchThreshold}
            min={0}
            max={100}
            hint="Empty uses 80. Zero disables voluntary quota switching."
            onChange={(autoSwitchThreshold) =>
              onChange({ ...options, autoSwitchThreshold })
            }
          />
          {codex ? (
            <>
              <OptionalNumber
                label="Upstream failover threshold"
                value={options.upstreamFailoverThreshold}
                min={0}
                max={20}
                hint="Empty uses 3. Zero disables upstream-failure-based failover."
                onChange={(upstreamFailoverThreshold) =>
                  onChange({ ...options, upstreamFailoverThreshold })
                }
              />
              <OptionalBoolean
                label="Failure window"
                value={options.failureWindow}
                hint="Default: enabled. Uses a sliding 60-second failure ratio; Disabled uses only the consecutive-failure counter."
                onChange={(failureWindow) =>
                  onChange({ ...options, failureWindow })
                }
              />
              <Field
                label="Pinned transient policy"
                hint="Empty uses Keep. Detour applies only to new threads on a degraded manually pinned account; existing bound threads are unchanged."
              >
                <select
                  value={options.pinnedTransientPolicy ?? ""}
                  onChange={(e) =>
                    onChange({
                      ...options,
                      pinnedTransientPolicy:
                        e.target.value === ""
                          ? undefined
                          : Number(e.target.value),
                    })
                  }
                >
                  <option value="">Default (keep)</option>
                  {options.pinnedTransientPolicy ===
                    NativePinnedTransientPolicy.UNSPECIFIED && (
                    <option value={NativePinnedTransientPolicy.UNSPECIFIED}>
                      Unspecified (keep)
                    </option>
                  )}
                  <option value={NativePinnedTransientPolicy.KEEP}>Keep</option>
                  <option
                    value={NativePinnedTransientPolicy.DETOUR_NEW_THREADS}
                  >
                    Detour new threads
                  </option>
                </select>
              </Field>
              <OptionalBoolean
                label="Cache affinity"
                value={options.cacheAffinity}
                hint="Default: enabled for Codex."
                onChange={(cacheAffinity) =>
                  onChange({ ...options, cacheAffinity })
                }
              />
              <Field
                label="Excluded plans"
                hint="One plan per line, up to 100. Names are case-insensitive."
              >
                <textarea
                  value={options.excludedPlans.join("\n")}
                  onChange={(e) =>
                    onChange({
                      ...options,
                      excludedPlans: e.target.value
                        ? e.target.value.split("\n")
                        : [],
                    })
                  }
                />
              </Field>
            </>
          ) : (
            <>
              <Field label="Native quota window">
                <select
                  value={options.quotaWindow}
                  onChange={(e) =>
                    onChange({
                      ...options,
                      quotaWindow: Number(e.target.value),
                    })
                  }
                >
                  <option value={NativeQuotaWindow.UNSPECIFIED}>
                    Default (five-hour)
                  </option>
                  {[
                    NativeQuotaWindow.FIVE_HOUR,
                    NativeQuotaWindow.WEEKLY,
                    NativeQuotaWindow.MAX_UTILIZATION,
                  ].map((w) => (
                    <option key={w} value={w}>
                      {enumLabel(NativeQuotaWindow[w])}
                    </option>
                  ))}
                </select>
              </Field>
              <OptionalBoolean
                label="Native messages"
                value={options.nativeMessages}
                hint="Default: enabled. Disabled uses legacy translation."
                onChange={(nativeMessages) =>
                  onChange({ ...options, nativeMessages })
                }
              />
              <h3>Ordered model rules</h3>
              <p>
                The first matching rule selects an ordered subset of this pool.
                Rules apply only while native pooling is enabled.
              </p>
              {options.routes.map((route, index) => (
                <fieldset key={index}>
                  <legend>Model rule {index + 1}</legend>
                  <Field
                    label={`Rule name ${index + 1}`}
                    hint="Letters, digits, underscores, or hyphens; start with a letter or digit."
                  >
                    <input
                      required
                      maxLength={64}
                      pattern="[A-Za-z0-9][A-Za-z0-9_-]*"
                      value={route.name}
                      onChange={(e) =>
                        onChange({
                          ...options,
                          routes: options.routes.map((r, i) =>
                            i === index ? { ...r, name: e.target.value } : r,
                          ),
                        })
                      }
                    />
                  </Field>
                  <Field
                    label={`Model pattern ${index + 1}`}
                    hint="Use * and ? as wildcards; maximum 128 characters."
                  >
                    <input
                      required
                      maxLength={128}
                      value={route.match}
                      onChange={(e) =>
                        onChange({
                          ...options,
                          routes: options.routes.map((r, i) =>
                            i === index ? { ...r, match: e.target.value } : r,
                          ),
                        })
                      }
                    />
                  </Field>
                  <ol>
                    {route.connectionIds.map((id, j) => (
                      <li key={id}>
                        {name(id)}
                        <div className="actions">
                          <Button
                            disabled={j === 0}
                            onClick={() => {
                              const ids = [...route.connectionIds];
                              [ids[j - 1], ids[j]] = [ids[j], ids[j - 1]];
                              onChange({
                                ...options,
                                routes: options.routes.map((r, i) =>
                                  i === index
                                    ? { ...r, connectionIds: ids }
                                    : r,
                                ),
                              });
                            }}
                          >
                            Move connection up
                          </Button>
                          <Button
                            onClick={() =>
                              onChange({
                                ...options,
                                routes: options.routes.map((r, i) =>
                                  i === index
                                    ? {
                                        ...r,
                                        connectionIds: r.connectionIds.filter(
                                          (_, k) => k !== j,
                                        ),
                                      }
                                    : r,
                                ),
                              })
                            }
                          >
                            Remove rule connection
                          </Button>
                        </div>
                      </li>
                    ))}
                  </ol>
                  <Field label={`Add rule connection ${index + 1}`}>
                    <select
                      value=""
                      disabled={route.connectionIds.length >= 32}
                      onChange={(e) => {
                        if (e.target.value)
                          onChange({
                            ...options,
                            routes: options.routes.map((r, i) =>
                              i === index
                                ? {
                                    ...r,
                                    connectionIds: [
                                      ...r.connectionIds,
                                      e.target.value,
                                    ],
                                  }
                                : r,
                            ),
                          });
                      }}
                    >
                      <option value="">Choose a pool connection</option>
                      {members
                        .filter(
                          (m) => !route.connectionIds.includes(m.connectionId),
                        )
                        .map((m) => (
                          <option key={m.connectionId} value={m.connectionId}>
                            {name(m.connectionId)}
                          </option>
                        ))}
                    </select>
                  </Field>
                  <label className="check">
                    <input
                      type="checkbox"
                      checked={route.fallback}
                      onChange={(e) =>
                        onChange({
                          ...options,
                          routes: options.routes.map((r, i) =>
                            i === index
                              ? { ...r, fallback: e.target.checked }
                              : r,
                          ),
                        })
                      }
                    />{" "}
                    Use the full pool only when no listed connection is eligible
                  </label>
                  <div className="actions">
                    <Button
                      disabled={index === 0}
                      onClick={() => {
                        const routes = [...options.routes];
                        [routes[index - 1], routes[index]] = [
                          routes[index],
                          routes[index - 1],
                        ];
                        onChange({ ...options, routes });
                      }}
                    >
                      Move rule up
                    </Button>
                    <Button
                      onClick={() =>
                        onChange({
                          ...options,
                          routes: options.routes.filter((_, i) => i !== index),
                        })
                      }
                    >
                      Remove model rule {index + 1}
                    </Button>
                  </div>
                </fieldset>
              ))}
              <Button
                disabled={options.routes.length >= 32}
                onClick={() =>
                  onChange({
                    ...options,
                    routes: [...options.routes, create(NativeModelRouteSchema)],
                  })
                }
              >
                Add model rule
              </Button>
            </>
          )}
        </>
      )}
      {provider && (
        <>
          <h3>Member native options</h3>
          {members.map((member, index) => {
            const current = member.nativeOptions;
            const update = (
              nativeOptions: NativePoolMemberOptions | undefined,
            ) =>
              onMembers(
                members.map((m, i) =>
                  i === index ? { ...m, nativeOptions } : m,
                ),
              );
            return (
              <fieldset key={member.connectionId}>
                <legend>{name(member.connectionId)}</legend>
                <label className="check">
                  <input
                    type="checkbox"
                    checked={!!current}
                    onChange={(e) =>
                      update(
                        e.target.checked
                          ? create(NativePoolMemberOptionsSchema)
                          : undefined,
                      )
                    }
                  />{" "}
                  Override native options for {name(member.connectionId)}
                </label>
                {current && (
                  <>
                    <OptionalNumber
                      label={`Switching threshold for ${name(member.connectionId)} (%)`}
                      value={current.autoSwitchThreshold}
                      min={0}
                      max={100}
                      hint="Empty inherits the pool threshold. Zero disables voluntary quota switching."
                      onChange={(autoSwitchThreshold) =>
                        update({ ...current, autoSwitchThreshold })
                      }
                    />
                    <OptionalNumber
                      label={`Native priority for ${name(member.connectionId)}`}
                      value={current.priority}
                      min={-100}
                      max={100}
                      hint="Higher values come first. Separate from the pool member order above."
                      onChange={(priority) => update({ ...current, priority })}
                    />
                    <label className="check">
                      <input
                        type="checkbox"
                        checked={current.useRemainingQuota}
                        onChange={(e) =>
                          update({
                            ...current,
                            useRemainingQuota: e.target.checked,
                          })
                        }
                      />{" "}
                      Use remaining quota for {name(member.connectionId)}
                    </label>
                    <p>
                      Using remaining quota makes the effective switching
                      threshold zero.
                    </p>
                    {codex && (
                      <label className="check">
                        <input
                          type="checkbox"
                          checked={current.allowCredits}
                          onChange={(e) =>
                            update({
                              ...current,
                              allowCredits: e.target.checked,
                            })
                          }
                        />{" "}
                        Allow credits for {name(member.connectionId)}
                      </label>
                    )}
                  </>
                )}
              </fieldset>
            );
          })}
        </>
      )}
      {error && <Notice tone="warning">{error}</Notice>}
      <p>
        Saving replaces all native pool and member options. Clearing them
        removes saved settings; it does not change generic affinity.
      </p>
      {(options || members.some((m) => m.nativeOptions)) && (
        <Button
          onClick={() => {
            onChange(undefined);
            onMembers(members.map((m) => ({ ...m, nativeOptions: undefined })));
          }}
        >
          Clear all native settings
        </Button>
      )}
    </details>
  );
}
