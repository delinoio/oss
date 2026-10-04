import { AccountingUnitKind, UsageAccountingProfile, type GetUsageSummaryResponse, type NativeAccountingMeasure, type NativeAccountingSummary, type NativeAccountingTotals } from "@delinoio/delidev-api-client";

const dimensions = [["Input", "input"], ["Cache read", "cacheRead"], ["Cache write", "cacheWrite"], ["Output", "output"], ["Thinking / reasoning", "thinking"], ["Native total", "total"]] as const;

export function nativeMeasure(value?: NativeAccountingMeasure): string {
  if (!value || !value.measuredUnits || !/^(0|[1-9][0-9]*)$/.test(value.knownTotal)) return "Unavailable";
  return `${BigInt(value.knownTotal).toLocaleString()}${value.unavailableUnits ? ` · ${value.unavailableUnits} unavailable units` : ""}`;
}

function Estimates({ totals }: { totals?: NativeAccountingTotals }) {
  return <>{totals?.currencies.map((value) => <p key={value.currency}>{value.currency} {value.knownAmount || "Unavailable"} · {value.completeUnits} complete · {value.partialUnits} partial · {value.unavailableUnits} unavailable units</p>)}<p>{totals?.unpricedUnits ?? 0} unpriced units. Actual charges are unavailable.</p></>;
}

function NativeSummary({ data, timeZone, open }: { data: NativeAccountingSummary; timeZone: string; open: (id: string) => void }) {
  const claude = data.totals?.kind === AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT;
  const title = claude ? "Claude main-loop inputs" : "OpenCode steps";
  const heading = `native-accounting-${data.totals?.kind}`;
  const format = new Intl.DateTimeFormat(undefined, { timeZone, dateStyle: "medium", timeStyle: "short" });
  return <section className="usage-detail" aria-labelledby={heading}>
    <h2 id={heading}>{title}</h2><p>{data.totals?.units.toLocaleString() ?? "0"} original {claude ? "input results" : "step-finish parts"} recorded</p>
    <p>{claude ? "Input combines uncached input, cache read and cache write. Thinking is included in output and is not charged again. Cumulative model snapshots and native dollar estimates are excluded. Native total tokens are unavailable." : "Each original step is counted once. Assistant summaries and inherited fork history are excluded. OpenCode input, cache read, cache write, nonreasoning output and reasoning are disjoint. Native normalized zero categories remain unavailable; an explicitly supplied total preserves zero."}</p>
    <p>Filters use first server retention. These units do not increase the distinct response count. Missing telemetry is unavailable, not zero.</p>
    <dl className="usage-metrics-secondary">{dimensions.map(([label, key]) => <div key={key}><dt>{label}</dt><dd>{nativeMeasure(data.totals?.[key])}</dd></div>)}</dl>
    <Estimates totals={data.totals} />
    {data.groups.length ? <div className="usage-table" role="region" aria-label={`${title} tables`} tabIndex={0}>
      <table><caption>{title} by original session, account and model</caption><thead><tr><th scope="col">Session / project</th><th scope="col">Account</th><th scope="col">Model / API</th><th scope="col">Units</th><th scope="col">Known categories</th><th scope="col">Historical estimate</th></tr></thead><tbody>{data.groups.map((group) => <tr key={`${group.sessionId}:${group.accountId}:${group.providerId}:${group.modelId}`}><td><button type="button" onClick={() => open(group.sessionId)}>{group.sessionId}</button><p>{group.projectId || "General Chat"}</p></td><td>{group.accountId}</td><td>{group.modelId}<p>{group.providerId}</p></td><td>{group.totals?.units.toLocaleString()}</td><td>{dimensions.map(([label, key]) => <p key={key}>{label}: {nativeMeasure(group.totals?.[key])}</p>)}</td><td><Estimates totals={group.totals} /></td></tr>)}</tbody></table>
      {data.days.length ? <table><caption>Daily {title} ({timeZone})</caption><thead><tr><th scope="col">From</th><th scope="col">Until (exclusive)</th><th scope="col">Units</th>{dimensions.map(([label, key]) => <th scope="col" key={key}>{label}</th>)}</tr></thead><tbody>{data.days.map((day) => <tr key={`${day.fromUnixMs}:${day.untilUnixMs}`}><td>{format.format(new Date(Number(day.fromUnixMs)))}</td><td>{format.format(new Date(Number(day.untilUnixMs)))}</td><td>{day.totals?.units.toLocaleString()}</td>{dimensions.map(([, key]) => <td key={key}>{nativeMeasure(day.totals?.[key])}</td>)}</tr>)}</tbody></table> : null}
      {data.models.length ? <table><caption>{title} by model</caption><thead><tr><th scope="col">Model / API</th><th scope="col">Units</th><th scope="col">Historical estimate</th></tr></thead><tbody>{data.models.map((model) => <tr key={`${model.providerId}:${model.modelId}`}><td>{model.modelId}<p>{model.providerId}</p></td><td>{model.totals?.units.toLocaleString()}</td><td><Estimates totals={model.totals} /></td></tr>)}</tbody></table> : null}
      {data.pricing.length ? <table><caption>Original price versions for {title}</caption><thead><tr><th scope="col">Price version</th><th scope="col">Source / as of</th><th scope="col">Category coverage</th></tr></thead><tbody>{data.pricing.map((price) => <tr key={price.pricing?.id}><td>{price.pricing?.id}<p>Revision {price.pricing?.revision.toString()}</p></td><td>{price.pricing?.basis?.source}<p>{price.pricing?.basis?.asOf}</p></td><td>{(["input", "cacheRead", "cacheWrite", "output", "reasoning"] as const).map((key) => <p key={key}>{key}: {price[key]?.knownAmount || "Unavailable"} · {price[key]?.pricedUnits ?? 0} priced · {price[key]?.missingUsageUnits ?? 0} missing usage · {price[key]?.missingPriceUnits ?? 0} missing price · {price[key]?.notApplicableUnits ?? 0} not applicable</p>)}</td></tr>)}</tbody></table> : null}
    </div> : <p>No original units are recorded for these filters. This does not establish zero usage or cost.</p>}
  </section>;
}

export function NativeAccounting({ data, open }: { data: GetUsageSummaryResponse; open: (id: string) => void }) {
  const kinds = new Set(data.nativeAccounting.map((summary) => summary.totals?.kind));
  if (data.accountingProfile !== UsageAccountingProfile.NATIVE_UNITS_V1 || data.nativeAccounting.length !== 2 || kinds.size !== 2 || !kinds.has(AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT) || !kinds.has(AccountingUnitKind.OPENCODE_STEP)) return <p role="status">Claude and OpenCode accounting is unavailable from this server version. Update the server to view it.</p>;
  return <>{data.nativeAccounting.map((summary) => <NativeSummary key={summary.totals?.kind} data={summary} timeZone={data.analytics?.timeZone || "UTC"} open={open} />)}</>;
}
