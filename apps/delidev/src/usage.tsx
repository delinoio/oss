import { Timestamp, TimestampMode, TimestampText } from "./timestamp-display";
import type { ReactNode } from "react";
import { LocalizedText, copy, displayLocale, useLocale } from "./localization";
import { useId, useState } from "react";
import type { UsageEntry } from "./usage-entry";
import { useQuery } from "@connectrpc/connect-query";
import { subscriptionServiceLabel, SubscriptionServiceIdentity, SystemCapability, SystemQuery, ResourceQuery, EntityKind, supportsResourceSchema, UsageCoverage, UsageQuery, UsageTimeGranularity, type UsageMeasure, type UsageTotals } from "@delinoio/delidev-api-client";
import { EstimateAmounts, EstimateCosts } from "./estimate-costs";
import { ResourceChoice } from "./configuration-fields";
import { Problem } from "./ui";
import { SidebarSurface, useCloseSidebarDrawer } from "./sidebar-context";
import { useUsageFilters, type UsageSelection } from "./usage-filters";
import { UsageCharts } from "./usage-chart";
import type { AccountingDisclosures } from "./usage-accounting-source";
import "./usage.css";
import { GrokAccounting } from "./grok-accounting";
import { NativeAccounting } from "./native-accounting";
import { ModelPricing } from "./pricing";
import { SettingsLifetime } from "./settings-lifetime";
import { MutationIntents } from "./mutation";
import { document, resourceName, text } from "./documents";

enum UsageTab { Responses = "responses", NativeAccounting = "native-accounting", CostEvidence = "cost-evidence" }
const tabs = [UsageTab.Responses, UsageTab.NativeAccounting, UsageTab.CostEvidence] as const;
const tabLabels = { [UsageTab.Responses]: "usage.responsesTab", [UsageTab.NativeAccounting]: "usage.nativeAccountingTab", [UsageTab.CostEvidence]: "usage.costEvidence_9ab3af" } as const;

function measure(value?: UsageMeasure): string {
  if (!value || value.measuredResponses === 0 || !/^\d+$/.test(value.knownTotal)) return copy("usage.extra.ca1844969742");
  return BigInt(value.knownTotal).toLocaleString(displayLocale());
}

const primaryMeasures = () => [[copy("usage.extra.6e3886ad15d2"), "total"], [copy("usage.extra.dd856eeb5046"), "input"], [copy("usage.extra.a9b50ea0c4a7"), "output"]] as const;
const secondaryMeasures = () => [[copy("usage.extra.876a4379087b"), "cachedInput"], [copy("usage.extra.f8bc2d034686"), "cacheWriteInput"], [copy("usage.extra.f85860ca7347"), "reasoningOutput"]] as const;

// Only the dashboard may present an empty recorded subtotal as zero. Require
// every response measure so missing or inconsistent evidence stays unavailable.
function emptyRecordedSummary(value?: UsageTotals): boolean {
  return value?.responses === 0 && [...primaryMeasures(), ...secondaryMeasures()].every(([, key]) => {
    const metric = value[key];
    return metric !== undefined && metric.knownTotal === "" && metric.measuredResponses === 0 && metric.unavailableResponses === 0;
  });
}

function MeasureCard({ label, value, primary = false, emptySummary = false }: { label: string; value?: UsageMeasure; primary?: boolean; emptySummary?: boolean }) {
  useLocale();
  return <div data-token-measure className={primary ? "usage-metric usage-metric-primary" : "usage-metric usage-metric-secondary"}><dt>{label}</dt><dd>{emptySummary ? BigInt(0).toLocaleString(displayLocale()) : measure(value)}</dd><small><LocalizedText id="usage.measuredUnavailableResponses_5145e5" components={{ s0: <>{value?.measuredResponses ?? 0}</>, s1: <>{value?.unavailableResponses ?? 0}</> }} /></small></div>;
}

function Measures({ value }: { value?: UsageTotals }) {
  useLocale();
  return <dl className="usage-row-measures">{[...primaryMeasures(), ...secondaryMeasures()].map(([label, key]) => <MeasureCard key={key} label={label} value={value?.[key]} />)}</dl>;
}

