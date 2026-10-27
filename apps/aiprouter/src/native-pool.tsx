// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "./i18n";
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
    return translate("nativeSettingsRequireOauthConnectionsFromOne");
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
      return translate("checkMemberThresholdsNativePriorityAndProvider");
  }
  if (!options) return;
  if (!bounded(options.upstreamFailoverThreshold, 0, 20))
    return translate("upstreamFailoverThresholdMustBe020");
  if (
    options.pinnedTransientPolicy !== undefined &&
    ![
      NativePinnedTransientPolicy.UNSPECIFIED,
      NativePinnedTransientPolicy.KEEP,
      NativePinnedTransientPolicy.DETOUR_NEW_THREADS,
    ].includes(options.pinnedTransientPolicy)
  )
    return translate("chooseASupportedPinnedTransientPolicy");
  if (
    provider !== "openai" &&
    (options.upstreamFailoverThreshold !== undefined ||
      options.failureWindow !== undefined ||
      options.pinnedTransientPolicy !== undefined)
  )
    return translate("upstreamFailoverFailureWindowAndPinnedTransient");

  if (
    !bounded(options.stickyLimit, 1, 100) ||
    !bounded(options.autoSwitchThreshold, 0, 100)
  )
    return translate("stickyLimitMustBe1100And");
  if (
    provider === "openai" &&
    (options.quotaWindow !== NativeQuotaWindow.UNSPECIFIED ||
      options.nativeMessages !== undefined ||
      options.routes.length)
  )
    return translate("quotaWindowsNativeMessagesAndModelRules");
  if (
    provider !== "openai" &&
    (options.strategy === NativePoolStrategy.RESET_FIRST ||
      options.cacheAffinity !== undefined ||
      options.excludedPlans.length)
  )
    return translate("message_522839c681");
  if (
    options.excludedPlans.length > 100 ||
    new Set(options.excludedPlans.map((p) => p.toLowerCase())).size !==
      options.excludedPlans.length ||
    options.excludedPlans.some((p) => !p.trim() || p.length > 128)
  )
    return translate("useUpTo100NonemptyUniqueExcluded");
  if (
    options.routes.length > 32 ||
    new Set(options.routes.map((r) => r.name)).size !== options.routes.length ||
    new Set(options.routes.map((r) => r.match)).size !== options.routes.length
  )
    return translate("useAtMost32ModelRulesWith");
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
      return translate("eachModelRuleNeedsASafeUnique");
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
  useLocale();
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
  useLocale();
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
        <option value="">{translate("default")}</option>
        <option value="true">{translate("enabled")}</option>
        <option value="false">{translate("disabled")}</option>
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
  useLocale();
  const provider = nativeProvider(members, accounts),
    codex = provider === "openai";
  const error = nativePoolError(options, members, provider);
  const name = (id: string) =>
    accounts.find((a) => a.id === id)?.name ??
    translate("unavailableConnection");
  return (
    <details>
      <summary>{translate("nativeOauthPoolSettings")}</summary>
      <p>{translate("appliesToPoolsContainingOauthConnectionsFrom")}</p>
      {!provider && (
        <Notice>
          {translate("addConnectionsFromOneSupportedOauthProvider")}
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
        />
        {translate("configurePoolLevelNativeSettings")}
      </label>
      {options && provider && (
        <>
          <OptionalBoolean
            label={translate("nativePooling")}
            value={options.enabled}
            hint={
              isNew
                ? translate("defaultEnablesNativePoolingWhenThisPool")
                : translate("anUnspecifiedSavedValueIsDisabledChoose")
            }
            onChange={(enabled) => onChange({ ...options, enabled })}
          />
          <Field label={translate("nativeSelectionStrategy")}>
            <select
              value={options.strategy}
              onChange={(e) =>
                onChange({ ...options, strategy: Number(e.target.value) })
              }
            >
              <option value={NativePoolStrategy.UNSPECIFIED}>
                {translate("defaultQuota")}
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
            label={translate("stickyAccountLimit")}
            value={options.stickyLimit}
            min={1}
            max={100}
            hint={translate("emptyUses1")}
            onChange={(stickyLimit) => onChange({ ...options, stickyLimit })}
          />
          <OptionalNumber
            label={translate("poolSwitchingThreshold")}
            value={options.autoSwitchThreshold}
            min={0}
            max={100}
            hint={translate("emptyUses80ZeroDisablesVoluntaryQuota")}
            onChange={(autoSwitchThreshold) =>
              onChange({ ...options, autoSwitchThreshold })
            }
          />
          {codex ? (
            <>
              <OptionalNumber
                label={translate("upstreamFailoverThreshold")}
                value={options.upstreamFailoverThreshold}
                min={0}
                max={20}
                hint={translate("emptyUses3ZeroDisablesUpstreamFailure")}
                onChange={(upstreamFailoverThreshold) =>
                  onChange({ ...options, upstreamFailoverThreshold })
                }
              />
              <OptionalBoolean
                label={translate("failureWindow")}
                value={options.failureWindow}
                hint={translate("defaultEnabledUsesASliding60Second")}
                onChange={(failureWindow) =>
                  onChange({ ...options, failureWindow })
                }
              />
              <Field
                label={translate("pinnedTransientPolicy")}
                hint={translate("emptyUsesKeepDetourAppliesOnlyTo")}
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
                  <option value="">{translate("defaultKeep")}</option>
                  {options.pinnedTransientPolicy ===
                    NativePinnedTransientPolicy.UNSPECIFIED && (
                    <option value={NativePinnedTransientPolicy.UNSPECIFIED}>
                      {translate("unspecifiedKeep")}
                    </option>
                  )}
                  <option value={NativePinnedTransientPolicy.KEEP}>
                    {translate("keep")}
                  </option>
                  <option
                    value={NativePinnedTransientPolicy.DETOUR_NEW_THREADS}
                  >
                    {translate("detourNewThreads")}
                  </option>
                </select>
              </Field>
              <OptionalBoolean
                label={translate("cacheAffinity")}
                value={options.cacheAffinity}
                hint={translate("defaultEnabledForCodex")}
                onChange={(cacheAffinity) =>
                  onChange({ ...options, cacheAffinity })
                }
              />
              <Field
                label={translate("excludedPlans")}
                hint={translate("onePlanPerLineUpTo100")}
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
              <Field label={translate("nativeQuotaWindow")}>
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
                    {translate("defaultFiveHour")}
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
                label={translate("nativeMessages")}
                value={options.nativeMessages}
                hint={translate("defaultEnabledDisabledUsesLegacyTranslation")}
                onChange={(nativeMessages) =>
                  onChange({ ...options, nativeMessages })
                }
              />
              <h3>{translate("orderedModelRules")}</h3>
              <p>{translate("theFirstMatchingRuleSelectsAnOrdered")}</p>
              {options.routes.map((route, index) => (
                <fieldset key={index}>
                  <legend>
                    {translate("modelRule", { value0: index + 1 })}
                  </legend>
                  <Field
                    label={translate("ruleName", { value0: index + 1 })}
                    hint={translate(
                      "lettersDigitsUnderscoresOrHyphensStartWith",
                    )}
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
                    label={translate("modelPattern", { value0: index + 1 })}
                    hint={translate("useAndAsWildcardsMaximum128Characters")}
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
                            {translate("moveConnectionUp")}
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
                            {translate("removeRuleConnection")}
                          </Button>
                        </div>
                      </li>
                    ))}
                  </ol>
                  <Field
                    label={translate("addRuleConnection", {
                      value0: index + 1,
                    })}
                  >
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
                      <option value="">
                        {translate("chooseAPoolConnection")}
                      </option>
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
                    />
                    {translate("useTheFullPoolOnlyWhenNo")}
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
                      {translate("moveRuleUp")}
                    </Button>
                    <Button
                      onClick={() =>
                        onChange({
                          ...options,
                          routes: options.routes.filter((_, i) => i !== index),
                        })
                      }
                    >
                      {translate("removeModelRule", { value0: index + 1 })}
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
                {translate("addModelRule")}
              </Button>
            </>
          )}
        </>
      )}
      {provider && (
        <>
          <h3>{translate("memberNativeOptions")}</h3>
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
                  />
                  {translate("overrideNativeOptionsFor", {
                    value0: name(member.connectionId),
                  })}
                </label>
                {current && (
                  <>
                    <OptionalNumber
                      label={translate("switchingThresholdFor", {
                        value0: name(member.connectionId),
                      })}
                      value={current.autoSwitchThreshold}
                      min={0}
                      max={100}
                      hint={translate(
                        "emptyInheritsThePoolThresholdZeroDisables",
                      )}
                      onChange={(autoSwitchThreshold) =>
                        update({ ...current, autoSwitchThreshold })
                      }
                    />
                    <OptionalNumber
                      label={translate("nativePriorityFor", {
                        value0: name(member.connectionId),
                      })}
                      value={current.priority}
                      min={-100}
                      max={100}
                      hint={translate("higherValuesComeFirstSeparateFromThe")}
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
                      />
                      {translate("useRemainingQuotaFor", {
                        value0: name(member.connectionId),
                      })}
                    </label>
                    <p>
                      {translate(
                        "usingRemainingQuotaMakesTheEffectiveSwitching",
                      )}
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
                        />
                        {translate("allowCreditsFor", {
                          value0: name(member.connectionId),
                        })}
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
      <p>{translate("savingReplacesAllNativePoolAndMember")}</p>
      {(options || members.some((m) => m.nativeOptions)) && (
        <Button
          onClick={() => {
            onChange(undefined);
            onMembers(members.map((m) => ({ ...m, nativeOptions: undefined })));
          }}
        >
          {translate("clearAllNativeSettings")}
        </Button>
      )}
    </details>
  );
}
