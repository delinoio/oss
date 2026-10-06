import { LocalizedText, copy, displayLocale, useLocale } from "./localization";
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
  return total === undefined ? copy("usage-chart.extra.ca1844969742") : total.toLocaleString(displayLocale());
}

function normalized(value: string, maximum: bigint, size: number): number {
  if (!/^\d+$/.test(value) || maximum <= 0n) return 0;
  const scaled = (BigInt(value) * BigInt(size)) / maximum;
  return Number(scaled > BigInt(size) ? BigInt(size) : scaled);
}

function axisTotal(value: bigint): string {
  const units = [{ divisor: 10n ** 18n, suffix: "E" }, { divisor: 10n ** 15n, suffix: "P" }, { divisor: 10n ** 12n, suffix: "T" }, { divisor: 10n ** 9n, suffix: "B" }, { divisor: 10n ** 6n, suffix: "M" }, { divisor: 10n ** 3n, suffix: "K" }];
  const unit = units.find((candidate) => value >= candidate.divisor);
  if (!unit) return value.toLocaleString(displayLocale());
  const whole = value / unit.divisor;
  const decimal = (value % unit.divisor) * 10n / unit.divisor;
  return `${whole.toLocaleString(displayLocale())}${decimal ? `.${decimal}` : ""}${unit.suffix}`;
}

