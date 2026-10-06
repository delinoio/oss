import { LocalizedText, copy, displayLocale, useLocale } from "./localization";
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { subscriptionServiceLabel, SubscriptionServiceIdentity, SystemCapability, SystemQuery, EntityKind, UsageAccountingProfile, UsageCoverage, UsageQuery, UsageTimeGranularity, type UsageMeasure, type UsageTotals } from "@delinoio/delidev-api-client";
import { EstimateAmounts, EstimateCosts } from "./estimate-costs";
import { ResourceChoice } from "./configuration-fields";
import { Problem } from "./ui";
import { SidebarSurface, useCloseSidebarDrawer } from "./sidebar-context";
import { detectDeviceTimeZone, localDateTimeToUnixMs } from "./usage-time";
import { UsageCharts } from "./usage-chart";
import { GrokAccounting } from "./grok-accounting";
import { NativeAccounting } from "./native-accounting";

interface Filters { from: string; until: string; sessionId: string; projectId: string; accountId: string; providerId: string; subscriptionService: SubscriptionServiceIdentity; modelId: string; generalChat: boolean }
const emptyFilters: Filters = { from: "", until: "", sessionId: "", projectId: "", accountId: "", providerId: "", subscriptionService: SubscriptionServiceIdentity.UNSPECIFIED, modelId: "", generalChat: false };

function request(filters: Filters, timeZone: string) {
  const { from, until, ...selection } = filters;
  return { ...selection, fromUnixMs: localDateTimeToUnixMs(from, timeZone), untilUnixMs: localDateTimeToUnixMs(until, timeZone), granularity: UsageTimeGranularity.DAY, timeZone, accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1 };
}

function sameFilters(left: Filters, right: Filters): boolean {
  return left.from === right.from && left.until === right.until && left.sessionId === right.sessionId && left.projectId === right.projectId && left.accountId === right.accountId && left.providerId === right.providerId && left.subscriptionService === right.subscriptionService && left.modelId === right.modelId && left.generalChat === right.generalChat;
}

function measure(value?: UsageMeasure): string {
  if (!value || value.measuredResponses === 0 || !/^\d+$/.test(value.knownTotal)) return "Unavailable";
  return BigInt(value.knownTotal).toLocaleString(displayLocale());
}

const primaryMeasures = [["Known total tokens", "total"], ["Input tokens", "input"], ["Output tokens", "output"]] as const;
const secondaryMeasures = [["Cached input", "cachedInput"], ["Cache-write input", "cacheWriteInput"], ["Reasoning output", "reasoningOutput"]] as const;

function MeasureCard({ label, value, primary = false }: { label: string; value?: UsageMeasure; primary?: boolean }) {
  useLocale();
  return <div className={primary ? "usage-metric usage-metric-primary" : "usage-metric usage-metric-secondary"}><dt>{label}</dt><dd>{measure(value)}</dd><small><LocalizedText id="usage.measuredUnavailableResponses_5145e5" components={{ s0: <>{value?.measuredResponses ?? 0}</>, s1: <>{value?.unavailableResponses ?? 0}</> }} /></small></div>;
}

function Measures({ value }: { value?: UsageTotals }) {
  useLocale();
  return <dl className="usage-row-measures">{[...primaryMeasures, ...secondaryMeasures].map(([label, key]) => <MeasureCard key={label} label={label} value={value?.[key]} />)}</dl>;
}

