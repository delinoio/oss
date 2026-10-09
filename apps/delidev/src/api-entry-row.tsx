// SPDX-License-Identifier: Apache-2.0
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { quotaColorStyle } from "./quota-color";
import { Timestamp, TimestampMode } from "./timestamp-display";
import { LocalizedText, copy, displayLocale, formatDecimal, formatNumber, useLocale } from "./localization";
import { statusLabel } from "./product-status";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { apiFormat, apiFormatLabels, supportsResourceSchema, AccountingUnitKind, UsageAccountingProfile, UsageCostState, UsageQuery, newRequestId, type GetUsageSummaryResponse, type Resource } from "@delinoio/delidev-api-client";
import { document, object, resourceName, text } from "./documents";
import { EstimateAmounts } from "./estimate-costs";
import { QuotaObservationState, quotaPresentation, type SubscriptionQuotaWindow } from "./subscription-settings";
import { Problem } from "./ui";
import { SettingsTaskDialog, SettingsDialogSize, SettingsDialogFocus } from "./settings-task";
import type { UsageEntry } from "./usage-entry";

const unitNames: Partial<Record<AccountingUnitKind, string>> = {
  get [AccountingUnitKind.CODEX_RESPONSE]() { return copy("api-entry-row.codexResponses_9e02cb"); },
  get [AccountingUnitKind.GROK_CLOSED_INPUT]() { return copy("api-entry-row.grokClosedInputs_12a648"); },
  get [AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT]() { return copy("api-entry-row.claudeMainLoopInputs_3081f2"); },
  get [AccountingUnitKind.OPENCODE_STEP]() { return copy("api-entry-row.opencodeSteps_583cef"); },
};
function count(value: string | undefined, measured: number | undefined) {
  return measured && value !== undefined && /^(0|[1-9][0-9]*)$/.test(value) ? BigInt(value).toLocaleString(displayLocale()) : copy("api-entry-row.extra.ca1844969742");
}
function UsageMetrics({ data }: { data?: GetUsageSummaryResponse }) {
  useLocale();
  // Empty history is a display state only; accepted work with missing telemetry
  // must retain its unavailable evidence instead of becoming measured zero.
  const empty = Boolean(data?.totals && data.accountingProfile === UsageAccountingProfile.NATIVE_UNITS_V1
    && data.totals.responses === 0 && data.totals.accounting.every(row => row.units === 0)
    && data.nativeAccounting.every(row => row.totals?.units === 0)
    && data.acceptedExecutionsWithoutResponse === 0 && data.acceptedCompactionsWithoutResponse === 0);
  const native = data?.accountingProfile === UsageAccountingProfile.NATIVE_UNITS_V1 ? data.nativeAccounting.filter(row => row.totals && (row.totals.kind === AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT || row.totals.kind === AccountingUnitKind.OPENCODE_STEP) && row.totals.units > 0) : [];
  const grok = data?.accountingProfile === UsageAccountingProfile.NATIVE_UNITS_V1 ? data.totals?.accounting.find(row => row.kind === AccountingUnitKind.GROK_CLOSED_INPUT && row.units > 0) : undefined;
  const response = (data?.totals?.responses ?? 0) > 0 || (!native.length && !grok);
  const separate = native.length > 0 || Boolean(grok);
  return <>
    <div className="api-usage-metric"><dt>{copy("api-entry-row.estimatedCost_9ccba2")}</dt><dd>
      {response ? <div>{separate ? <small>{copy("api-entry-row.responses_9b4c6d")}</small> : null}{empty ? <strong>$0</strong> : data?.estimatedCost === UsageCostState.KNOWN_SUBTOTAL && data.estimates?.currencies.length ? data.estimates.currencies.map(row => <strong key={row.currency}>{row.currency} {formatDecimal(row.knownAmount) || copy("api-entry-row.extra.ca1844969742")}</strong>) : <strong>{copy("api-entry-row.unavailable_ca1844")}</strong>}</div> : null}
      {native.map(row => <div key={row.totals!.kind}><small>{unitNames[row.totals!.kind]}</small>{row.totals!.currencies.length ? row.totals!.currencies.map(value => <strong key={value.currency}>{value.currency} {formatDecimal(value.knownAmount) || copy("api-entry-row.extra.ca1844969742")}</strong>) : <strong>{copy("api-entry-row.unavailable_ca1844")}</strong>}</div>)}
      {grok ? <div><small>{copy("api-entry-row.grokClosedInputs_12a648")}</small><strong>{copy("api-entry-row.unavailable_ca1844")}</strong></div> : null}
    </dd><small>{empty ? copy("api-entry-row.noUsageInPeriod") : data?.estimatedCost === UsageCostState.KNOWN_SUBTOTAL || native.some(row => row.totals!.currencies.some(value => value.knownAmount !== "")) ? copy("api-entry-row.knownSubtotal_fe63dd") : copy("api-entry-row.noHistoricalEstimate_db5d04")}</small></div>
    <div className="api-usage-metric"><dt>{copy("api-entry-row.observedTokens_617022")}</dt><dd>
      {response ? <div>{separate ? <small>{copy("api-entry-row.responses_9b4c6d")}</small> : null}<strong>{empty ? "0" : count(data?.totals?.total?.knownTotal, data?.totals?.total?.measuredResponses)}</strong><small>{empty ? copy("api-entry-row.noUsageInPeriod") : data?.totals ? data.totals.responses > 0 ? copy("api-entry-row.observedResponses", { count: data.totals.responses, v0: formatNumber(data.totals.responses) }) : copy("api-entry-row.noExactResponseRecords_bead73") : copy("api-entry-row.notReported_adadfa")}</small>{data?.totals?.responses === 1 && displayLocale().startsWith("en") ? <span className="api-entry-sr-only">{copy("api-entry-row.observedResponses_compatibility", { v0: "1" })}</span> : null}</div> : null}
      {native.map(row => <div key={row.totals!.kind}><small>{unitNames[row.totals!.kind]}</small><strong>{count(row.totals!.total?.knownTotal, row.totals!.total?.measuredUnits)}</strong><small>{copy("api-entry-row.observedUnits", { count: row.totals!.units, v0: formatNumber(row.totals!.units) })}</small></div>)}
      {grok ? <div><small>{copy("api-entry-row.grokClosedInputs_12a648")}</small><strong>{count(grok.knownTotal, grok.measuredUnits)}</strong><small>{copy("api-entry-row.observedUnits", { count: grok.units, v0: formatNumber(grok.units) })}</small></div> : null}
    </dd></div>
  </>;
}
function Quota({ window, now, compact = false, active = true }: { window: SubscriptionQuotaWindow; now: number; compact?: boolean; active?: boolean }) {
  useLocale();
  const value = quotaPresentation(window, now);
  return <div className="api-quota-window"><small>{window.id || copy("api-entry-row.extra.aae8d5aad61f")}</small><strong>{value.percent === undefined ? copy("api-entry-row.notReported_adadfa") : copy("api-entry-row.remaining_fe6b6b", { v0: value.percent })}</strong>{value.percent !== undefined ? <progress style={quotaColorStyle(value.percent)} value={value.percent} max={100} aria-label={copy("api-entry-row.remaining_f33475", { v0: window.id || copy("api-entry-row.extra.aae8d5aad61f") })} /> : null}<small>{value.state === QuotaObservationState.Observed ? copy("api-entry-row.observed_64fa8a") : value.state === QuotaObservationState.Failed ? copy("api-entry-row.observationFailed_d2fffd") : value.state === QuotaObservationState.Stale ? copy("api-entry-row.staleObservation_ce693c") : value.state === QuotaObservationState.Unsupported ? copy("api-entry-row.unsupported_543246") : copy("api-entry-row.unknown_b764cd")}</small>{!compact && window.observedAt ? <small><LocalizedText id="api-entry-row.observed_e8e2c1" components={{ s0: <Timestamp active={active} value={window.observedAt} /> }} /></small> : null}{!compact && window.resetAt ? <small><Timestamp active={active} value={window.resetAt} mode={TimestampMode.QuotaCountdown} expired={timestamp => <LocalizedText id="api-entry-row.resets_8693d0" components={{ s0: <>{timestamp}</> }} />} /></small> : null}</div>;
}
export function ApiEntryRow({ row, provider, active, manage, edit, remove, openUsage, verification }: {
  verification?: ReactNode;
  row: Resource; provider?: { displayName: string; enabled: boolean; protocol?: string }; active: boolean;
  manage: () => void; edit: () => void; remove: () => void; openUsage?: (entry: UsageEntry) => void;
}) {
  useLocale();
  const value = document(row), name = resourceName(row), supported = supportsResourceSchema(row);
  const result = useQuery(UsageQuery.getUsageSummary, { accountId: row.id, accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1 }, { enabled: active && supported, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false });
  const [menu, setMenu] = useState(false), [details, setDetails] = useState(false);
  const menuId = useId(), nameId = useId();
  const menuButton = useRef<HTMLButtonElement>(null);
  const [, setClock] = useState(0);
  const now = Date.now();
  const windows: SubscriptionQuotaWindow[] = Array.isArray(value.quota) ? value.quota.map(entry => {
    const window = object(entry);
    return { id: text(window.id), state: Object.values(QuotaObservationState).find(state => state === window.state) ?? QuotaObservationState.Unknown, remaining: typeof window.remaining === "number" ? window.remaining : undefined, observedAt: text(window.observed_at), resetAt: text(window.reset_at) };
  }) : [];
  const expiry = windows.flatMap(window => [Date.parse(window.observedAt ?? "") + 5 * 60 * 1000 + 1, Date.parse(window.resetAt ?? "")]).filter(time => Number.isFinite(time) && time > now).sort((a, b) => a - b)[0];
  useEffect(() => {
    if (!active || expiry === undefined) return;
    const timer = setTimeout(() => setClock(value => value + 1), Math.min(Math.max(0, expiry - Date.now()), 2_147_483_647));
    return () => clearTimeout(timer);
  }, [active, expiry]);
  useEffect(() => { if (!active) { setMenu(false); setDetails(false); } }, [active]);
  const closeMenu = () => { setMenu(false); menuButton.current?.focus(); };
  const cleanup = Boolean(text(object(value.removal).request_id));
  const connected = Boolean(text(object(value.connection).id));
  const validation = object(value.validation);
  const verified = value.health === "ready" && connected && validation.connection_id === object(value.connection).id && validation.state === "observed" && ["credential-accepted", "keyless-endpoint"].includes(text(validation.authentication));
  return <article className="api-entry-row api-usage-row" aria-labelledby={nameId}>
    <div className="api-usage-main">
      <div className="api-entry-identity"><span className="api-entry-mark" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M9 15l6-6m-8 9H5a4 4 0 0 1 0-8h4m6-4h4a4 4 0 0 1 0 8h-4" /></svg></span><div>
        <h2 id={nameId}>{name}</h2><p className="api-entry-provider-name">{provider?.displayName ?? copy("api-entry-row.extra.37709967fc3f") + text(value.provider_id)}</p>
        <p className="api-entry-format">{apiFormatLabels[apiFormat(value.api_protocol) ?? apiFormat(provider?.protocol)!] ?? ""}</p>
        <div className="api-entry-statuses"><span data-state={cleanup ? "warning" : connected ? "connected" : "neutral"}><span className="api-entry-sr-only">{copy("api-entry-row.connection_9adb21")}</span>{cleanup ? copy("api-entry-row.credentialCleanupPending_50459d") : connected ? copy("api-entry-row.connected_229655") : copy("api-entry-row.disconnected_04dfac")}</span><span data-state={value.health === "ready" ? "connected" : "warning"}><span className="api-entry-sr-only">{copy("api-entry-row.health_6e9098")}</span>{verified ? copy("api-verification.badge") : statusLabel(text(value.health)) || copy("api-entry-row.extra.b764cdc0eab7")}</span></div>
        {!supported ? <small>{row.id}</small> : null}
      </div></div>
      <dl className="api-usage-metrics"><UsageMetrics data={result.data} /><div className="api-usage-metric"><dt>{copy("api-entry-row.quota_6c105c")}</dt><dd>{value.confirmed_exhausted === true ? <strong className="api-entry-exhausted">{copy("api-entry-row.confirmedExhausted_763851")}</strong> : windows.length ? windows.slice(0, 2).map((window, index) => <Quota key={index} window={window} now={now} compact />) : <strong>{copy("api-entry-row.notReported_adadfa")}</strong>}</dd></div></dl>
      <div className="api-entry-controls"><SettingsActionButton icon={SettingsActionIcon.Connect} type="button" disabled={!supported} onClick={manage}>{copy("api-entry-row.manageConnection_ad2892")}</SettingsActionButton><div className="api-entry-more">
        <button ref={menuButton} type="button" aria-label={copy("api-entry-row.moreActionsFor_5057a7", { v0: name })} aria-expanded={menu} aria-controls={menuId} onClick={() => setMenu(!menu)} onKeyDown={event => { if (event.key === "Escape" && menu) { event.preventDefault(); event.stopPropagation(); closeMenu(); } }}><span aria-hidden="true">⋯</span></button>
        {menu ? <div id={menuId} className="api-entry-more-panel" role="group" aria-label={copy("api-entry-row.actionsFor_b59837", { v0: name })} onKeyDown={event => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); closeMenu(); } }}><SettingsActionButton icon={SettingsActionIcon.Inspect} type="button" onClick={() => { closeMenu(); setDetails(true); }}>{copy("api-entry-row.details")}</SettingsActionButton><SettingsActionButton icon={SettingsActionIcon.Edit} type="button" disabled={!supported} onClick={() => { closeMenu(); edit(); }}>{copy("api-entry-row.editPreferences_00b4cc")}</SettingsActionButton><SettingsActionButton icon={SettingsActionIcon.Delete} className="api-entry-delete" type="button" disabled={!supported} onClick={() => { closeMenu(); remove(); }}>{copy("api-entry-row.deleteEntry_d2968b")}</SettingsActionButton></div> : null}
      </div></div>
    </div>
    {verification}
    <div className="api-entry-usage-status">
      {result.isFetching ? <p role="status">{result.data ? copy("api-entry-row.refreshingUsage_70313a") : copy("api-entry-row.loadingUsage_0134e9")}</p> : null}
      <Problem error={result.error} />{result.error ? <><p role="status">{result.data ? copy("api-entry-row.refreshFailedShowingStaleUsageFrom_b5be80") : copy("api-entry-row.usageIsUnavailableEntryControlsRemain_755eaf")}</p><SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={!active || result.isFetching} onClick={() => void result.refetch()}><LocalizedText id="api-entry-row.retryUsageFor_40ac3c" components={{ s0: <>{name}</> }} /></SettingsActionButton></> : null}
    </div>
    <div className="api-entry-footer"><SettingsActionButton icon={SettingsActionIcon.Inspect} type="button" className="api-entry-text-action api-entry-usage-link" disabled={!supported || !openUsage} onClick={() => openUsage?.({ key: newRequestId(), accountId: row.id, fromUnixMs: result.data?.fromUnixMs ?? 0n, untilUnixMs: result.data?.untilUnixMs ?? 0n })}>{copy("api-entry-row.viewUsage_2e4ae3")}</SettingsActionButton></div>
    {details ? <SettingsTaskDialog title={copy("api-entry-row.details")} subtitle={name} size={SettingsDialogSize.Form} focus={SettingsDialogFocus.Heading} close={() => setDetails(false)}><ApiEntryDetailsBody row={row} provider={provider} usage={result.data} windows={windows} now={now} active={active}>
      {result.isFetching ? <p role="status">{result.data ? copy("api-entry-row.refreshingUsage_70313a") : copy("api-entry-row.loadingUsage_0134e9")}</p> : null}
      <Problem error={result.error} />{result.error ? <p role="status">{result.data ? copy("api-entry-row.refreshFailedShowingStaleUsageFrom_b5be80") : copy("api-entry-row.usageIsUnavailableEntryControlsRemain_755eaf")}</p> : null}
    </ApiEntryDetailsBody></SettingsTaskDialog> : null}
  </article>;
}

