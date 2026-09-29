import { EstimateAmounts, EstimateCosts } from "./estimate-costs";
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, UsageCoverage, UsageQuery, type UsageMeasure, type UsageTotals } from "@delinoio/delidev-api-client";
import { ResourceChoice } from "./configuration-fields";
import { Problem } from "./ui";
import { SidebarSurface, useCloseSidebarDrawer } from "./sidebar-context";

interface Filters { from: string; until: string; sessionId: string; projectId: string; accountId: string; providerId: string; modelId: string; generalChat: boolean }
const emptyFilters: Filters = { from: "", until: "", sessionId: "", projectId: "", accountId: "", providerId: "", modelId: "", generalChat: false };
function timestamp(value: string): bigint { return value ? BigInt(new Date(value).getTime()) : 0n; }
function request(filters: Filters) { const { from, until, ...selection } = filters; return { ...selection, fromUnixMs: timestamp(from), untilUnixMs: timestamp(until) }; }
function measure(value?: UsageMeasure) {
  if (!value?.measuredResponses || !/^\d+$/.test(value.knownTotal)) return "Unavailable";
  return BigInt(value.knownTotal).toLocaleString();
}
function Measures({ value }: { value?: UsageTotals }) {
  return <dl className="usage-stats">{([
    ["Total tokens", value?.total], ["Input tokens", value?.input], ["Output tokens", value?.output],
    ["Cached input", value?.cachedInput], ["Cache-write input", value?.cacheWriteInput], ["Reasoning output", value?.reasoningOutput],
  ] as const).map(([label, count]) => <div key={label}><dt>{label}</dt><dd>{measure(count)}</dd>{count?.unavailableResponses ? <small>{count.unavailableResponses.toLocaleString()} responses unavailable</small> : null}</div>)}</dl>;
}