function formatAppliedTime(milliseconds: bigint, timeZone: string): string {
  return new Intl.DateTimeFormat(displayLocale(), { timeZone, year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit", timeZoneName: "shortOffset" }).format(new Date(Number(milliseconds)));
}

function pendingRange(selection: ReturnType<typeof request>): string {
  if (selection.fromUnixMs === 0n && selection.untilUnixMs === 0n) return "Last 30 days · server time";
  const from = selection.fromUnixMs || (selection.untilUnixMs ? selection.untilUnixMs - 30n * 86_400_000n : 0n);
  const start = from ? formatAppliedTime(from, selection.timeZone) : "30 days before server now";
  const end = selection.untilUnixMs ? formatAppliedTime(selection.untilUnixMs, selection.timeZone) : "server now";
  return `${start} – ${end} (exclusive)`;
}

function appliedFilters(selection: ReturnType<typeof request>): string[] {
  const values: string[] = [];
  if (selection.sessionId) values.push(`Session ${selection.sessionId}`);
  if (selection.projectId) values.push(`Project ${selection.projectId}`);
  if (selection.generalChat) values.push("General Chat");
  if (selection.accountId) values.push(`Account ${selection.accountId}`);
  if (selection.providerId) values.push(`API ${selection.providerId}`);
  if (selection.subscriptionService) values.push(`Subscription service ${SubscriptionServiceIdentity[selection.subscriptionService].toLowerCase()}`);
  if (selection.modelId) values.push(`Model ${selection.modelId}`);
  return values;
}

export function Usage({ active, open }: { active: boolean; open: (id: string) => void }) {
  useLocale();
  const [draft, setDraft] = useState<Filters>(emptyFilters);
  const [appliedDraft, setAppliedDraft] = useState<Filters>(emptyFilters);
  const [selection, setSelection] = useState(() => request(emptyFilters, detectDeviceTimeZone()));
  const [invalid, setInvalid] = useState("");
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const nativeFilters = status.data?.capabilities.includes(SystemCapability.SUBSCRIPTION_SERVICE_ACCOUNTS_V1) === true;
  const result = useQuery(UsageQuery.getUsageSummary, selection, { enabled: active });
  const closeDrawer = useCloseSidebarDrawer();
  const detectedTimeZone = detectDeviceTimeZone();
  const change = <K extends keyof Filters>(key: K, value: Filters[K]) => setDraft((current) => ({ ...current, [key]: value }));
  const apply = () => {
    try {
      const zone = detectedTimeZone;
      const next = request(draft, zone);
      const until = next.untilUnixMs || BigInt(Date.now());
      const from = next.fromUnixMs || until - 30n * 86_400_000n;
      if (from <= 0n || until <= from || until - from > 366n * 86_400_000n) throw new Error("range");
      setInvalid("");
      setSelection(next);
      setAppliedDraft({ ...draft });
      closeDrawer();
    } catch {
      setInvalid("Choose valid local date and time values in the detected IANA timezone, with From before the exclusive Until and no more than 366 days apart.");
    }
  };
  const reset = () => {
    const zone = detectDeviceTimeZone();
    setDraft(emptyFilters);
    setAppliedDraft(emptyFilters);
    setSelection(request(emptyFilters, zone));
    setInvalid("");
    closeDrawer();
  };
  const data = result.data;
  const responseGroups = data?.groups.filter((group) => (group.totals?.responses ?? 0) > 0) ?? [];
  const responseAnalytics = data?.analytics ? { ...data.analytics, models: data.analytics.models.filter((model) => (model.totals?.responses ?? 0) > 0) } : undefined;
  const appliedZone = data?.analytics?.timeZone || selection.timeZone;
  const conditions = appliedFilters(selection);
  const draftChanged = !sameFilters(draft, appliedDraft);

  return <>
    <SidebarSurface active={active} title={copy("usage.usage_8d5982")}>
      <p><LocalizedText id="usage.last30DaysByDefaultTimes_70537d" components={{ s0: <>{detectedTimeZone}</> }} /></p>
      <form className="sidebar-form" onSubmit={(event) => { event.preventDefault(); apply(); }}>
        <label><LocalizedText id="usage.fromTime_b9f8b1" components={{ s0: <>{detectedTimeZone}</> }} /><input type="datetime-local" value={draft.from} onChange={(event) => change("from", event.target.value)} /></label>
        <label><LocalizedText id="usage.untilTimeExclusive_4c9f27" components={{ s0: <>{detectedTimeZone}</> }} /><input type="datetime-local" value={draft.until} onChange={(event) => change("until", event.target.value)} /></label>
        <ResourceChoice label={copy("usage.session_6959b4")} kind={EntityKind.SESSION} value={draft.sessionId} change={(id) => change("sessionId", id)} active={active} />
        <ResourceChoice label={copy("usage.project_985959")} kind={EntityKind.PROJECT} value={draft.projectId} change={(id) => change("projectId", id)} active={active} disabled={draft.generalChat} />
        <ResourceChoice label={copy("usage.account_7e1b0d")} kind={EntityKind.ACCOUNT} value={draft.accountId} change={(id) => change("accountId", id)} active={active} />
        <ResourceChoice label={copy("usage.provider_472590")} kind={EntityKind.PROVIDER} value={draft.providerId} change={(id) => setDraft((current) => ({ ...current, providerId: id, subscriptionService: SubscriptionServiceIdentity.UNSPECIFIED }))} active={active} />
        <label>{copy("usage.subscriptionService_0e16df")}<select disabled={!nativeFilters} value={draft.subscriptionService} onChange={(event) => setDraft((current) => ({ ...current, providerId: "", subscriptionService: Number(event.target.value) as SubscriptionServiceIdentity }))}><option value={SubscriptionServiceIdentity.UNSPECIFIED}>{copy("usage.allServices_5b9809")}</option><option value={SubscriptionServiceIdentity.CHATGPT}>{copy("usage.chatgpt_50a412")}</option><option value={SubscriptionServiceIdentity.CLAUDE}>{copy("usage.claude_061557")}</option><option value={SubscriptionServiceIdentity.GROK}>{copy("usage.grok_dca61d")}</option></select></label>
        <ResourceChoice label={copy("usage.model_5e2c61")} kind={EntityKind.MODEL} value={draft.modelId} change={(id) => change("modelId", id)} active={active} />
        <label className="checkbox"><input type="checkbox" checked={draft.generalChat} onChange={(event) => setDraft((current) => ({ ...current, generalChat: event.target.checked, projectId: event.target.checked ? "" : current.projectId }))} />{copy("usage.generalChatOnly_9032cc")}</label>
        {invalid ? <p role="alert">{invalid}</p> : null}
        <div className="actions"><button type="submit" className="primary">{copy("usage.applyFilters_d80ab1")}</button><button type="button" onClick={reset}>{copy("usage.resetToLast30Days_a15ab6")}</button></div>
      </form>
    </SidebarSurface>
    <section hidden={!active} className="page usage-page" aria-busy={result.isFetching}>
    <header className="usage-header"><div><h1>{copy("usage.tokenUsage_00f594")}</h1><p>{copy("usage.delidevActivityOnlyArchivedSessionsIncluded_09c1fa")}</p></div><div className="usage-header-actions"><button type="button" disabled={result.isFetching} onClick={() => void result.refetch()}>{copy("usage.refresh_0e9161")}</button></div></header>
    <div className="usage-applied" role="group" aria-label={copy("usage.appliedConditions_bc3af3")}><strong>{copy("usage.appliedConditions_bc3af3")}</strong><span>{data ? copy("usage.exclusive_fd9e0a", { v0: formatAppliedTime(data.fromUnixMs, appliedZone), v1: formatAppliedTime(data.untilUnixMs, appliedZone) }) : pendingRange(selection)}</span><span><LocalizedText id="usage.timezone_9229e0" components={{ s0: <>{appliedZone}</> }} /></span><span>{copy("usage.responseTimesShowWhenTheServer_c28198")}</span>{conditions.length ? <span>{conditions.join(" · ")}</span> : <span>{copy("usage.allSessionsAccountsApisAndModels_d7b7c7")}</span>}{draftChanged ? <span className="usage-draft-state">{copy("usage.unappliedFilterEdits_f387c1")}</span> : null}</div>
    {draftChanged ? <p className="usage-draft-state" role="status">{copy("usage.unappliedFilterEditsAreInThe_b69e11")}</p> : null}<Problem error={result.error} />
    {result.isFetching ? <p className="usage-loading" role="status">{data ? copy("usage.refreshingThisAppliedRange_349e6c") : copy("usage.loadingTokenUsage_ded2ab")}</p> : null}
    {data && result.error ? <p className="notice">{copy("usage.theRefreshFailedTheseAreThe_a67de1")}</p> : null}
    {!data && result.isPending ? <div className="usage-skeletons" aria-hidden="true"><div /><div /><div /><div /></div> : null}
    {data ? <>
      <section className="usage-summary" aria-labelledby="usage-summary-title"><h2 id="usage-summary-title">{copy("usage.knownTokenTotals_17a07e")}</h2><p><LocalizedText id="usage.distinctResponsesRecorded_baaa83" components={{ s0: <>{data.totals?.responses.toLocaleString(displayLocale()) ?? "0"}</> }} /></p>
        <dl className="usage-metrics-primary">{primaryMeasures.map(([label, key]) => <MeasureCard key={label} primary label={label} value={data.totals?.[key]} />)}</dl>
        <dl className="usage-metrics-secondary">{secondaryMeasures.map(([label, key]) => <MeasureCard key={label} label={label} value={data.totals?.[key]} />)}</dl>
        <p className="usage-subset-note">{copy("usage.cachedInputIsPartOfInput_fa92e1")}</p>
      </section>
      <div className="usage-coverage"><strong>{copy("usage.incompleteCoverage_0922dc")}</strong><span>{data.coverage === UsageCoverage.OBSERVED_ROOT_RESPONSES ? copy("usage.observedRootResponsesOnlyMissingOlder_ae66ec") : copy("usage.thisServerSTelemetryCoverageIs_d25728")}</span><span><LocalizedText id="usage.acceptedExecutionsHaveNoResponseUsage_9f2beb" components={{ s0: <>{data.acceptedExecutionsWithoutResponse.toLocaleString(displayLocale())}</> }} /></span><span><LocalizedText id="usage.nativeContextActionsHaveNoExact_9bc220" components={{ s0: <>{data.acceptedCompactionsWithoutResponse.toLocaleString(displayLocale())}</> }} /></span></div>
      {result.error ? <p className="usage-stale-indicator" role="status">{copy("usage.staleValuesFromTheLastSuccessful_26becd")}</p> : null}
      {responseAnalytics?.granularity === UsageTimeGranularity.DAY ? <UsageCharts analytics={responseAnalytics} timeZone={appliedZone} /> : <p className="usage-charts-unavailable" role="status">{copy("usage.dailyAndModelChartsAreUnavailable_a70ff8")}</p>}
      <section className="usage-detail" aria-labelledby="usage-detail-title"><h2 id="usage-detail-title">{copy("usage.sessionModelAndAccountDetails_778c53")}</h2>
        {responseGroups.length ? <div className="usage-table" role="region" aria-label={copy("usage.sessionModelAndAccountUsageTable_fc350c")} tabIndex={0}><table><caption>{copy("usage.knownResponseSubtotalsWithOriginalSession_e1cb71")}</caption><thead><tr><th scope="col">{copy("usage.sessionProject_59a44c")}</th><th scope="col">{copy("usage.account_7e1b0d")}</th><th scope="col">{copy("usage.modelApi_6a8129")}</th><th scope="col">{copy("usage.tokens_a039df")}</th><th scope="col">{copy("usage.tokenPriceEstimate_ed3009")}</th></tr></thead><tbody>{responseGroups.map((group) => <tr key={`${group.sessionId}:${group.accountId}:${group.providerId}:${subscriptionServiceLabel(group.subscriptionService)}:${group.modelId}`}>
          <td><button type="button" onClick={() => open(group.sessionId)}>{group.sessionName || group.sessionId}</button><small>{group.sessionId}</small><p>{group.projectId ? group.projectName || `Project ${group.projectId}` : copy("usage.generalChat_f634bc")}</p>{group.projectId ? <small>{group.projectId}</small> : null}</td>
          <td><span>{group.accountName || "Retained account"}</span><small>{group.accountId}</small></td>
          <td><span>{group.modelName || "Retained model"}</span><small>{group.modelId}</small><p>{group.subscriptionService ? copy("usage.subscriptionService_67a163", { v0: subscriptionServiceLabel(group.subscriptionService) }) : group.providerName || `API ${group.providerId}`}</p><small>{group.providerId}</small></td>
          <td><strong>{measure(group.totals?.total)}</strong><p><LocalizedText id="usage.responses_5238cc" components={{ s0: <>{group.totals?.responses.toLocaleString(displayLocale())}</>, s1: <>{group.totals?.total?.unavailableResponses ? copy("usage.unavailable_e4701b", { v0: group.totals.total.unavailableResponses }) : ""}</> }} /></p><details><summary>{copy("usage.tokenBreakdown_c7576a")}</summary><Measures value={group.totals} /></details></td><td><EstimateAmounts value={group.estimates} /></td>
        </tr>)}</tbody></table></div> : <p>{copy("usage.noExactResponseUsageIsRecorded_d85b24")}</p>}
      </section>
      <GrokAccounting data={data} open={open} />
      <NativeAccounting data={data} open={open} />
      <section className="usage-costs" aria-labelledby="usage-cost-title"><h2 id="usage-cost-title">{copy("usage.costEvidence_9ab3af")}</h2><p><LocalizedText id="usage.unavailableNoVerifiedAttributableChargeIs_35ada6" components={{ s0: <strong>{copy("usage.actualApiCost_c88286")}</strong> }} /></p><EstimateCosts totals={data.estimates} pricing={data.pricing} /><p>{copy("usage.completeTokenPriceCategoriesDoNot_ddbf3a")}</p></section>
    </> : null}
  </section></>;
}