// Presentation consumes the mounted row's original query and quota clock. A
// dialog opening never creates a second reader or verification controller.
export function ApiEntryDetailsBody({ row, provider, usage, windows, now, children, active = true }: { row: Resource; provider?: { enabled: boolean; protocol?: string }; usage?: GetUsageSummaryResponse; windows: SubscriptionQuotaWindow[]; now: number; children?: ReactNode; active?: boolean }) {
  useLocale();
  const value = document(row);
  return <section className="api-entry-details">{children}
      <dl><div><dt>{copy("api-entry-row.apiFormat")}</dt><dd>{apiFormatLabels[apiFormat(value.api_protocol) ?? apiFormat(provider?.protocol)!] ?? ""}</dd></div><div><dt>{copy("api-entry-row.entry_861e39")}</dt><dd>{value.enabled === true ? copy("api-entry-row.enabled_92c1cd") : copy("api-entry-row.disabled_75081b")}</dd></div><div><dt>{copy("api-entry-row.providerStatus_369744")}</dt><dd>{provider ? provider.enabled ? copy("api-entry-row.enabled_92c1cd") : copy("api-entry-row.off_ca7981") : copy("api-entry-row.unavailable_ca1844")}</dd></div><div><dt>{copy("api-entry-row.accountId_4489c4")}</dt><dd>{row.id}</dd></div></dl>
      <p>{copy("api-entry-row.connectionAndHealthAreIndependentActual_149c54")}</p>
      {usage ? <>{usage.fromUnixMs > 0n && usage.untilUnixMs > usage.fromUnixMs ? <p><LocalizedText id="api-entry-row.rangeExclusive_cacdfc" components={{ s0: <Timestamp value={new Date(Number(usage.fromUnixMs)).toISOString()} mode={TimestampMode.Absolute} timeZone="UTC" />, s1: <Timestamp value={new Date(Number(usage.untilUnixMs)).toISOString()} mode={TimestampMode.Absolute} timeZone="UTC" /> }} /></p> : null}<p><LocalizedText id="api-entry-row.incompleteCoverageAcceptedExecutionsAndNative_a304c9" components={{ s0: <>{formatNumber(usage.acceptedExecutionsWithoutResponse)}</>, s1: <>{formatNumber(usage.acceptedCompactionsWithoutResponse)}</> }} /></p><p><LocalizedText id="api-entry-row.measuredResponsesUnavailableResponseTotals_7758ea" components={{ s0: <>{formatNumber(usage.totals?.total?.measuredResponses ?? 0)}</>, s1: <>{formatNumber(usage.totals?.total?.unavailableResponses ?? 0)}</> }} /></p><EstimateAmounts value={usage.estimates} />{usage.nativeAccounting.map(data => unitNames[data.totals?.kind ?? AccountingUnitKind.UNSPECIFIED] ? <p key={data.totals!.kind}><LocalizedText id="api-entry-row.measuredUnavailableTotalUnitsUnpricedUnits_f7c451" components={{ s0: <>{unitNames[data.totals!.kind]}</>, s1: <>{formatNumber(data.totals?.total?.measuredUnits ?? 0)}</>, s2: <>{formatNumber(data.totals?.total?.unavailableUnits ?? 0)}</>, s3: <>{formatNumber(data.totals?.unpricedUnits ?? 0)}</> }} /></p> : null)}{usage.accountingProfile !== UsageAccountingProfile.NATIVE_UNITS_V1 ? <p>{copy("api-entry-row.independentNativeAccountingIsUnavailableFrom_324aa8")}</p> : null}</> : <p>{copy("api-entry-row.noUsageEvidenceHasBeenRetrieved_a6a0ca")}</p>}
      {windows.length ? windows.map((window, index) => <Quota key={index} window={window} now={now} active={active} />) : <p>{copy("api-entry-row.noQuotaObservation_d9e3af")}</p>}
  </section>;
}