function formatAppliedTime(milliseconds: bigint, timeZone: string) {
  return <Timestamp value={new Date(Number(milliseconds)).toISOString()} mode={TimestampMode.Absolute} timeZone={timeZone} />;
}

function pendingRange(selection: UsageSelection): ReactNode {
  if (selection.fromUnixMs === 0n && selection.untilUnixMs === 0n) return copy("usage.extra.5f5f75f8e14c");
  const from = selection.fromUnixMs || (selection.untilUnixMs ? selection.untilUnixMs - 30n * 86_400_000n : 0n);
  const start = from ? formatAppliedTime(from, selection.timeZone) : copy("usage.pendingStart");
  const end = selection.untilUnixMs ? formatAppliedTime(selection.untilUnixMs, selection.timeZone) : copy("usage.extra.ca936ad0748f");
  return <TimestampText id="usage.range" values={{ from: start, until: end }} />;
}

function appliedFilters(selection: UsageSelection): string[] {
  const values: string[] = [];
  if (selection.sessionId) values.push(copy("usage.sentence.15934289ffb2", { v0: selection.sessionId }));
  if (selection.projectId) values.push(copy("usage.sentence.874241e2ef76", { v0: selection.projectId }));
  if (selection.generalChat) values.push(copy("usage.extra.f634bca1f142"));
  if (selection.accountId) values.push(copy("usage.sentence.a422d5ea430e", { v0: selection.accountId }));
  if (selection.providerId) values.push(copy("usage.sentence.886fdf04e3f3", { v0: selection.providerId }));
  if (selection.subscriptionService) values.push(copy("usage.sentence.67a16314500f", { v0: SubscriptionServiceIdentity[selection.subscriptionService].toLowerCase() }));
  if (selection.modelId) values.push(copy("usage.sentence.1d3a37cc1c5e", { v0: selection.modelId }));
  return values;
}

