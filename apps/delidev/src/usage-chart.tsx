import { SubscriptionServiceIdentity, subscriptionServiceLabel } from "@delinoio/delidev-api-client";
import { useState, type KeyboardEvent } from "react";
import type { UsageAnalytics, UsageAnalyticsDay, UsageAnalyticsModel, UsageMeasure, UsageOtherModels, UsageTotals } from "@delinoio/delidev-api-client";

const dailyPlot = { left: 70, right: 872, top: 18, bottom: 174 } as const;
const modelPlot = { left: 248, right: 872, top: 24, row: 40 } as const;

function exactTotal(value?: UsageMeasure): bigint | undefined {
  if (!value || value.measuredResponses === 0 || !/^\d+$/.test(value.knownTotal)) return undefined;
  return BigInt(value.knownTotal);
}

function exactText(value?: UsageMeasure): string {
  const total = exactTotal(value);
  return total === undefined ? "Unavailable" : total.toLocaleString();
}

function normalized(value: string, maximum: bigint, size: number): number {
  if (!/^\d+$/.test(value) || maximum <= 0n) return 0;
  const scaled = (BigInt(value) * BigInt(size)) / maximum;
  return Number(scaled > BigInt(size) ? BigInt(size) : scaled);
}

function axisTotal(value: bigint): string {
  const units = [{ divisor: 10n ** 18n, suffix: "E" }, { divisor: 10n ** 15n, suffix: "P" }, { divisor: 10n ** 12n, suffix: "T" }, { divisor: 10n ** 9n, suffix: "B" }, { divisor: 10n ** 6n, suffix: "M" }, { divisor: 10n ** 3n, suffix: "K" }];
  const unit = units.find((candidate) => value >= candidate.divisor);
  if (!unit) return value.toLocaleString();
  const whole = value / unit.divisor;
  const decimal = (value % unit.divisor) * 10n / unit.divisor;
  return `${whole.toLocaleString()}${decimal ? `.${decimal}` : ""}${unit.suffix}`;
}

