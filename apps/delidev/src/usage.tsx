import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, UsageAccountingProfile, UsageCoverage, UsageQuery, UsageTimeGranularity, type UsageMeasure, type UsageTotals } from "@delinoio/delidev-api-client";
import { EstimateAmounts, EstimateCosts } from "./estimate-costs";
import { ResourceChoice } from "./configuration-fields";
import { Problem } from "./ui";
import { SidebarSurface, useCloseSidebarDrawer } from "./sidebar-context";
import { detectDeviceTimeZone, localDateTimeToUnixMs } from "./usage-time";
import { UsageCharts } from "./usage-chart";
import { GrokAccounting } from "./grok-accounting";

interface Filters { from: string; until: string; sessionId: string; projectId: string; accountId: string; providerId: string; modelId: string; generalChat: boolean }
const emptyFilters: Filters = { from: "", until: "", sessionId: "", projectId: "", accountId: "", providerId: "", modelId: "", generalChat: false };

function request(filters: Filters, timeZone: string) {
  const { from, until, ...selection } = filters;
  return { ...selection, fromUnixMs: localDateTimeToUnixMs(from, timeZone), untilUnixMs: localDateTimeToUnixMs(until, timeZone), granularity: UsageTimeGranularity.DAY, timeZone, accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1 };
}

function sameFilters(left: Filters, right: Filters): boolean {
  return left.from === right.from && left.until === right.until && left.sessionId === right.sessionId && left.projectId === right.projectId && left.accountId === right.accountId && left.providerId === right.providerId && left.modelId === right.modelId && left.generalChat === right.generalChat;
}

function measure(value?: UsageMeasure): string {
  if (!value || value.measuredResponses === 0 || !/^\d+$/.test(value.knownTotal)) return "Unavailable";
  return BigInt(value.knownTotal).toLocaleString();
}

const primaryMeasures = [["Known total tokens", "total"], ["Input tokens", "input"], ["Output tokens", "output"]] as const;
const secondaryMeasures = [["Cached input", "cachedInput"], ["Cache-write input", "cacheWriteInput"], ["Reasoning output", "reasoningOutput"]] as const;

function MeasureCard({ label, value, primary = false }: { label: string; value?: UsageMeasure; primary?: boolean }) {
  return <div className={primary ? "usage-metric usage-metric-primary" : "usage-metric usage-metric-secondary"}><dt>{label}</dt><dd>{measure(value)}</dd><small>{value?.measuredResponses ?? 0} measured · {value?.unavailableResponses ?? 0} unavailable responses</small></div>;
}

function Measures({ value }: { value?: UsageTotals }) {
  return <dl className="usage-row-measures">{[...primaryMeasures, ...secondaryMeasures].map(([label, key]) => <MeasureCard key={label} label={label} value={value?.[key]} />)}</dl>;
}

