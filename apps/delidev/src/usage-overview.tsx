// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { AccountingUnitKind, UsageAccountingProfile, type GetUsageSummaryResponse } from "@delinoio/delidev-api-client";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { copy, displayLocale, formatDecimal, useLocale } from "./localization";
import { Timestamp, TimestampMode } from "./timestamp-display";

const tools = [AccountingUnitKind.CODEX_RESPONSE, AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT, AccountingUnitKind.OPENCODE_STEP, AccountingUnitKind.GROK_CLOSED_INPUT] as const;
const names = { [AccountingUnitKind.CODEX_RESPONSE]: "Codex", [AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT]: "Claude Code", [AccountingUnitKind.OPENCODE_STEP]: "OpenCode", [AccountingUnitKind.GROK_CLOSED_INPUT]: "Grok Build" };
function integer(value: string | undefined, measured: number | undefined) {
  return measured && value && /^(0|[1-9][0-9]*)$/.test(value) ? BigInt(value).toLocaleString(displayLocale()) : "—";
}

/** Each row reads one server-owned source; overlapping native reports are never added. */
export function UsageOverview({ data, timeZone }: { data: GetUsageSummaryResponse; timeZone: string }) {
  useLocale();
  const [source, setSource] = useState<AccountingUnitKind>(AccountingUnitKind.CODEX_RESPONSE);
  const native = data.nativeAccounting.find(summary => summary.totals?.kind === source);
  const supported = data.accountingProfile === UsageAccountingProfile.NATIVE_UNITS_V1;
  const nativeSupported = data.nativeAccounting.length === 2 && new Set(data.nativeAccounting.map(row => row.totals?.kind)).size === 2 && data.nativeAccounting.every(row => row.totals && [AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT, AccountingUnitKind.OPENCODE_STEP].includes(row.totals.kind));
  return <>
    <section className="usage-tool-overview" aria-labelledby="usage-tools-title"><h2 id="usage-tools-title">{copy("usage.byTool")}</h2>
      <div className="usage-overview-table"><table><thead><tr><th scope="col">{copy("usage.tool")}</th><th scope="col">{copy("usage.recordedTokens")}</th><th scope="col">{copy("usage.estimatedCost")}</th><th scope="col">{copy("usage.status")}</th></tr></thead><tbody>{tools.map(kind => {
        const codex = kind === AccountingUnitKind.CODEX_RESPONSE;
        const grok = kind === AccountingUnitKind.GROK_CLOSED_INPUT;
        const totals = data.nativeAccounting.find(summary => summary.totals?.kind === kind)?.totals;
        const unit = data.totals?.accounting.find(row => row.kind === kind);
        const count = codex ? data.totals?.responses : grok ? unit?.units : totals?.units;
        const valid = supported && (codex ? Boolean(data.totals) : grok ? Boolean(data.totals) : Boolean(totals) && nativeSupported);
        const tokens = !valid || !count ? "—" : codex ? integer(data.totals?.total?.knownTotal, data.totals?.total?.measuredResponses) : grok ? integer(unit?.knownTotal, unit?.measuredUnits) : integer(totals?.total?.knownTotal, totals?.total?.measuredUnits);
        const currencies = codex ? data.estimates?.currencies : grok ? [] : totals?.currencies;
        return <tr key={kind}><th scope="row">{names[kind]}</th><td data-label={copy("usage.recordedTokens")}>{tokens}<small>{copy(grok ? "usage.inputTokens" : "usage.reportedTotal")}</small></td><td data-label={copy("usage.estimatedCost")}>{valid && count && currencies?.some(row => row.knownAmount !== "") ? currencies.filter(row => row.knownAmount !== "").map(row => <span key={row.currency}>{row.currency} {formatDecimal(row.knownAmount)}</span>) : "—"}</td><td data-label={copy("usage.status")}>{!valid ? copy("usage.unsupportedEvidence") : !count ? copy("usage.noRecordedUsage") : copy("usage.recordedUnits", { count: count.toLocaleString(displayLocale()) })}{count && tokens === "—" ? <small>{copy("usage.missingReportedTotal")}</small> : null}{grok && count ? <small>{copy("usage.unpricedInput")}</small> : null}</td></tr>;
      })}</tbody></table></div>
      <p>{copy("usage.toolsSeparate")}</p><p>{copy("usage.estimateNotBill")}</p>
      <Disclosure><DisclosureSummary>{copy("usage.howCounted")}</DisclosureSummary><p>{copy("usage.sourceDefinitionsHelp")}</p></Disclosure>
    </section>
    <section className="usage-source-trends" aria-label={copy("usage.dailyAndModelTrends")}>
      <label>{copy("usage.trendSource")}<select value={source} onChange={event => setSource(Number(event.target.value) as AccountingUnitKind)}>{tools.map(kind => <option key={kind} value={kind}>{names[kind]}</option>)}</select></label>
      <div className="usage-overview-table"><table><caption>{names[source as keyof typeof names]} · {copy("usage.dailySourceRecords")}</caption><thead><tr><th scope="col">{copy("usage.period")}</th><th scope="col">{copy("usage.recordedTokens")}</th></tr></thead><tbody>{(source === AccountingUnitKind.CODEX_RESPONSE ? data.analytics?.days.map(day => ({ fromUnixMs: day.fromUnixMs, value: integer(day.totals?.total?.knownTotal, day.totals?.total?.measuredResponses) })) : source === AccountingUnitKind.GROK_CLOSED_INPUT ? data.analytics?.days.map(day => {
        const value = day.totals?.accounting.find(unit => unit.kind === source);
        return { fromUnixMs: day.fromUnixMs, value: integer(value?.knownTotal, value?.measuredUnits) };
      }) : native?.days.map(day => ({ fromUnixMs: day.fromUnixMs, value: integer(day.totals?.total?.knownTotal, day.totals?.total?.measuredUnits) })))?.map(day => <tr key={day.fromUnixMs.toString()}><td><Timestamp value={new Date(Number(day.fromUnixMs)).toISOString()} mode={TimestampMode.Absolute} timeZone={timeZone} /></td><td>{day.value}<small>{copy(source === AccountingUnitKind.GROK_CLOSED_INPUT ? "usage.inputTokens" : "usage.reportedTotal")}</small></td></tr>)}</tbody></table><p>{copy("usage.sourceDefinitionsHelp")}</p></div>
    </section>
  </>;
}