function fullTime(value: bigint, timeZone: string): string {
  return new Intl.DateTimeFormat(undefined, { timeZone, year: "numeric", month: "long", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit", timeZoneName: "shortOffset" }).format(new Date(Number(value)));
}

function shortDay(value: bigint, timeZone: string): string {
  return new Intl.DateTimeFormat(undefined, { timeZone, month: "short", day: "numeric" }).format(new Date(Number(value)));
}

function dateInterval(value: UsageAnalyticsDay, timeZone: string): string {
  return `${fullTime(value.fromUnixMs, timeZone)} – ${fullTime(value.untilUnixMs, timeZone)} (exclusive)`;
}

function evidence(value?: UsageTotals): string {
  const total = value?.total;
  return `${value?.responses ?? 0} responses · ${total?.measuredResponses ?? 0} measured · ${total?.unavailableResponses ?? 0} unavailable`;
}

const measureColumns = [
  ["Known total", "total"], ["Input", "input"], ["Cached input", "cachedInput"],
  ["Cache-write input", "cacheWriteInput"], ["Output", "output"], ["Reasoning output", "reasoningOutput"],
] as const;

function MeasureCells({ totals }: { totals?: UsageTotals }) {
  return <>{measureColumns.map(([label, key]) => {
    const value = totals?.[key];
    return <td key={key}><strong>{exactText(value)}</strong><small>{value?.measuredResponses ?? 0} measured · {value?.unavailableResponses ?? 0} unavailable</small><span className="usage-sr-only"> responses for {label}</span></td>;
  })}</>;
}

function keyIndex(event: KeyboardEvent<SVGSVGElement>, index: number, count: number, setIndex: (next: number | null) => void, direction: "horizontal" | "vertical") {
  if (!count) return;
  let next = index;
  if (event.key === (direction === "horizontal" ? "ArrowRight" : "ArrowDown")) next = Math.min(count - 1, index + 1);
  else if (event.key === (direction === "horizontal" ? "ArrowLeft" : "ArrowUp")) next = Math.max(0, index - 1);
  else if (event.key === "Home") next = 0;
  else if (event.key === "End") next = count - 1;
  else if (event.key === "Escape") { event.preventDefault(); setIndex(null); return; }
  else return;
  event.preventDefault();
  setIndex(next);
}

function ViewData({ id, open, change }: { id: string; open: boolean; change: (value: boolean) => void }) {
  return <button type="button" className="usage-data-toggle" aria-expanded={open} aria-controls={id} onClick={() => change(!open)}>{open ? "Hide data" : "View data"}</button>;
}

function DailyViewData({ days, timeZone, id, open }: { days: UsageAnalyticsDay[]; timeZone: string; id: string; open: boolean }) {
  return <div id={id} className="usage-chart-table" role="region" aria-label="Daily usage data table; scroll horizontally to inspect all measures" tabIndex={0} hidden={!open}><table><caption>All calendar-day intervals and exact token evidence in {timeZone}</caption><thead><tr><th scope="col">From</th><th scope="col">Until</th><th scope="col">Responses</th>{measureColumns.map(([label, key]) => <th scope="col" key={key}>{label}</th>)}</tr></thead><tbody>{days.map((day) => <tr key={`${day.fromUnixMs}:${day.untilUnixMs}`}>
    <td><time dateTime={new Date(Number(day.fromUnixMs)).toISOString()}>{fullTime(day.fromUnixMs, timeZone)}</time></td>
    <td><time dateTime={new Date(Number(day.untilUnixMs)).toISOString()}>{fullTime(day.untilUnixMs, timeZone)} <span>(exclusive)</span></time></td>
    <td>{day.totals?.responses ?? 0}</td><MeasureCells totals={day.totals} />
  </tr>)}</tbody></table></div>;
}

export function DailyUsageChart({ days, timeZone }: { days: UsageAnalyticsDay[]; timeZone: string }) {
  const [selected, setSelected] = useState<number | null>(null);
  const [open, setOpen] = useState(false);
  const values = days.map((day) => exactTotal(day.totals?.total));
  const maximum = values.reduce<bigint>((current, value) => value !== undefined && value > current ? value : current, 0n);
  const xAt = (index: number) => days.length <= 1 ? Math.floor((dailyPlot.left + dailyPlot.right) / 2) : dailyPlot.left + Math.floor(index * (dailyPlot.right - dailyPlot.left) / (days.length - 1));
  const yAt = (index: number) => dailyPlot.bottom - normalized(values[index]?.toString() ?? "", maximum, dailyPlot.bottom - dailyPlot.top);
  const segments: string[] = [];
  let segment: string[] = [];
  for (let index = 0; index < days.length; index++) {
    if (values[index] === undefined) {
      if (segment.length) segments.push(segment.join(" "));
      segment = [];
      continue;
    }
    segment.push(`${segment.length ? "L" : "M"} ${xAt(index)} ${yAt(index)}`);
  }
  if (segment.length) segments.push(segment.join(" "));
  const active = selected === null ? undefined : days[selected];
  const selectedValue = selected === null ? undefined : values[selected];
  const dataId = "usage-daily-data";

  return <section className="usage-chart-panel" aria-labelledby="usage-daily-title">
    <header><div><h3 id="usage-daily-title">Daily token usage</h3><p>Known total tokens · first server retention time · {timeZone}</p></div><ViewData id={dataId} open={open} change={setOpen} /></header>
    {days.length ? <svg className="usage-daily-svg" viewBox="0 0 900 224" role="group" aria-describedby="usage-daily-help" aria-label={`Daily usage chart for ${days.length} local calendar days. Focus and use Left or Right, Home or End, and Escape for exact details.`} tabIndex={0} onFocus={() => setSelected((current) => current ?? 0)} onKeyDown={(event) => keyIndex(event, selected ?? 0, days.length, setSelected, "horizontal")}>
      <line className="usage-chart-grid" x1={dailyPlot.left} x2={dailyPlot.right} y1={dailyPlot.top} y2={dailyPlot.top} /><line className="usage-chart-grid" x1={dailyPlot.left} x2={dailyPlot.right} y1={dailyPlot.bottom} y2={dailyPlot.bottom} />
      <text className="usage-axis-label" x={dailyPlot.left - 8} y={dailyPlot.top + 4} textAnchor="end">{axisTotal(maximum)}</text><text className="usage-axis-label" x={dailyPlot.left - 8} y={dailyPlot.bottom + 4} textAnchor="end">0</text>
      {segments.map((path, index) => <path key={index} className="usage-line" d={path} />)}
      {days.map((day, index) => {
        const value = values[index];
        const noMeasuredTotal = day.totals?.responses ? value === undefined : false;
        if (value === undefined && !noMeasuredTotal) return null;
        return <circle key={`${day.fromUnixMs}:${day.untilUnixMs}`} className={value === undefined ? "usage-point usage-point-unavailable" : "usage-point"} cx={xAt(index)} cy={value === undefined ? dailyPlot.bottom : yAt(index)} r={selected === index ? 6 : 4} onMouseEnter={() => setSelected(index)} onMouseLeave={() => setSelected(null)}><title>{`${shortDay(day.fromUnixMs, timeZone)}: ${value === undefined ? "Unavailable" : value.toLocaleString()} tokens; ${evidence(day.totals)}`}</title></circle>;
      })}
      <text className="usage-axis-label" x={dailyPlot.left} y="208" textAnchor="start">{shortDay(days[0].fromUnixMs, timeZone)}</text><text className="usage-axis-label" x={dailyPlot.right} y="208" textAnchor="end">{shortDay(days[days.length - 1].fromUnixMs, timeZone)}</text>
    </svg> : <p className="usage-chart-empty">No calendar days are available for this applied range.</p>}
    <p className="usage-chart-help" id="usage-daily-help">Focus the chart to inspect each day. Unmeasured observations break the line; measured zero is shown at the baseline; days without responses have no marker.</p>
    <p className="usage-chart-detail" role="status" aria-live="polite">{active ? <><strong>{active.totals?.responses ? selectedValue === undefined ? "Unavailable total" : `${selectedValue.toLocaleString()} known tokens` : "No responses recorded"}</strong> · {dateInterval(active, timeZone)} · {evidence(active.totals)}</> : "Move focus to a chart point to inspect its exact interval and evidence."}</p>
    <DailyViewData days={days} timeZone={timeZone} id={dataId} open={open} />
  </section>;
}

interface ModelBar { subscriptionService?: SubscriptionServiceIdentity; key: string; providerId: string; modelId: string; providerName: string; modelName: string; totals?: UsageTotals; modelCount?: number; }

function modelRows(analytics: UsageAnalytics): ModelBar[] {
  const ranked: ModelBar[] = analytics.models.filter((model) => exactTotal(model.totals?.total) !== undefined).slice(0, 5).map((model) => ({ key: `${model.providerId}:${model.subscriptionService}:${model.modelId}`, subscriptionService: model.subscriptionService, providerId: model.providerId, modelId: model.modelId, providerName: model.providerName, modelName: model.modelName, totals: model.totals }));
  if (analytics.otherModels) ranked.push({ key: "other-models", providerId: "", modelId: "", providerName: "", modelName: "Other models", totals: analytics.otherModels.totals, modelCount: analytics.otherModels.modelCount });
  return ranked;
}

function ModelViewData({ models, otherModels, id, open }: { models: UsageAnalyticsModel[]; otherModels?: UsageOtherModels; id: string; open: boolean }) {
  return <div id={id} className="usage-chart-table" role="region" aria-label="Model usage data table; scroll horizontally to inspect original identities and all measures" tabIndex={0} hidden={!open}><table><caption>Every original provider and model group, including groups without known totals</caption><thead><tr><th scope="col">Provider ID</th><th scope="col">Provider</th><th scope="col">Model ID</th><th scope="col">Model</th><th scope="col">Responses</th>{measureColumns.map(([label, key]) => <th scope="col" key={key}>{label}</th>)}</tr></thead><tbody>
    {models.map((model) => <tr key={`${model.providerId}:${model.subscriptionService}:${model.modelId}`}><td>{model.subscriptionService ? subscriptionServiceLabel(model.subscriptionService) : model.providerId}</td><td>{model.subscriptionService ? "Subscription service" : model.providerName || "Retained provider"}</td><td>{model.modelId}</td><td>{model.modelName || "Retained model"}</td><td>{model.totals?.responses ?? 0}</td><MeasureCells totals={model.totals} /></tr>)}
    {otherModels ? <tr><td colSpan={4}>Other models ({otherModels.modelCount} measured groups)</td><td>{otherModels.totals?.responses ?? 0}</td><MeasureCells totals={otherModels.totals} /></tr> : null}
  </tbody></table><p>Unmeasured groups are excluded from ranking and remain listed above.</p></div>;
}

export function ModelUsageChart({ analytics }: { analytics: UsageAnalytics }) {
  const [selected, setSelected] = useState<number | null>(null);
  const [open, setOpen] = useState(false);
  const rows = modelRows(analytics);
  const maximum = rows.reduce<bigint>((current, row) => { const value = exactTotal(row.totals?.total); return value !== undefined && value > current ? value : current; }, 0n);
  const selectedRow = selected === null ? undefined : rows[selected];
  const dataId = "usage-model-data";
  return <section className="usage-chart-panel" aria-labelledby="usage-model-title">
    <header><div><h3 id="usage-model-title">By model / API</h3><p>Original provider and model identities · ranked by known total</p></div><ViewData id={dataId} open={open} change={setOpen} /></header>
    {rows.length ? <svg className="usage-model-svg" viewBox="0 0 900 278" role="group" aria-describedby="usage-model-help" aria-label={`Ranked chart of ${rows.length} measured provider and model groups. Focus and use Up or Down, Home or End, and Escape for exact details.`} tabIndex={0} onFocus={() => setSelected((current) => current ?? 0)} onKeyDown={(event) => keyIndex(event, selected ?? 0, rows.length, setSelected, "vertical")}>
      {rows.map((row, index) => {
        const total = exactTotal(row.totals?.total) ?? 0n;
        const y = modelPlot.top + index * modelPlot.row;
        const width = normalized(total.toString(), maximum, modelPlot.right - modelPlot.left);
        return <g key={row.key} className={selected === index ? "usage-model-row usage-model-row-selected" : "usage-model-row"} onMouseEnter={() => setSelected(index)} onMouseLeave={() => setSelected(null)}>
          <text className="usage-model-rank" x="208" y={y + 16} textAnchor="end">{row.modelCount ? "Other" : `#${index + 1}`}</text>
          <rect className="usage-model-track" x={modelPlot.left} y={y} width={modelPlot.right - modelPlot.left} height="22" rx="4" />
          <rect className="usage-model-bar" x={modelPlot.left} y={y} width={width} height="22" rx="4" />
          <line className="usage-chart-grid" x1={modelPlot.left} x2={modelPlot.left} y1={y - 3} y2={y + 25} />
        </g>;
      })}
      <text className="usage-axis-label" x={modelPlot.left} y="270" textAnchor="start">0</text><text className="usage-axis-label" x={modelPlot.right} y="270" textAnchor="end">{axisTotal(maximum)}</text>
    </svg> : <p className="usage-chart-empty">No model group has a known total in this range.</p>}
    {rows.length ? <ol className="usage-model-labels">{rows.map((row, index) => <li key={row.key}><span className="usage-model-label-rank">{row.modelCount ? "Other models" : `#${index + 1}`}</span><strong>{row.modelName || (row.modelCount ? "Other models" : `Model ${index + 1}`)}</strong><span>{row.modelCount ? `${row.modelCount} measured model groups · no monetary rollup` : ` ${row.subscriptionService ? `Subscription service ${subscriptionServiceLabel(row.subscriptionService)}` : `API ${row.providerName || row.providerId} · provider ${row.providerId}`} · model ${row.modelId}`}</span><span className="usage-model-value">{exactText(row.totals?.total)} tokens · {evidence(row.totals)}</span></li>)}</ol> : null}
    <p className="usage-chart-help" id="usage-model-help">Focus the chart to inspect ranked groups. Up and Down move between rows. Full identities and exact values are also available in View data.</p>
    <p className="usage-chart-detail" role="status" aria-live="polite">{selectedRow ? <><strong>{selectedRow.modelName || selectedRow.modelId || "Other models"}</strong> · {selectedRow.modelCount ? `${selectedRow.modelCount} measured groups` : `${selectedRow.subscriptionService ? `Subscription service ${subscriptionServiceLabel(selectedRow.subscriptionService)}` : `Provider ${selectedRow.providerId}`} · model ${selectedRow.modelId}`} · {exactText(selectedRow.totals?.total)} tokens · {evidence(selectedRow.totals)}</> : "Move focus to a chart row to inspect its original identity and exact totals."}</p>
    <ModelViewData models={analytics.models} otherModels={analytics.otherModels} id={dataId} open={open} />
    {analytics.models.some((model) => exactTotal(model.totals?.total) === undefined) ? <p className="usage-unranked">Unmeasured models are not ranked. Their original identities remain in View data.</p> : null}
  </section>;
}

export function UsageCharts({ analytics, timeZone }: { analytics: UsageAnalytics; timeZone: string }) {
  return <div className="usage-charts"><DailyUsageChart days={analytics.days} timeZone={timeZone} /><ModelUsageChart analytics={analytics} /></div>;
}