export function Usage({ active, open, entry }: { active: boolean; open: (id: string) => void; entry?: UsageEntry }) {
  useLocale();
  const [detail, setDetail] = useState("");
  const [tab, setTab] = useState(UsageTab.Responses);
  const tabId = useId();
  const [expanded, setExpanded] = useState<AccountingDisclosures["expanded"]>({});
  const disclosures: AccountingDisclosures = { expanded, change: (kind, value) => setExpanded(current => current[kind] === value ? current : { ...current, [kind]: value }) };
  const { draft, selection, invalid, pending, change, edit, reset: resetFilters, ready } = useUsageFilters(active, entry);
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const nativeFilters = status.data?.capabilities.includes(SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1) === true;
  const result = useQuery(UsageQuery.getUsageSummary, selection, { enabled: active && ready });
  const closeDrawer = useCloseSidebarDrawer();
  const detectedTimeZone = selection.timeZone;
  const reset = () => { resetFilters(); closeDrawer(); };
  const data = result.data;
  const emptySummary = emptyRecordedSummary(data?.totals);
  const responseGroups = data?.groups.filter((group) => (group.totals?.responses ?? 0) > 0) ?? [];
  const responseAnalytics = data?.analytics ? { ...data.analytics, models: data.analytics.models.filter((model) => (model.totals?.responses ?? 0) > 0) } : undefined;
  const appliedZone = data?.analytics?.timeZone || selection.timeZone;
  const conditions = appliedFilters(selection);
  const draftChanged = pending || invalid;

  return <>
    <SidebarSurface active={active} title={copy("usage.usage_8d5982")} className="usage-sidebar">
      <p><LocalizedText id="usage.last30DaysByDefaultTimes_70537d" components={{ s0: <>{detectedTimeZone}</> }} /></p>
      <form className="sidebar-form usage-sidebar-form" onSubmit={(event) => { event.preventDefault(); }}>
        <div className="usage-filter-scroll"><fieldset><legend>{copy("usage.timeRange")}</legend>
        <label><LocalizedText id="usage.fromTime_b9f8b1" components={{ s0: <>{detectedTimeZone}</> }} /><input type="datetime-local" value={draft.from} onChange={(event) => change("from", event.target.value)} /></label>
        <label><LocalizedText id="usage.untilTimeExclusive_4c9f27" components={{ s0: <>{detectedTimeZone}</> }} /><input type="datetime-local" value={draft.until} onChange={(event) => change("until", event.target.value)} /></label>
        </fieldset><fieldset><legend>{copy("usage.scope")}</legend>
        <ResourceChoice label={copy("usage.session_6959b4")} kind={EntityKind.SESSION} value={draft.sessionId} change={(id) => change("sessionId", id)} active={active} />
        <ResourceChoice label={copy("usage.project_985959")} kind={EntityKind.PROJECT} value={draft.projectId} change={(id) => change("projectId", id)} active={active} disabled={draft.generalChat} />
        <label className="checkbox"><input type="checkbox" checked={draft.generalChat} onChange={(event) => edit({ generalChat: event.target.checked, projectId: event.target.checked ? "" : draft.projectId })} />{copy("usage.generalChatOnly_9032cc")}</label>
        </fieldset><fieldset><legend>{copy("usage.source")}</legend>
        <ResourceChoice label={copy("usage.account_7e1b0d")} kind={EntityKind.ACCOUNT} value={draft.accountId} change={(id) => change("accountId", id)} active={active} />
        <ResourceChoice label={copy("usage.provider_472590")} kind={EntityKind.PROVIDER} value={draft.providerId} change={(id) => edit({ providerId: id, subscriptionService: SubscriptionServiceIdentity.UNSPECIFIED })} active={active} />
        <label>{copy("usage.subscriptionService_0e16df")}<select disabled={!nativeFilters} value={draft.subscriptionService} onChange={(event) => edit({ providerId: "", subscriptionService: Number(event.target.value) as SubscriptionServiceIdentity })}><option value={SubscriptionServiceIdentity.UNSPECIFIED}>{copy("usage.allServices_5b9809")}</option><option value={SubscriptionServiceIdentity.CHATGPT}>{copy("usage.chatgpt_50a412")}</option><option value={SubscriptionServiceIdentity.CLAUDE}>{copy("usage.claude_061557")}</option><option value={SubscriptionServiceIdentity.GROK}>{copy("usage.grok_dca61d")}</option></select></label>
        <ResourceChoice label={copy("usage.model_5e2c61")} kind={EntityKind.MODEL} value={draft.modelId} change={(id) => change("modelId", id)} active={active} />
        <button type="button" disabled={!draft.modelId} onClick={() => { setDetail(draft.modelId); closeDrawer(); }}>{copy("usage.modelDetailsAndTokenPricing")}</button>

        </fieldset></div>
        {invalid ? <p role="alert">{copy("usage.extra.fac4f0da83d0")}</p> : null}
        <div className="actions"><button type="button" onClick={reset}>{copy("usage.resetToLast30Days_a15ab6")}</button></div>
      </form>
    </SidebarSurface>
    <section hidden={!active} className="page usage-page" aria-busy={result.isFetching}>
    {detail ? <SettingsLifetime key={detail}>{() => <MutationIntents><UsageModelDetail id={detail} active={active} close={() => setDetail("")} /></MutationIntents>}</SettingsLifetime> : null}
    <header className="usage-header"><div><h1>{copy("usage.tokenUsage_00f594")}</h1><p>{copy("usage.delidevActivityOnlyArchivedSessionsIncluded_09c1fa")}</p></div><div className="usage-header-actions"><button type="button" disabled={result.isFetching} onClick={() => void result.refetch()}>{copy("usage.refresh_0e9161")}</button></div></header>
    <div className="usage-applied" role="group" aria-label={copy("usage.appliedConditions_bc3af3")}><strong>{copy("usage.appliedConditions_bc3af3")}</strong><span>{data ? <TimestampText id="usage.exclusive_fd9e0a" values={{ v0: formatAppliedTime(data.fromUnixMs, appliedZone), v1: formatAppliedTime(data.untilUnixMs, appliedZone) }} /> : pendingRange(selection)}</span><span><LocalizedText id="usage.timezone_9229e0" components={{ s0: <>{appliedZone}</> }} /></span><span>{copy("usage.responseTimesShowWhenTheServer_c28198")}</span>{conditions.length ? <span>{conditions.join(" · ")}</span> : <span>{copy("usage.allSessionsAccountsApisAndModels_d7b7c7")}</span>}{draftChanged ? <span className="usage-draft-state">{copy(invalid ? "usage.invalidFiltersRetained" : "usage.waitingForDateInput")}</span> : null}</div>
    {draftChanged ? <p className="usage-draft-state" role="status">{copy(invalid ? "usage.invalidFiltersRetained" : "usage.waitingForDateInput")}</p> : null}<Problem error={result.error} summary={copy("usage.readHelp")} />
    {result.isFetching ? <p className="usage-loading" role="status">{data ? copy("usage.refreshingThisAppliedRange_349e6c") : copy("usage.loadingTokenUsage_ded2ab")}</p> : null}
    {data && result.error ? <p className="notice">{copy("usage.theRefreshFailedTheseAreThe_a67de1")}</p> : null}
    {!data && result.isPending ? <div className="usage-skeletons" aria-hidden="true"><div /><div /><div /><div /></div> : null}
    {data ? <>
      <div className="usage-coverage"><strong>{copy("usage.incompleteCoverage_0922dc")}</strong><span>{data.coverage === UsageCoverage.OBSERVED_ROOT_RESPONSES ? copy("usage.observedRootResponsesOnlyMissingOlder_ae66ec") : copy("usage.thisServerSTelemetryCoverageIs_d25728")}</span><span><LocalizedText id="usage.acceptedExecutionsHaveNoResponseUsage_9f2beb" components={{ s0: <>{data.acceptedExecutionsWithoutResponse.toLocaleString(displayLocale())}</> }} /></span><span><LocalizedText id="usage.nativeContextActionsHaveNoExact_9bc220" components={{ s0: <>{data.acceptedCompactionsWithoutResponse.toLocaleString(displayLocale())}</> }} /></span></div>
      {result.error ? <p className="usage-stale-indicator" role="status">{copy("usage.staleValuesFromTheLastSuccessful_26becd")}</p> : null}
    </> : null}
    <div className="usage-tabs" role="tablist" aria-label={copy("usage.analysisTabs")}>
      {tabs.map((value, index) => <button key={value} type="button" role="tab" id={`${tabId}-${value}-tab`} aria-controls={`${tabId}-${value}-panel`} aria-selected={tab === value} tabIndex={tab === value ? 0 : -1} onClick={() => setTab(value)} onKeyDown={(event) => {
        const next = event.key === "ArrowRight" ? tabs[(index + 1) % tabs.length] : event.key === "ArrowLeft" ? tabs[(index + tabs.length - 1) % tabs.length] : event.key === "Home" ? tabs[0] : event.key === "End" ? tabs[tabs.length - 1] : undefined;
        if (!next) return;
        event.preventDefault(); setTab(next); window.document.getElementById(`${tabId}-${next}-tab`)?.focus();
      }}>{copy(tabLabels[value])}</button>)}
    </div>
    {!data ? tabs.map(value => <div key={value} role="tabpanel" id={`${tabId}-${value}-panel`} aria-labelledby={`${tabId}-${value}-tab`} hidden={tab !== value} />) : null}
    {data ? <>
      <div role="tabpanel" id={`${tabId}-${UsageTab.Responses}-panel`} aria-labelledby={`${tabId}-${UsageTab.Responses}-tab`} hidden={tab !== UsageTab.Responses}>
      <section className="usage-summary" aria-labelledby="usage-summary-title"><h2 id="usage-summary-title">{copy("usage.knownTokenTotals_17a07e")}</h2><p><LocalizedText id="usage.distinctResponsesRecorded_baaa83" components={{ s0: <>{data.totals?.responses.toLocaleString(displayLocale()) ?? "0"}</> }} /></p>
        <dl className="usage-metrics-primary">{primaryMeasures().map(([label, key]) => <MeasureCard key={key} primary label={label} emptySummary={emptySummary} value={data.totals?.[key]} />)}<div className="usage-metric usage-recorded-responses"><dt>{copy("usage.recordedResponses")}</dt><dd>{data.totals?.responses.toLocaleString(displayLocale()) ?? copy("usage.extra.ca1844969742")}</dd><small>{copy("usage.exactResponseRecords")}</small></div></dl>
        <details className="usage-summary-breakdown"><summary>{copy("usage.tokenBreakdown_c7576a")}</summary><dl className="usage-metrics-secondary">{secondaryMeasures().map(([label, key]) => <MeasureCard key={key} label={label} emptySummary={emptySummary} value={data.totals?.[key]} />)}</dl>
        <p className="usage-subset-note">{copy("usage.cachedInputIsPartOfInput_fa92e1")}</p></details>
      </section>
      <section className="usage-detail" aria-labelledby="usage-detail-title"><h2 id="usage-detail-title">{copy("usage.sessionModelAndAccountDetails_778c53")}</h2>
        {responseGroups.length ? <div className="usage-table" role="region" aria-label={copy("usage.sessionModelAndAccountUsageTable_fc350c")} tabIndex={0}><table><caption>{copy("usage.knownResponseSubtotalsWithOriginalSession_e1cb71")}</caption><thead><tr><th scope="col">{copy("usage.sessionProject_59a44c")}</th><th scope="col">{copy("usage.account_7e1b0d")}</th><th scope="col">{copy("usage.modelApi_6a8129")}</th><th scope="col">{copy("usage.extra.dd856eeb5046")}</th><th scope="col">{copy("usage.extra.a9b50ea0c4a7")}</th><th scope="col">{copy("usage.extra.6e3886ad15d2")}</th><th scope="col">{copy("usage.recordedResponses")}</th><th scope="col">{copy("usage.tokenPriceEstimate_ed3009")}</th></tr></thead><tbody>{responseGroups.map((group) => <tr key={`${group.sessionId}:${group.accountId}:${group.providerId}:${subscriptionServiceLabel(group.subscriptionService)}:${group.modelId}`}>
          <td><button type="button" onClick={() => open(group.sessionId)}>{group.sessionName || group.sessionId}</button><small>{group.sessionId}</small><p>{group.projectId ? group.projectName || copy("usage.sentence.874241e2ef76", { v0: group.projectId }) : copy("usage.generalChat_f634bc")}</p>{group.projectId ? <small>{group.projectId}</small> : null}</td>
          <td><span>{group.accountName || copy("usage.extra.415673677e87")}</span><small>{group.accountId}</small></td>
          <td><button type="button" onClick={() => setDetail(group.modelId)}>{group.modelName || copy("usage.extra.a99bc331d9ad")}</button><small>{group.modelId}</small><p>{group.subscriptionService ? copy("usage.subscriptionService_67a163", { v0: subscriptionServiceLabel(group.subscriptionService) }) : group.providerName || copy("usage.sentence.886fdf04e3f3", { v0: group.providerId })}</p><small>{group.providerId}</small></td>
          <td>{measure(group.totals?.input)}</td><td>{measure(group.totals?.output)}</td><td><strong>{measure(group.totals?.total)}</strong><p><LocalizedText id="usage.responses_5238cc" components={{ s0: <>{group.totals?.responses.toLocaleString(displayLocale())}</>, s1: <>{group.totals?.total?.unavailableResponses ? copy("usage.unavailable_e4701b", { v0: group.totals.total.unavailableResponses }) : ""}</> }} /></p><details><summary>{copy("usage.tokenBreakdown_c7576a")}</summary><Measures value={group.totals} /></details></td><td>{group.totals?.responses.toLocaleString(displayLocale()) ?? copy("usage.extra.ca1844969742")}</td><td><EstimateAmounts value={group.estimates} /></td>
        </tr>)}</tbody></table></div> : <p>{copy("usage.noExactResponseUsageIsRecorded_d85b24")}</p>}
      </section>
      <details className="usage-trends"><summary>{copy("usage.dailyAndModelTrends")}</summary>
      {responseAnalytics?.granularity === UsageTimeGranularity.DAY ? <UsageCharts analytics={responseAnalytics} timeZone={appliedZone} /> : <p className="usage-charts-unavailable" role="status">{copy("usage.dailyAndModelChartsAreUnavailable_a70ff8")}</p>}
      </details>
      <details className="usage-definitions"><summary>{copy("usage.accountingDefinitions")}</summary><p>{copy("usage.responseTimesShowWhenTheServer_c28198")}</p><p>{copy("usage.cachedInputIsPartOfInput_fa92e1")}</p></details>
      </div>
      <div role="tabpanel" id={`${tabId}-${UsageTab.NativeAccounting}-panel`} aria-labelledby={`${tabId}-${UsageTab.NativeAccounting}-tab`} hidden={tab !== UsageTab.NativeAccounting}>
      <GrokAccounting data={data} open={open} disclosures={disclosures} />
      <NativeAccounting data={data} open={open} disclosures={disclosures} />
      </div>
      <div role="tabpanel" id={`${tabId}-${UsageTab.CostEvidence}-panel`} aria-labelledby={`${tabId}-${UsageTab.CostEvidence}-tab`} hidden={tab !== UsageTab.CostEvidence}>
      <section className="usage-costs" aria-labelledby="usage-cost-title"><h2 id="usage-cost-title">{copy("usage.costEvidence_9ab3af")}</h2><p><LocalizedText id="usage.unavailableNoVerifiedAttributableChargeIs_35ada6" components={{ s0: <strong>{copy("usage.actualApiCost_c88286")}</strong> }} /></p><EstimateCosts totals={data.estimates} pricing={data.pricing} /><p>{copy("usage.completeTokenPriceCategoriesDoNot_ddbf3a")}</p></section>
      </div>
    </> : null}
  </section></>;
}

