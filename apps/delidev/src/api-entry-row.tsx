// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { AccountingUnitKind, UsageAccountingProfile, UsageCostState, UsageQuery, newRequestId, type GetUsageSummaryResponse, type Resource } from "@delinoio/delidev-api-client";
import { document, object, resourceName, text } from "./documents";
import { EstimateAmounts } from "./estimate-costs";
import { QuotaObservationState, quotaPresentation, type SubscriptionQuotaWindow } from "./subscription-settings";
import { Problem } from "./ui";
import type { UsageEntry } from "./usage-entry";

const unitNames: Partial<Record<AccountingUnitKind, string>> = {
  [AccountingUnitKind.CODEX_RESPONSE]: "Codex responses",
  [AccountingUnitKind.GROK_CLOSED_INPUT]: "Grok closed inputs",
  [AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT]: "Claude main-loop inputs",
  [AccountingUnitKind.OPENCODE_STEP]: "OpenCode steps",
};
function count(value: string | undefined, measured: number | undefined) {
  return measured && value !== undefined && /^(0|[1-9][0-9]*)$/.test(value) ? BigInt(value).toLocaleString() : "Unavailable";
}
function UsageMetrics({ data }: { data?: GetUsageSummaryResponse }) {
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
    <div className="api-usage-metric"><dt>Estimated cost</dt><dd>
      {response ? <div>{separate ? <small>Responses</small> : null}{empty ? <strong>$0</strong> : data?.estimatedCost === UsageCostState.KNOWN_SUBTOTAL && data.estimates?.currencies.length ? data.estimates.currencies.map(row => <strong key={row.currency}>{row.currency} {row.knownAmount || "Unavailable"}</strong>) : <strong>Unavailable</strong>}</div> : null}
      {native.map(row => <div key={row.totals!.kind}><small>{unitNames[row.totals!.kind]}</small>{row.totals!.currencies.length ? row.totals!.currencies.map(value => <strong key={value.currency}>{value.currency} {value.knownAmount || "Unavailable"}</strong>) : <strong>Unavailable</strong>}</div>)}
      {grok ? <div><small>Grok closed inputs</small><strong>Unavailable</strong></div> : null}
    </dd><small>{empty ? "No usage in this period" : data?.estimatedCost === UsageCostState.KNOWN_SUBTOTAL || native.some(row => row.totals!.currencies.some(value => value.knownAmount !== "")) ? "Known subtotal" : "No historical estimate"}</small></div>
    <div className="api-usage-metric"><dt>Observed tokens</dt><dd>
      {response ? <div>{separate ? <small>Responses</small> : null}<strong>{empty ? "0" : count(data?.totals?.total?.knownTotal, data?.totals?.total?.measuredResponses)}</strong><small>{empty ? "No usage in this period" : data?.totals ? data.totals.responses > 0 ? `${data.totals.responses.toLocaleString()} observed responses` : "No exact response records" : "Not reported"}</small></div> : null}
      {native.map(row => <div key={row.totals!.kind}><small>{unitNames[row.totals!.kind]}</small><strong>{count(row.totals!.total?.knownTotal, row.totals!.total?.measuredUnits)}</strong><small>{row.totals!.units.toLocaleString()} observed units</small></div>)}
      {grok ? <div><small>Grok closed inputs</small><strong>{count(grok.knownTotal, grok.measuredUnits)}</strong><small>{grok.units.toLocaleString()} observed units</small></div> : null}
    </dd></div>
  </>;
}
function Quota({ window, now, compact = false }: { window: SubscriptionQuotaWindow; now: number; compact?: boolean }) {
  const value = quotaPresentation(window, now);
  return <div className="api-quota-window"><small>{window.id || "Quota window"}</small><strong>{value.percent === undefined ? "Not reported" : `${value.percent}% remaining`}</strong>{value.percent !== undefined ? <progress value={value.percent} max={100} aria-label={`${window.id || "Quota window"} remaining`} /> : null}<small>{value.state === QuotaObservationState.Observed ? "Observed" : value.state === QuotaObservationState.Failed ? "Observation failed" : value.state === QuotaObservationState.Stale ? "Stale observation" : value.state === QuotaObservationState.Unsupported ? "Unsupported" : "Unknown"}</small>{!compact && window.observedAt ? <small>Observed <time dateTime={window.observedAt}>{window.observedAt}</time></small> : null}{!compact && window.resetAt ? <small>Resets <time dateTime={window.resetAt}>{window.resetAt}</time></small> : null}</div>;
}
export function ApiEntryRow({ row, provider, active, manage, edit, remove, openUsage }: {
  row: Resource; provider?: { displayName: string; enabled: boolean }; active: boolean;
  manage: () => void; edit: () => void; remove: () => void; openUsage?: (entry: UsageEntry) => void;
}) {
  const value = document(row), name = resourceName(row), supported = row.schemaVersion === 1;
  const result = useQuery(UsageQuery.getUsageSummary, { accountId: row.id, accountingProfile: UsageAccountingProfile.NATIVE_UNITS_V1 }, { enabled: active && supported, retry: false, refetchOnWindowFocus: false, refetchOnReconnect: false });
  const [menu, setMenu] = useState(false), [details, setDetails] = useState(false);
  const menuId = useId(), detailsId = useId(), nameId = useId();
  const menuButton = useRef<HTMLButtonElement>(null), detailsButton = useRef<HTMLButtonElement>(null);
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
  const closeMenu = () => { setMenu(false); menuButton.current?.focus(); };
  const cleanup = Boolean(text(object(value.removal).request_id));
  const connected = Boolean(text(object(value.connection).id));
  return <article className="api-entry-row api-usage-row" aria-labelledby={nameId}>
    <div className="api-usage-main">
      <div className="api-entry-identity"><span className="api-entry-mark" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M9 15l6-6m-8 9H5a4 4 0 0 1 0-8h4m6-4h4a4 4 0 0 1 0 8h-4" /></svg></span><div>
        <h2 id={nameId}>{name}</h2><p className="api-entry-provider-name">{provider?.displayName ?? "Provider unavailable · " + text(value.provider_id)}</p>
        <div className="api-entry-statuses"><span data-state={cleanup ? "warning" : connected ? "connected" : "neutral"}><span className="api-entry-sr-only">Connection: </span>{cleanup ? "Credential cleanup pending" : connected ? "Connected" : "Disconnected"}</span><span data-state={value.health === "ready" ? "connected" : "warning"}><span className="api-entry-sr-only">Health: </span>{text(value.health) || "Unknown"}</span></div>
        {!supported ? <small>{row.id}</small> : null}
      </div></div>
      <dl className="api-usage-metrics"><UsageMetrics data={result.data} /><div className="api-usage-metric"><dt>Quota</dt><dd>{value.confirmed_exhausted === true ? <strong className="api-entry-exhausted">Confirmed exhausted</strong> : windows.length ? windows.slice(0, 2).map((window, index) => <Quota key={index} window={window} now={now} compact />) : <strong>Not reported</strong>}</dd></div></dl>
      <div className="api-entry-controls"><button type="button" disabled={!supported} onClick={manage}>Manage connection</button><div className="api-entry-more">
        <button ref={menuButton} type="button" disabled={!supported} aria-label={`More actions for ${name}`} aria-expanded={menu} aria-controls={menuId} onClick={() => setMenu(!menu)} onKeyDown={event => { if (event.key === "Escape" && menu) { event.preventDefault(); event.stopPropagation(); closeMenu(); } }}><span aria-hidden="true">⋯</span></button>
        {menu ? <div id={menuId} className="api-entry-more-panel" role="group" aria-label={`Actions for ${name}`} onKeyDown={event => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); closeMenu(); } }}><button type="button" onClick={() => { closeMenu(); edit(); }}>Edit preferences</button><button className="api-entry-delete" type="button" onClick={() => { closeMenu(); remove(); }}>Delete entry</button></div> : null}
      </div></div>
    </div>
    <div className="api-entry-usage-status">
      {result.isFetching ? <p role="status">{result.data ? "Refreshing usage…" : "Loading usage…"}</p> : null}
      <Problem error={result.error} />{result.error ? <><p role="status">{result.data ? "Refresh failed. Showing stale usage from the last successful read." : "Usage is unavailable. Entry controls remain available."}</p><button type="button" disabled={!active || result.isFetching} onClick={() => void result.refetch()}>Retry usage for {name}</button></> : null}
    </div>
    <div className="api-entry-footer"><button ref={detailsButton} type="button" className="api-entry-text-action" aria-expanded={details} aria-controls={detailsId} onClick={() => setDetails(!details)} onKeyDown={event => { if (event.key === "Escape" && details) { event.preventDefault(); event.stopPropagation(); setDetails(false); } }}><span aria-hidden="true">{details ? "⌄" : "›"}</span> Details</button><button type="button" className="api-entry-text-action api-entry-usage-link" disabled={!supported || !openUsage} onClick={() => openUsage?.({ key: newRequestId(), accountId: row.id, fromUnixMs: result.data?.fromUnixMs ?? 0n, untilUnixMs: result.data?.untilUnixMs ?? 0n })}>View usage</button></div>
    <div id={detailsId} className="api-entry-details" hidden={!details} onKeyDown={event => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); setDetails(false); detailsButton.current?.focus(); } }}>
      <dl><div><dt>Entry:</dt><dd>{value.enabled === true ? "Enabled" : "Disabled"}</dd></div><div><dt>Provider status:</dt><dd>{provider ? provider.enabled ? "Enabled" : "Off" : "Unavailable"}</dd></div><div><dt>Account ID:</dt><dd>{row.id}</dd></div></dl>
      <p>Connection and health are independent. Actual charges are unavailable.</p>
      {result.data ? <>{result.data.fromUnixMs > 0n && result.data.untilUnixMs > result.data.fromUnixMs ? <p>Range: <time>{new Date(Number(result.data.fromUnixMs)).toISOString()}</time> – <time>{new Date(Number(result.data.untilUnixMs)).toISOString()}</time> (exclusive).</p> : null}<p>Incomplete coverage. {result.data.acceptedExecutionsWithoutResponse} accepted executions and {result.data.acceptedCompactionsWithoutResponse} native context actions have no exact response usage in this range.</p><p>{result.data.totals?.total?.measuredResponses ?? 0} measured responses · {result.data.totals?.total?.unavailableResponses ?? 0} unavailable response totals.</p><EstimateAmounts value={result.data.estimates} />{result.data.nativeAccounting.map(data => unitNames[data.totals?.kind ?? AccountingUnitKind.UNSPECIFIED] ? <p key={data.totals!.kind}>{unitNames[data.totals!.kind]}: {data.totals?.total?.measuredUnits ?? 0} measured · {data.totals?.total?.unavailableUnits ?? 0} unavailable total units · {data.totals?.unpricedUnits ?? 0} unpriced units.</p> : null)}{result.data.accountingProfile !== UsageAccountingProfile.NATIVE_UNITS_V1 ? <p>Independent native accounting is unavailable from this server version. Update the selected server.</p> : null}</> : <p>No usage evidence has been retrieved.</p>}
      {windows.length ? windows.map((window, index) => <Quota key={index} window={window} now={now} />) : <p>No quota observation</p>}
    </div>
  </article>;
}