function formatAppliedTime(milliseconds: bigint, timeZone: string): string {
  return new Intl.DateTimeFormat(undefined, { timeZone, year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit", timeZoneName: "shortOffset" }).format(new Date(Number(milliseconds)));
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
  if (selection.modelId) values.push(`Model ${selection.modelId}`);
  return values;
}

export function Usage({ active, open }: { active: boolean; open: (id: string) => void }) {
  const [draft, setDraft] = useState<Filters>(emptyFilters);
  const [appliedDraft, setAppliedDraft] = useState<Filters>(emptyFilters);
  const [selection, setSelection] = useState(() => request(emptyFilters, detectDeviceTimeZone()));
  const [invalid, setInvalid] = useState("");
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
    <SidebarSurface active={active} title="Usage">
      <p>Last 30 days by default · Times use {detectedTimeZone}.</p>
      <form className="sidebar-form" onSubmit={(event) => { event.preventDefault(); apply(); }}>
        <label>From ({detectedTimeZone} time)<input type="datetime-local" value={draft.from} onChange={(event) => change("from", event.target.value)} /></label>
        <label>Until ({detectedTimeZone} time, exclusive)<input type="datetime-local" value={draft.until} onChange={(event) => change("until", event.target.value)} /></label>
        <ResourceChoice label="Session" kind={EntityKind.SESSION} value={draft.sessionId} change={(id) => change("sessionId", id)} active={active} />
        <ResourceChoice label="Project" kind={EntityKind.PROJECT} value={draft.projectId} change={(id) => change("projectId", id)} active={active} disabled={draft.generalChat} />
        <ResourceChoice label="Account" kind={EntityKind.ACCOUNT} value={draft.accountId} change={(id) => change("accountId", id)} active={active} />
        <ResourceChoice label="Provider" kind={EntityKind.PROVIDER} value={draft.providerId} change={(id) => change("providerId", id)} active={active} />
        <ResourceChoice label="Model" kind={EntityKind.MODEL} value={draft.modelId} change={(id) => change("modelId", id)} active={active} />
        <label className="checkbox"><input type="checkbox" checked={draft.generalChat} onChange={(event) => setDraft((current) => ({ ...current, generalChat: event.target.checked, projectId: event.target.checked ? "" : current.projectId }))} />General Chat only</label>
        {invalid ? <p role="alert">{invalid}</p> : null}
        <div className="actions"><button type="submit" className="primary">Apply filters</button><button type="button" onClick={reset}>Reset to last 30 days</button></div>
      </form>
    </SidebarSurface>
    <section hidden={!active} className="page usage-page" aria-busy={result.isFetching}>
    <header className="usage-header"><div><h1>Token Usage</h1><p>DeliDev activity only · Archived sessions included</p></div><div className="usage-header-actions"><button type="button" disabled={result.isFetching} onClick={() => void result.refetch()}>Refresh</button></div></header>
    <div className="usage-applied" role="group" aria-label="Applied conditions"><strong>Applied conditions</strong><span>{data ? `${formatAppliedTime(data.fromUnixMs, appliedZone)} – ${formatAppliedTime(data.untilUnixMs, appliedZone)} (exclusive)` : pendingRange(selection)}</span><span>Timezone: {appliedZone}</span><span>Times show when the server first retained each response, not provider execution time.</span>{conditions.length ? <span>{conditions.join(" · ")}</span> : <span>All sessions, accounts, APIs and models</span>}{draftChanged ? <span className="usage-draft-state">Unapplied filter edits</span> : null}</div>
    {draftChanged ? <p className="usage-draft-state" role="status">Unapplied filter edits are in the Usage sidebar.</p> : null}<Problem error={result.error} />
    {result.isFetching ? <p className="usage-loading" role="status">{data ? "Refreshing this applied range…" : "Loading token usage…"}</p> : null}
    {data && result.error ? <p className="notice">The refresh failed. These are the last successfully retrieved values for this applied range; the displayed data is stale.</p> : null}
    {!data && result.isPending ? <div className="usage-skeletons" aria-hidden="true"><div /><div /><div /><div /></div> : null}
    {data ? <>
      <section className="usage-summary" aria-labelledby="usage-summary-title"><h2 id="usage-summary-title">Known token totals</h2><p>{data.totals?.responses.toLocaleString() ?? "0"} distinct responses recorded</p>
        <dl className="usage-metrics-primary">{primaryMeasures.map(([label, key]) => <MeasureCard key={label} primary label={label} value={data.totals?.[key]} />)}</dl>
        <dl className="usage-metrics-secondary">{secondaryMeasures.map(([label, key]) => <MeasureCard key={label} label={label} value={data.totals?.[key]} />)}</dl>
        <p className="usage-subset-note">Cached input is part of input, and reasoning output is part of output. These subsets are not added again to known total tokens.</p>
      </section>
      <div className="usage-coverage"><strong>Incomplete coverage</strong><span>{data.coverage === UsageCoverage.OBSERVED_ROOT_RESPONSES ? "Observed root responses only. Missing, older, resumed-conversation, child and unsupported harness telemetry is unavailable, not zero." : "This server's telemetry coverage is unknown."}</span><span>{data.acceptedExecutionsWithoutResponse.toLocaleString()} accepted executions have no response usage recorded in this range. This does not establish zero actual usage.</span></div>
      {result.error ? <p className="usage-stale-indicator" role="status">Stale values from the last successful read</p> : null}
      {responseAnalytics?.granularity === UsageTimeGranularity.DAY ? <UsageCharts analytics={responseAnalytics} timeZone={appliedZone} /> : <p className="usage-charts-unavailable" role="status">Daily and model charts are unavailable from this server version. Update the DeliDev server to view analytics; the current summary and detail data remain available.</p>}
      <section className="usage-detail" aria-labelledby="usage-detail-title"><h2 id="usage-detail-title">Session, model and account details</h2>
        {responseGroups.length ? <div className="usage-table" role="region" aria-label="Session, model and account usage table; scroll horizontally to inspect all details" tabIndex={0}><table><caption>Known response subtotals with original session, project, account, API and model identities</caption><thead><tr><th scope="col">Session / project</th><th scope="col">Account</th><th scope="col">Model / API</th><th scope="col">Tokens</th><th scope="col">Token-price estimate</th></tr></thead><tbody>{responseGroups.map((group) => <tr key={`${group.sessionId}:${group.accountId}:${group.providerId}:${group.modelId}`}>
          <td><button type="button" onClick={() => open(group.sessionId)}>{group.sessionName || group.sessionId}</button><small>{group.sessionId}</small><p>{group.projectId ? group.projectName || `Project ${group.projectId}` : "General Chat"}</p>{group.projectId ? <small>{group.projectId}</small> : null}</td>
          <td><span>{group.accountName || "Retained account"}</span><small>{group.accountId}</small></td>
          <td><span>{group.modelName || "Retained model"}</span><small>{group.modelId}</small><p>{group.providerName || `API ${group.providerId}`}</p><small>{group.providerId}</small></td>
          <td><strong>{measure(group.totals?.total)}</strong><p>{group.totals?.responses.toLocaleString()} responses{group.totals?.total?.unavailableResponses ? ` · ${group.totals.total.unavailableResponses} unavailable` : ""}</p><details><summary>Token breakdown</summary><Measures value={group.totals} /></details></td><td><EstimateAmounts value={group.estimates} /></td>
        </tr>)}</tbody></table></div> : <p>No exact response usage is recorded for these filters. This does not mean zero usage or zero cost.</p>}
      </section>
      <GrokAccounting data={data} open={open} />
      <section className="usage-costs" aria-labelledby="usage-cost-title"><h2 id="usage-cost-title">Cost evidence</h2><p><strong>Actual API cost:</strong> Unavailable — no verified attributable charge is supplied by the current telemetry.</p><EstimateCosts totals={data.estimates} pricing={data.pricing} /><p>Complete token-price categories do not establish complete telemetry, billed spend, a billing ceiling or budget compliance. Historical estimates remain separated by currency and original price basis.</p></section>
    </> : null}
  </section></>;
}