function UsageModelDetail({ id, active, close }: { id: string; active: boolean; close: () => void }) {
  useLocale();
  const [pricing, setPricing] = useState(false);
  const current = useQuery(ResourceQuery.getResource, { kind: EntityKind.MODEL, id }, { enabled: active, refetchInterval: active ? 5000 : false });
  const model = current.data?.resource;
  const value = document(model);
  return <section className="usage-detail" aria-label={copy("usage.modelDetails")}>
    <h2>{copy("usage.modelDetailsTitle", { v0: model ? resourceName(model) : id })}</h2>
    <Problem error={current.error} summary={copy("usage.modelReadHelp")} />
    {current.isLoading ? <p role="status">{copy("usage.loadingModelDetails")}</p> : null}
    {current.error && model ? <p role="status">{copy("usage.staleModelDetails")}</p> : null}
    {model ? <><p>{copy("usage.modelAndNativeId", { v0: id, v1: text(value.native_id) || copy("usage.extra.ca1844969742") })}</p><p>{value.source_kind === "subscription" ? copy("usage.subscriptionServiceDetail", { v0: text(value.subscription_service) }) : copy("usage.apiProviderDetail", { v0: text(value.provider_id) })}</p>
      {value.retired === true ? <p>{copy("usage.retiredModelPricingUnavailable")}</p> : pricing ? <ModelPricing model={model} active={active && !current.error} close={() => { setPricing(false); void current.refetch(); }} /> : <button type="button" disabled={!supportsResourceSchema(model) || Boolean(current.error)} onClick={() => setPricing(true)}>{copy("usage.tokenPricing")}</button>}
    </> : null}
    <div className="actions"><button type="button" disabled={current.isFetching} onClick={() => void current.refetch()}>{copy("usage.refreshModelDetails")}</button><button type="button" onClick={close}>{copy("usage.backToUsage")}</button></div>
  </section>;
}
