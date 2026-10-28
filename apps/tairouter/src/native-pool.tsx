// SPDX-License-Identifier: Apache-2.0
import { t as translate, useLocale } from "./i18n";
import { useEffect, useRef, useState } from "react";
import { create } from "@bufbuild/protobuf";
import {
  NativePoolOptionsSchema,
  NativePoolMemberOptionsSchema,
  NativeModelRouteSchema,
  NativePoolStrategy,
  NativeQuotaWindow,
  NativePinnedTransientPolicy,
  ProviderAuthKind,
  type NativeModelRoute,
  type NativePoolOptions,
  type NativePoolMemberOptions,
  type ProviderConnection,
} from "@tairouter/client";
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
/** Limits for the Codex excluded plan list. */
enum ExcludedPlanLimit {
  MaxPlans = 100,
  MaxLength = 128,
}
/**
 * Field-level explanation for the Codex excluded plans list, or undefined
 * when valid. Values are checked as typed: nothing is trimmed or case-folded,
 * and uniqueness ignores case only for comparison.
 */
export function excludedPlansError(plans: string[]): string | undefined {
  if (plans.length > ExcludedPlanLimit.MaxPlans)
    return translate("useAtMostExcludedPlans", {
      value0: ExcludedPlanLimit.MaxPlans,
    });
  const blank = plans.findIndex((p) => !p.trim());
  if (blank >= 0)
    return translate("lineIsBlankRemoveEmptyLines", { value0: blank + 1 });
  const long = plans.findIndex((p) => p.length > ExcludedPlanLimit.MaxLength);
  if (long >= 0)
    return translate("lineIsLongerThanCharacters", {
      value0: long + 1,
      value1: ExcludedPlanLimit.MaxLength,
    });
  const seen = new Map<string, number>();
  for (const [i, p] of plans.entries()) {
    const key = p.toLowerCase();
    const first = seen.get(key);
    if (first !== undefined)
      return translate("lineRepeatsLinePlanNamesAreCompared", {
        value0: i + 1,
        value1: first + 1,
      });
    seen.set(key, i);
  }
}
/** DOM id of the native settings disclosure summary; error summary target. */
export const NATIVE_SETTINGS_SUMMARY_ID = "pool-native-settings";
/** End notes that stay visible whether or not pool-level settings exist. */
const nativePoolNote = {
  create: "clearingRemovesDraftNativeSettingsCreatingAn",
  edit: "savingReplacesAllNativePoolAndMember",
} as const;
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
  const invalid =
    value !== undefined &&
    !(Number.isInteger(value) && value >= min && value <= max);
  return (
    <Field
      label={label}
      hint={hint}
      error={
        invalid
          ? translate("enterAWholeNumberFromTo", { value0: min, value1: max })
          : undefined
      }
    >
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
/** Limits for ordered model rules. */
enum ModelRuleLimit {
  MaxRules = 32,
  MaxConnections = 32,
  MaxNameLength = 64,
  MaxPatternLength = 128,
}
const MODEL_RULE_NAME = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/;
const MODEL_RULE_PATTERN = /^[A-Za-z0-9_./:*?-]+$/;
/** Field-level explanations for one model rule; undefined entries are valid. */
export type ModelRuleErrors = {
  name?: string;
  match?: string;
  connections?: string;
};
/**
 * Explains why one model rule cannot be saved. The checks mirror
 * nativePoolError, which keeps gating Save with one aggregate message.
 */
export function modelRuleErrors(
  route: NativeModelRoute,
  index: number,
  routes: NativeModelRoute[],
  members: NativeMember[],
): ModelRuleErrors {
  const errors: ModelRuleErrors = {};
  if (!route.name) errors.name = translate("enterARuleName");
  else if (!MODEL_RULE_NAME.test(route.name))
    errors.name = translate("useOrFewerLettersDigitsUnderscoresOr", {
      value0: ModelRuleLimit.MaxNameLength,
    });
  else if (routes.some((r, i) => i !== index && r.name === route.name))
    errors.name = translate("anotherRuleAlreadyUsesThisNameNames");
  if (!route.match) errors.match = translate("enterAModelPattern");
  else if (route.match.length > ModelRuleLimit.MaxPatternLength)
    errors.match = translate("useOrFewerCharacters", {
      value0: ModelRuleLimit.MaxPatternLength,
    });
  else if (!MODEL_RULE_PATTERN.test(route.match))
    errors.match = translate("useLettersDigitsUnderscoresDotsSlashesColons");
  else if (routes.some((r, i) => i !== index && r.match === route.match))
    errors.match = translate("anotherRuleAlreadyUsesThisPatternPatterns");
  const ids = route.connectionIds;
  if (ids.length < 1)
    errors.connections = translate("addAtLeastOneConnectionToThis");
  else if (ids.length > ModelRuleLimit.MaxConnections)
    errors.connections = translate("aRuleCanListAtMostConnections", {
      value0: ModelRuleLimit.MaxConnections,
    });
  else if (new Set(ids).size !== ids.length)
    errors.connections = translate("aConnectionCanAppearOnlyOnceIn");
  else if (ids.some((id) => !members.some((m) => m.connectionId === id)))
    errors.connections = translate("aListedConnectionIsNoLongerIn");
  return errors;
}
function ModelRuleCard({
  index,
  route,
  options,
  members,
  name,
  onChange,
  announce,
  requestFocus,
}: {
  index: number;
  route: NativeModelRoute;
  options: NativePoolOptions;
  members: NativeMember[];
  name: (id: string) => string;
  onChange: (o: NativePoolOptions) => void;
  announce: (message: string) => void;
  requestFocus: (id: string) => void;
}) {
  useLocale();
  const number = index + 1;
  const rid = `pool-rule-${index}`;
  const errors = modelRuleErrors(route, index, options.routes, members);
  const patch = (next: Partial<NativeModelRoute>) =>
    onChange({
      ...options,
      routes: options.routes.map((r, i) =>
        i === index ? { ...r, ...next } : r,
      ),
    });
  const available = members.filter(
    (m) => !route.connectionIds.includes(m.connectionId),
  );
  return (
    <fieldset className="target-card rule-card">
      <legend>{translate("modelRule", { value0: number })}</legend>
      <div className="field-grid">
        <Field
          label={translate("ruleName", { value0: number })}
          hint={translate("lettersDigitsUnderscoresOrHyphensStartWith")}
          error={errors.name}
        >
          <input
            required
            value={route.name}
            onChange={(e) => patch({ name: e.target.value })}
          />
        </Field>
        <Field
          label={translate("modelPattern", { value0: number })}
          hint={translate("useAndAsWildcardsMaximum128Characters")}
          error={errors.match}
        >
          <input
            required
            value={route.match}
            onChange={(e) => patch({ match: e.target.value })}
          />
        </Field>
      </div>
      <p className="section-hint">
        {translate("listedConnectionsAreTriedInThisOrder")}
      </p>
      <ol className="rule-connections">
        {route.connectionIds.map((id, j) => {
          const label = name(id);
          const inPool = members.some((m) => m.connectionId === id);
          return (
            <li key={id}>
              <span className="rule-connection-name">
                {inPool ? label : translate("notInThisPool", { value0: label })}
              </span>
              <div className="actions">
                <Button
                  id={`${rid}-conn-${id}-up`}
                  aria-label={translate("moveConnectionUpInModelRule", {
                    value0: label,
                    value1: number,
                  })}
                  disabled={j === 0}
                  onClick={() => {
                    const ids = [...route.connectionIds];
                    [ids[j - 1], ids[j]] = [ids[j]!, ids[j - 1]!];
                    patch({ connectionIds: ids });
                    requestFocus(
                      j - 1 === 0
                        ? `${rid}-conn-${id}-remove`
                        : `${rid}-conn-${id}-up`,
                    );
                    announce(
                      translate("movedUpToPositionInModelRule", {
                        value0: label,
                        value1: j,
                        value2: number,
                      }),
                    );
                  }}
                >
                  {translate("moveConnectionUp")}
                </Button>
                <Button
                  id={`${rid}-conn-${id}-remove`}
                  variant="danger"
                  aria-label={translate("removeRuleConnectionFromModelRule", {
                    value0: label,
                    value1: number,
                  })}
                  onClick={() => {
                    const next =
                      route.connectionIds[j + 1] ?? route.connectionIds[j - 1];
                    patch({
                      connectionIds: route.connectionIds.filter(
                        (_, k) => k !== j,
                      ),
                    });
                    requestFocus(
                      next ? `${rid}-conn-${next}-remove` : `${rid}-add`,
                    );
                    announce(
                      translate("removedFromModelRule", {
                        value0: label,
                        value1: number,
                      }),
                    );
                  }}
                >
                  {translate("removeRuleConnection")}
                </Button>
              </div>
            </li>
          );
        })}
      </ol>
      <Field
        label={translate("addRuleConnection", { value0: number })}
        hint={
          route.connectionIds.length >= ModelRuleLimit.MaxConnections
            ? translate("aRuleCanListAtMostConnections", {
                value0: ModelRuleLimit.MaxConnections,
              })
            : available.length === 0
              ? translate("everyConnectionInThisPoolIsAlready")
              : undefined
        }
        error={errors.connections}
      >
        <select
          id={`${rid}-add`}
          value=""
          disabled={route.connectionIds.length >= ModelRuleLimit.MaxConnections}
          onChange={(e) => {
            const id = e.target.value;
            if (!id) return;
            patch({ connectionIds: [...route.connectionIds, id] });
            announce(
              translate("addedToModelRule", {
                value0: name(id),
                value1: number,
              }),
            );
          }}
        >
          <option value="">{translate("chooseAPoolConnection")}</option>
          {available.map((m) => (
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
          onChange={(e) => patch({ fallback: e.target.checked })}
        />
        {translate("useTheFullPoolOnlyWhenNo")}
      </label>
      <div className="actions">
        <Button
          id={`${rid}-up`}
          aria-label={translate("moveRuleUpModelRule", { value0: number })}
          disabled={index === 0}
          onClick={() => {
            const routes = [...options.routes];
            [routes[index - 1], routes[index]] = [
              routes[index]!,
              routes[index - 1]!,
            ];
            onChange({ ...options, routes });
            requestFocus(
              index - 1 === 0
                ? `pool-rule-0-remove`
                : `pool-rule-${index - 1}-up`,
            );
            announce(
              translate("movedModelRuleUpToPosition", {
                value0: number,
                value1: index,
              }),
            );
          }}
        >
          {translate("moveRuleUp")}
        </Button>
        <Button
          id={`${rid}-remove`}
          variant="danger"
          onClick={() => {
            onChange({
              ...options,
              routes: options.routes.filter((_, i) => i !== index),
            });
            requestFocus(
              options.routes.length > 1
                ? `pool-rule-${index === options.routes.length - 1 ? index - 1 : index}-remove`
                : "pool-add-rule",
            );
            announce(translate("removedModelRule", { value0: number }));
          }}
        >
          {translate("removeModelRule", { value0: number })}
        </Button>
      </div>
    </fieldset>
  );
}
function AnthropicFields({
  family,
  options,
  onChange,
  members,
  name,
}: {
  /** Display identity of the homogeneous Anthropic family. */
  family: string;
  options: NativePoolOptions;
  onChange: (o: NativePoolOptions) => void;
  members: NativeMember[];
  name: (id: string) => string;
}) {
  useLocale();
  const [announcement, setAnnouncement] = useState("");
  const focusRequest = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (focusRequest.current) {
      document.getElementById(focusRequest.current)?.focus();
      focusRequest.current = undefined;
    }
  });
  const atLimit = options.routes.length >= ModelRuleLimit.MaxRules;
  return (
    <>
      <fieldset className="native-group">
        <legend>{translate("quotaAndMessages", { value0: family })}</legend>
        <div className="field-grid">
          <Field
            label={translate("nativeQuotaWindow")}
            hint={translate("whichUsageWindowDrivesVoluntaryQuotaSwitching")}
          >
            <select
              value={options.quotaWindow}
              onChange={(e) =>
                onChange({ ...options, quotaWindow: Number(e.target.value) })
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
        </div>
      </fieldset>
      <section className="route-section" aria-labelledby="pool-rules-heading">
        <h3 id="pool-rules-heading">{translate("orderedModelRules")}</h3>
        <p className="section-hint">
          {translate("theFirstMatchingRuleSelectsAnOrdered")}
        </p>
        <p className="section-hint">
          {translate("ofRules", {
            value0: options.routes.length,
            value1: ModelRuleLimit.MaxRules,
          })}
          {atLimit && <> {translate("removeARuleToAddAnother")}</>}
        </p>
        <div
          role="status"
          className="visually-hidden"
          data-testid="model-rule-announcement"
        >
          {announcement}
        </div>
        {options.routes.map((route, index) => (
          <ModelRuleCard
            key={index}
            index={index}
            route={route}
            options={options}
            members={members}
            name={name}
            onChange={onChange}
            announce={setAnnouncement}
            requestFocus={(id) => {
              focusRequest.current = id;
            }}
          />
        ))}
        <Button
          id="pool-add-rule"
          disabled={atLimit}
          onClick={() =>
            onChange({
              ...options,
              routes: [...options.routes, create(NativeModelRouteSchema)],
            })
          }
        >
          {translate("addModelRule")}
        </Button>
      </section>
    </>
  );
}
function MemberOverrides({
  members,
  onMembers,
  codex,
  name,
}: {
  members: NativeMember[];
  onMembers: (m: NativeMember[]) => void;
  codex: boolean;
  name: (id: string) => string;
}) {
  useLocale();
  return (
    <section className="route-section" aria-labelledby="pool-member-native">
      <h3 id="pool-member-native">{translate("memberNativeOptions")}</h3>
      <p className="section-hint">
        {translate("cardsFollowTheConnectionOrderAboveNative")}
      </p>
      {members.map((member, index) => {
        const current = member.nativeOptions;
        const label = name(member.connectionId);
        const update = (nativeOptions: NativePoolMemberOptions | undefined) =>
          onMembers(
            members.map((m, i) => (i === index ? { ...m, nativeOptions } : m)),
          );
        return (
          <fieldset
            className="target-card native-member-card"
            key={member.connectionId}
          >
            <legend>{translate("nativeOptionsFor", { value0: label })}</legend>
            <p className="section-hint">
              {current
                ? translate("overrideConfigured")
                : translate("noOverride")}
            </p>
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
              {translate("overrideNativeOptionsFor", { value0: label })}
            </label>
            {current && (
              <>
                <div className="field-grid">
                  <OptionalNumber
                    label={translate("switchingThresholdFor", {
                      value0: label,
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
                    label={translate("nativePriorityFor", { value0: label })}
                    value={current.priority}
                    min={-100}
                    max={100}
                    hint={translate("wholeNumberFrom100To100Higher")}
                    onChange={(priority) => update({ ...current, priority })}
                  />
                </div>
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
                  {translate("useRemainingQuotaFor", { value0: label })}
                </label>
                <p className="section-hint">
                  {current.useRemainingQuota
                    ? current.autoSwitchThreshold === undefined
                      ? translate("theEffectiveSwitchingThresholdIs0No")
                      : translate("theEffectiveSwitchingThresholdIs0The", {
                          value0: current.autoSwitchThreshold,
                        })
                    : translate(
                        "usingRemainingQuotaMakesTheEffectiveSwitching",
                      )}
                </p>
                {codex && (
                  <label className="check">
                    <input
                      type="checkbox"
                      checked={current.allowCredits}
                      onChange={(e) =>
                        update({ ...current, allowCredits: e.target.checked })
                      }
                    />
                    {translate("allowCreditsFor", { value0: label })}
                  </label>
                )}
              </>
            )}
          </fieldset>
        );
      })}
    </section>
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
  // The disclosure stays closed by default but opens itself when it holds a
  // blocking error, so the message and the controls that fix it are visible.
  const [open, setOpen] = useState(false);
  const blocked = Boolean(error);
  useEffect(() => {
    if (blocked) setOpen(true);
  }, [blocked]);
  const hasNative = !!options || members.some((m) => m.nativeOptions);
  return (
    <>
      <details
        className="route-disclosure"
        open={open}
        onToggle={(event) => setOpen(event.currentTarget.open)}
      >
        <summary id={NATIVE_SETTINGS_SUMMARY_ID}>
          {translate("nativeOauthPoolSettings")}
        </summary>
        <p>{translate("appliesToPoolsContainingOauthConnectionsFrom")}</p>
        {!provider && (
          <Notice>
            {translate("addConnectionsFromOneSupportedOauthProvider")}
          </Notice>
        )}
        <label className="check">
          <input
            type="checkbox"
            aria-describedby="native-pool-config-help"
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
        <p id="native-pool-config-help" className="section-hint">
          {translate("uncheckedMeansNoPoolLevelNativeSettings")}
        </p>
        {options && provider && (
          <>
            <OptionalBoolean
              label={translate("nativePooling")}
              value={options.enabled}
              hint={
                isNew
                  ? translate("eligibleOauthPoolsEnableNativePoolingOn")
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
              <fieldset>
                <legend>{translate("codexFailoverAndAffinity")}</legend>
                <OptionalNumber
                  label={translate("upstreamFailoverThreshold")}
                  value={options.upstreamFailoverThreshold}
                  min={0}
                  max={20}
                  hint={translate("emptyUses3ZeroDisablesConsecutiveFailure")}
                  onChange={(upstreamFailoverThreshold) =>
                    onChange({ ...options, upstreamFailoverThreshold })
                  }
                />
                <OptionalBoolean
                  label={translate("failureWindow")}
                  value={options.failureWindow}
                  hint={translate("defaultSliding60SecondFailureRatioSteering")}
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
                  hint={translate("onePlanPerLineUpTo100_2da7f1")}
                  error={excludedPlansError(options.excludedPlans)}
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
              </fieldset>
            ) : (
              <AnthropicFields
                family={provider === "anthropic2" ? "Anthropic 2" : "Anthropic"}
                options={options}
                onChange={onChange}
                members={members}
                name={name}
              />
            )}
          </>
        )}
        {provider && (
          <MemberOverrides
            members={members}
            onMembers={onMembers}
            codex={codex}
            name={name}
          />
        )}
        {error && <Notice tone="warning">{error}</Notice>}
        {hasNative && (
          <Button
            onClick={() => {
              onChange(undefined);
              onMembers(
                members.map((m) => ({ ...m, nativeOptions: undefined })),
              );
            }}
          >
            {translate("clearAllNativeSettings")}
          </Button>
        )}
      </details>
      <p id="native-pool-note" className="section-hint">
        {translate(isNew ? nativePoolNote.create : nativePoolNote.edit)}
      </p>
    </>
  );
}