export function Usage({ active, open }: { active: boolean; open: (id: string) => void }) {
  const [draft, setDraft] = useState<Filters>(emptyFilters);
  const [selection, setSelection] = useState(() => request(emptyFilters));
  const [invalid, setInvalid] = useState("");
  const result = useQuery(UsageQuery.getUsageSummary, selection, { enabled: active });
  const closeDrawer = useCloseSidebarDrawer();
  const change = <K extends keyof Filters>(key: K, value: Filters[K]) => setDraft((current) => ({ ...current, [key]: value }));
  const apply = () => {
    try {
      const next = request(draft);
      const until = next.untilUnixMs || BigInt(Date.now());
      const from = next.fromUnixMs || until - 30n * 86400000n;
      if (from <= 0n || until <= from || until - from > 366n * 86400000n) throw new Error("range");
      setInvalid(""); setSelection(next); closeDrawer();
    } catch { setInvalid("Choose a valid time range of at most 366 days, with From before Until."); }
  };
  const data = result.data;
  return <>
    <SidebarSurface active={active} title="Usage">
      <p>Last 30 days by default</p>
      <form className="sidebar-form" onSubmit={(event) => { event.preventDefault(); apply(); }}>
        <label>From (local time)<input type="datetime-local" value={draft.from} onChange={(event) => change("from", event.target.value)} /></label>
        <label>Until (local time, exclusive)<input type="datetime-local" value={draft.until} onChange={(event) => change("until", event.target.value)} /></label>
        <ResourceChoice label="Session" kind={EntityKind.SESSION} value={draft.sessionId} change={(id) => change("sessionId", id)} active={active} />
        <ResourceChoice label="Project" kind={EntityKind.PROJECT} value={draft.projectId} change={(id) => change("projectId", id)} active={active} disabled={draft.generalChat} />
        <ResourceChoice label="Account" kind={EntityKind.ACCOUNT} value={draft.accountId} change={(id) => change("accountId", id)} active={active} />
        <ResourceChoice label="Provider" kind={EntityKind.PROVIDER} value={draft.providerId} change={(id) => change("providerId", id)} active={active} />
        <ResourceChoice label="Model" kind={EntityKind.MODEL} value={draft.modelId} change={(id) => change("modelId", id)} active={active} />
        <label className="checkbox"><input type="checkbox" checked={draft.generalChat} onChange={(event) => setDraft((current) => ({ ...current, generalChat: event.target.checked, projectId: event.target.checked ? "" : current.projectId }))} />General Chat only</label>
        {invalid ? <p role="alert">{invalid}</p> : null}
        <div className="actions"><button type="submit" className="primary">Apply filters</button><button type="button" onClick={() => { setDraft(emptyFilters); setSelection(request(emptyFilters)); setInvalid(""); closeDrawer(); }}>Reset</button></div>
      </form>
    </SidebarSurface>
    <section hidden={!active} className="page usage-page"><header><h2>Usage</h2><button disabled={result.isFetching} onClick={() => void result.refetch()}>Refresh usage</button></header>
    <p>DeliDev activity only. The default range is the last 30 days; archived sessions are included.</p>
    <Problem error={result.error} />{result.isPending && active ? <p role="status">Loading usage…</p> : null}
    {result.isFetching ? <p role="status">Loading usage…</p> : null}
    {data && result.error ? <p className="notice">The refresh failed. These are the last successfully retrieved values.</p> : null}
    {data ? <>
      <p>Recorded from <time dateTime={new Date(Number(data.fromUnixMs)).toISOString()}>{new Date(Number(data.fromUnixMs)).toLocaleString()}</time> until <time dateTime={new Date(Number(data.untilUnixMs)).toISOString()}>{new Date(Number(data.untilUnixMs)).toLocaleString()}</time>. Times reflect when the server first retained each response.</p>
      <div className="notice"><strong>Known subtotals · incomplete coverage</strong><p>{data.coverage === UsageCoverage.OBSERVED_ROOT_RESPONSES ? "Only observed root responses are included. Missing, older, resumed-conversation, child and unsupported harness telemetry is unavailable, not zero." : "This server's telemetry coverage is unknown."}</p><p>{data.acceptedExecutionsWithoutResponse.toLocaleString()} executions accepted in this range have no response usage recorded in the range.</p></div>
      <p>{data.totals?.responses.toLocaleString() ?? "0"} distinct responses recorded.</p><Measures value={data.totals} />
      <p>Cached input is part of input, and reasoning output is part of output. These breakdowns are not added again to total tokens.</p>
      <div className="usage-costs"><p><strong>Actual API cost:</strong> Unavailable — no verified attributable charge is supplied by the current telemetry.</p><EstimateCosts totals={data.estimates} pricing={data.pricing} /></div>
      <h3>By session, model and account</h3>
      {data.groups.length ? <div className="usage-table"><table><caption>Known response subtotals with original account and model identities</caption><thead><tr><th scope="col">Session / project</th><th scope="col">Account</th><th scope="col">Model / API</th><th scope="col">Tokens</th><th scope="col">Token-price estimate</th></tr></thead><tbody>{data.groups.map((group) => <tr key={`${group.sessionId}:${group.accountId}:${group.providerId}:${group.modelId}`}>
        <td><button onClick={() => open(group.sessionId)}>{group.sessionName || group.sessionId}</button><p>{group.projectId ? group.projectName || `Project ${group.projectId}` : "General Chat"}</p></td>
        <td><span>{group.accountName || "Retained account"}</span><small>{group.accountId}</small></td>
        <td><span>{group.modelName || "Retained model"}</span><small>{group.modelId}</small><p>{group.providerName || `API ${group.providerId}`}</p></td>
        <td><strong>{measure(group.totals?.total)}</strong><p>{group.totals?.responses.toLocaleString()} responses{group.totals?.total?.unavailableResponses ? ` · ${group.totals.total.unavailableResponses} unavailable` : ""}</p><details><summary>Token breakdown</summary><Measures value={group.totals} /></details></td><td><EstimateAmounts value={group.estimates} /></td>
      </tr>)}</tbody></table></div> : <p>No exact response usage is recorded for these filters. This does not mean zero usage or zero cost.</p>}
    </> : null}
  </section></>;
}