function fullTime(value: bigint, timeZone: string): string {
  return new Intl.DateTimeFormat(displayLocale(), { timeZone, year: "numeric", month: "long", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit", timeZoneName: "shortOffset" }).format(new Date(Number(value)));
}

function shortDay(value: bigint, timeZone: string): string {
  return new Intl.DateTimeFormat(displayLocale(), { timeZone, month: "short", day: "numeric" }).format(new Date(Number(value)));
}

function dateInterval(value: UsageAnalyticsDay, timeZone: string): string {
  return copy("usage.range", { from: fullTime(value.fromUnixMs, timeZone), until: fullTime(value.untilUnixMs, timeZone) });
}

function evidence(value?: UsageTotals): string {
  const total = value?.total;
  return copy("usage-chart.sentence.196fc0970efc", { v0: value?.responses ?? 0, v1: total?.measuredResponses ?? 0, v2: total?.unavailableResponses ?? 0 });
}

const measureColumns = () => [
  [copy("usage-chart.extra.33553459e8b2"), "total"], [copy("usage-chart.extra.36ecb4f86691"), "input"], [copy("usage-chart.extra.876a4379087b"), "cachedInput"],
  [copy("usage-chart.extra.f8bc2d034686"), "cacheWriteInput"], [copy("usage-chart.extra.b2439bcb8dee"), "output"], [copy("usage-chart.extra.f85860ca7347"), "reasoningOutput"],
] as const;

function MeasureCells({ totals }: { totals?: UsageTotals }) {
  useLocale();
  return <>{measureColumns().map(([label, key]) => {
    const value = totals?.[key];
    return <td key={key}><strong>{exactText(value)}</strong><small><LocalizedText id="usage-chart.measuredUnavailable_6fb061" components={{ s0: <>{value?.measuredResponses ?? 0}</>, s1: <>{value?.unavailableResponses ?? 0}</> }} /></small><span className="usage-sr-only"><LocalizedText id="usage-chart.responsesFor_2359a5" components={{ s0: <>{label}</> }} /></span></td>;
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
  useLocale();
  return <button type="button" className="usage-data-toggle" aria-expanded={open} aria-controls={id} onClick={() => change(!open)}>{open ? copy("usage-chart.hideData_3bfb0a") : copy("usage-chart.viewData_249495")}</button>;
}

function DailyViewData({ days, timeZone, id, open }: { days: UsageAnalyticsDay[]; timeZone: string; id: string; open: boolean }) {
  useLocale();
  return <div id={id} className="usage-chart-table" role="region" aria-label={copy("usage-chart.dailyUsageDataTableScrollHorizontally_3f2b6c")} tabIndex={0} hidden={!open}><table><caption><LocalizedText id="usage-chart.allCalendarDayIntervalsAndExact_a9c769" components={{ s0: <>{timeZone}</> }} /></caption><thead><tr><th scope="col">{copy("usage-chart.from_218197")}</th><th scope="col">{copy("usage-chart.until_7caf85")}</th><th scope="col">{copy("usage-chart.responses_9b4c6d")}</th>{measureColumns().map(([label, key]) => <th scope="col" key={key}>{label}</th>)}</tr></thead><tbody>{days.map((day) => <tr key={`${day.fromUnixMs}:${day.untilUnixMs}`}>
    <td><time dateTime={new Date(Number(day.fromUnixMs)).toISOString()}>{fullTime(day.fromUnixMs, timeZone)}</time></td>
    <td><time dateTime={new Date(Number(day.untilUnixMs)).toISOString()}>{fullTime(day.untilUnixMs, timeZone)} <span>{copy("usage-chart.exclusive_b71bef")}</span></time></td>
    <td>{day.totals?.responses ?? 0}</td><MeasureCells totals={day.totals} />
  </tr>)}</tbody></table></div>;
}

export function DailyUsageChart({ days, timeZone }: { days: UsageAnalyticsDay[]; timeZone: string }) {
  useLocale();
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
    <header><div><h3 id="usage-daily-title">{copy("usage-chart.dailyTokenUsage_664583")}</h3><p><LocalizedText id="usage-chart.knownTotalTokensFirstServerRetention_d06463" components={{ s0: <>{timeZone}</> }} /></p></div><ViewData id={dataId} open={open} change={setOpen} /></header>
    {days.length ? <svg className="usage-daily-svg" viewBox="0 0 900 224" role="group" aria-describedby="usage-daily-help" aria-label={copy("usage-chart.dailyUsageChartForLocalCalendar_1692ba", { v0: days.length })} tabIndex={0} onFocus={() => setSelected((current) => current ?? 0)} onKeyDown={(event) => keyIndex(event, selected ?? 0, days.length, setSelected, "horizontal")}>
      <line className="usage-chart-grid" x1={dailyPlot.left} x2={dailyPlot.right} y1={dailyPlot.top} y2={dailyPlot.top} /><line className="usage-chart-grid" x1={dailyPlot.left} x2={dailyPlot.right} y1={dailyPlot.bottom} y2={dailyPlot.bottom} />
      <text className="usage-axis-label" x={dailyPlot.left - 8} y={dailyPlot.top + 4} textAnchor="end">{axisTotal(maximum)}</text><text className="usage-axis-label" x={dailyPlot.left - 8} y={dailyPlot.bottom + 4} textAnchor="end">0</text>
      {segments.map((path, index) => <path key={index} className="usage-line" d={path} />)}
      {days.map((day, index) => {
        const value = values[index];
        const noMeasuredTotal = day.totals?.responses ? value === undefined : false;
        if (value === undefined && !noMeasuredTotal) return null;
        return <circle key={`${day.fromUnixMs}:${day.untilUnixMs}`} className={value === undefined ? "usage-point usage-point-unavailable" : "usage-point"} cx={xAt(index)} cy={value === undefined ? dailyPlot.bottom : yAt(index)} r={selected === index ? 6 : 4} onMouseEnter={() => setSelected(index)} onMouseLeave={() => setSelected(null)}><title>{copy("usage-chart.tokens_e0e267", { v0: shortDay(day.fromUnixMs, timeZone), v1: value === undefined ? copy("usage-chart.unavailable_ca1844") : value.toLocaleString(displayLocale()), v2: evidence(day.totals) })}</title></circle>;
      })}
      <text className="usage-axis-label" x={dailyPlot.left} y="208" textAnchor="start">{shortDay(days[0].fromUnixMs, timeZone)}</text><text className="usage-axis-label" x={dailyPlot.right} y="208" textAnchor="end">{shortDay(days[days.length - 1].fromUnixMs, timeZone)}</text>
    </svg> : <p className="usage-chart-empty">{copy("usage-chart.noCalendarDaysAreAvailableFor_7aff04")}</p>}
    <p className="usage-chart-help" id="usage-daily-help">{copy("usage-chart.focusTheChartToInspectEach_89b2f4")}</p>
    <p className="usage-chart-detail" role="status" aria-live="polite">{active ? <><strong>{active.totals?.responses ? selectedValue === undefined ? copy("usage-chart.unavailableTotal_35d55a") : copy("usage-chart.knownTokens_c69c69", { v0: selectedValue.toLocaleString(displayLocale()) }) : copy("usage-chart.noResponsesRecorded_5d628d")}</strong> · {dateInterval(active, timeZone)} · {evidence(active.totals)}</> : copy("usage-chart.moveFocusToAChartPoint_3437a1")}</p>
    <DailyViewData days={days} timeZone={timeZone} id={dataId} open={open} />
  </section>;
}

interface ModelBar { subscriptionService?: SubscriptionServiceIdentity; key: string; providerId: string; modelId: string; providerName: string; modelName: string; totals?: UsageTotals; modelCount?: number; }

function modelRows(analytics: UsageAnalytics): ModelBar[] {
  const ranked: ModelBar[] = analytics.models.filter((model) => exactTotal(model.totals?.total) !== undefined).slice(0, 5).map((model) => ({ key: `${model.providerId}:${model.subscriptionService}:${model.modelId}`, subscriptionService: model.subscriptionService, providerId: model.providerId, modelId: model.modelId, providerName: model.providerName, modelName: model.modelName, totals: model.totals }));
  if (analytics.otherModels) ranked.push({ key: "other-models", providerId: "", modelId: "", providerName: "", modelName: copy("usage-chart.extra.1b8bc2ba488c"), totals: analytics.otherModels.totals, modelCount: analytics.otherModels.modelCount });
  return ranked;
}

function ModelViewData({ models, otherModels, id, open }: { models: UsageAnalyticsModel[]; otherModels?: UsageOtherModels; id: string; open: boolean }) {
  useLocale();
  return <div id={id} className="usage-chart-table" role="region" aria-label={copy("usage-chart.modelUsageDataTableScrollHorizontally_2489e8")} tabIndex={0} hidden={!open}><table><caption>{copy("usage-chart.everyOriginalProviderAndModelGroup_82fc11")}</caption><thead><tr><th scope="col">{copy("usage-chart.providerId_751108")}</th><th scope="col">{copy("usage-chart.provider_472590")}</th><th scope="col">{copy("usage-chart.modelId_089ef2")}</th><th scope="col">{copy("usage-chart.model_5e2c61")}</th><th scope="col">{copy("usage-chart.responses_9b4c6d")}</th>{measureColumns().map(([label, key]) => <th scope="col" key={key}>{label}</th>)}</tr></thead><tbody>
    {models.map((model) => <tr key={`${model.providerId}:${model.subscriptionService}:${model.modelId}`}><td>{model.subscriptionService ? subscriptionServiceLabel(model.subscriptionService) : model.providerId}</td><td>{model.subscriptionService ? copy("usage-chart.subscriptionService_0e16df") : model.providerName || copy("usage-chart.extra.3947dfdaa0f8")}</td><td>{model.modelId}</td><td>{model.modelName || copy("usage-chart.extra.a99bc331d9ad")}</td><td>{model.totals?.responses ?? 0}</td><MeasureCells totals={model.totals} /></tr>)}
    {otherModels ? <tr><td colSpan={4}><LocalizedText id="usage-chart.otherModelsMeasuredGroups_01ba79" components={{ s0: <>{otherModels.modelCount}</> }} /></td><td>{otherModels.totals?.responses ?? 0}</td><MeasureCells totals={otherModels.totals} /></tr> : null}
  </tbody></table><p>{copy("usage-chart.unmeasuredGroupsAreExcludedFromRanking_223165")}</p></div>;
}

export function ModelUsageChart({ analytics }: { analytics: UsageAnalytics }) {
  useLocale();
  const [selected, setSelected] = useState<number | null>(null);
  const [open, setOpen] = useState(false);
  const rows = modelRows(analytics);
  const maximum = rows.reduce<bigint>((current, row) => { const value = exactTotal(row.totals?.total); return value !== undefined && value > current ? value : current; }, 0n);
  const selectedRow = selected === null ? undefined : rows[selected];
  const dataId = "usage-model-data";
  return <section className="usage-chart-panel" aria-labelledby="usage-model-title">
    <header><div><h3 id="usage-model-title">{copy("usage-chart.byModelApi_d2ab3b")}</h3><p>{copy("usage-chart.originalProviderAndModelIdentitiesRanked_b57157")}</p></div><ViewData id={dataId} open={open} change={setOpen} /></header>
    {rows.length ? <svg className="usage-model-svg" viewBox="0 0 900 278" role="group" aria-describedby="usage-model-help" aria-label={copy("usage-chart.rankedChartOfMeasuredProviderAnd_fba701", { v0: rows.length })} tabIndex={0} onFocus={() => setSelected((current) => current ?? 0)} onKeyDown={(event) => keyIndex(event, selected ?? 0, rows.length, setSelected, "vertical")}>
      {rows.map((row, index) => {
        const total = exactTotal(row.totals?.total) ?? 0n;
        const y = modelPlot.top + index * modelPlot.row;
        const width = normalized(total.toString(), maximum, modelPlot.right - modelPlot.left);
        return <g key={row.key} className={selected === index ? "usage-model-row usage-model-row-selected" : "usage-model-row"} onMouseEnter={() => setSelected(index)} onMouseLeave={() => setSelected(null)}>
          <text className="usage-model-rank" x="208" y={y + 16} textAnchor="end">{row.modelCount ? copy("usage-chart.other_f97e9d") : copy("usage-chart.message_2f5350", { v0: index + 1 })}</text>
          <rect className="usage-model-track" x={modelPlot.left} y={y} width={modelPlot.right - modelPlot.left} height="22" rx="4" />
          <rect className="usage-model-bar" x={modelPlot.left} y={y} width={width} height="22" rx="4" />
          <line className="usage-chart-grid" x1={modelPlot.left} x2={modelPlot.left} y1={y - 3} y2={y + 25} />
        </g>;
      })}
      <text className="usage-axis-label" x={modelPlot.left} y="270" textAnchor="start">0</text><text className="usage-axis-label" x={modelPlot.right} y="270" textAnchor="end">{axisTotal(maximum)}</text>
    </svg> : <p className="usage-chart-empty">{copy("usage-chart.noModelGroupHasAKnown_2b483d")}</p>}
    {rows.length ? <ol className="usage-model-labels">{rows.map((row, index) => <li key={row.key}><span className="usage-model-label-rank">{row.modelCount ? copy("usage-chart.otherModels_1b8bc2") : copy("usage-chart.message_2f5350", { v0: index + 1 })}</span><strong>{row.modelName || (row.modelCount ? copy("usage-chart.extra.1b8bc2ba488c") : copy("usage-chart.sentence.1d3a37cc1c5e", { v0: index + 1 }))}</strong><span>{row.modelCount ? copy("usage-chart.measuredModelGroupsNoMonetaryRollup_3a25fa", { v0: row.modelCount }) : copy("usage-chart.model_be9a1a", { v0: row.subscriptionService ? copy("usage-chart.subscriptionService_67a163", { v0: subscriptionServiceLabel(row.subscriptionService) }) : copy("usage-chart.apiProvider_f01871", { v0: row.providerName || row.providerId, v1: row.providerId }), v1: row.modelId })}</span><span className="usage-model-value"><LocalizedText id="usage-chart.tokens_a59f00" components={{ s0: <>{exactText(row.totals?.total)}</>, s1: <>{evidence(row.totals)}</> }} /></span></li>)}</ol> : null}
    <p className="usage-chart-help" id="usage-model-help">{copy("usage-chart.focusTheChartToInspectRanked_30453a")}</p>
    <p className="usage-chart-detail" role="status" aria-live="polite">{selectedRow ? <><LocalizedText id="usage-chart.tokens_eb2379" components={{ s0: <strong>{selectedRow.modelName || selectedRow.modelId || copy("usage-chart.extra.1b8bc2ba488c")}</strong>, s1: <>{selectedRow.modelCount ? copy("usage-chart.measuredGroups_0b5210", { v0: selectedRow.modelCount }) : copy("usage-chart.model_836ccd", { v0: selectedRow.subscriptionService ? copy("usage-chart.subscriptionService_67a163", { v0: subscriptionServiceLabel(selectedRow.subscriptionService) }) : copy("usage-chart.provider_7a7f6c", { v0: selectedRow.providerId }), v1: selectedRow.modelId })}</>, s2: <>{exactText(selectedRow.totals?.total)}</>, s3: <>{evidence(selectedRow.totals)}</> }} /></> : copy("usage-chart.moveFocusToAChartRow_839f35")}</p>
    <ModelViewData models={analytics.models} otherModels={analytics.otherModels} id={dataId} open={open} />
    {analytics.models.some((model) => exactTotal(model.totals?.total) === undefined) ? <p className="usage-unranked">{copy("usage-chart.unmeasuredModelsAreNotRankedTheir_7ac3b6")}</p> : null}
  </section>;
}

export function UsageCharts({ analytics, timeZone }: { analytics: UsageAnalytics; timeZone: string }) {
  useLocale();
  return <div className="usage-charts"><DailyUsageChart days={analytics.days} timeZone={timeZone} /><ModelUsageChart analytics={analytics} /></div>;
}
