import { AccountingSource, type AccountingDisclosures } from "./usage-accounting-source";
import { formatDecimal, LocalizedText, copy, displayLocale, useLocale } from "./localization";
import { AccountingUnitKind, UsageAccountingProfile, type GetUsageSummaryResponse, type NativeAccountingMeasure, type NativeAccountingSummary, type NativeAccountingTotals } from "@delinoio/delidev-api-client";

const dimensions = () => [[copy("native-accounting.extra.36ecb4f86691"), "input"], [copy("native-accounting.extra.0008ce30b97d"), "cacheRead"], [copy("native-accounting.extra.e7fc264359d6"), "cacheWrite"], [copy("native-accounting.extra.b2439bcb8dee"), "output"], [copy("native-accounting.extra.852cb285e12d"), "thinking"], [copy("native-accounting.extra.b1cbbb5582ff"), "total"]] as const;

export function nativeMeasure(value?: NativeAccountingMeasure): string {
  if (!value || !value.measuredUnits || !/^(0|[1-9][0-9]*)$/.test(value.knownTotal)) return copy("native-accounting.extra.ca1844969742");
  return `${BigInt(value.knownTotal).toLocaleString(displayLocale())}${value.unavailableUnits ? copy("native-accounting.unavailableUnits", { count: value.unavailableUnits }) : ""}`;
}

function Estimates({ totals }: { totals?: NativeAccountingTotals }) {
  useLocale();
  return <>{totals?.currencies.map((value) => <p key={value.currency}><LocalizedText id="native-accounting.completePartialUnavailableUnits_0365c9" components={{ s0: <>{value.currency}</>, s1: <>{formatDecimal(value.knownAmount ?? "") || copy("native-accounting.extra.ca1844969742")}</>, s2: <>{value.completeUnits}</>, s3: <>{value.partialUnits}</>, s4: <>{value.unavailableUnits}</> }} /></p>)}<p><LocalizedText id="native-accounting.unpricedUnitsActualChargesAreUnavailable_e4b3ba" components={{ s0: <>{totals?.unpricedUnits ?? 0}</> }} /></p></>;
}

function historicalAmount(amount?: string, currency?: string): string {
  return amount && currency ? `${currency} ${formatDecimal(amount)}` : copy("native-accounting.extra.ca1844969742");
}

function NativeSummary({ data, timeZone, open, disclosures }: { data: NativeAccountingSummary; timeZone: string; open: (id: string) => void; disclosures?: AccountingDisclosures }) {
  useLocale();
  const claude = data.totals?.kind === AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT;
  const title = claude ? copy("native-accounting.extra.3081f2ba9c0e") : copy("native-accounting.extra.583cefc35691");
  const heading = `native-accounting-${data.totals?.kind}`;
  const format = new Intl.DateTimeFormat(displayLocale(), { timeZone, dateStyle: "medium", timeStyle: "short" });
  return <AccountingSource kind={data.totals!.kind} title={title} count={data.totals!.units} disclosures={disclosures}>
    <div className="usage-detail">
    <h2 id={heading}>{title}</h2><p><LocalizedText id="native-accounting.originalRecorded_4752cd" components={{ s0: <>{data.totals?.units.toLocaleString(displayLocale()) ?? "0"}</>, s1: <>{claude ? copy("native-accounting.inputResults_562123") : copy("native-accounting.stepFinishParts_d29cde")}</> }} /></p>
    <details className="usage-source-definitions"><summary>{copy("usage.sourceDefinitions")}</summary><p>{claude ? copy("native-accounting.inputCombinesUncachedInputCacheRead_78e1e2") : copy("native-accounting.eachOriginalStepIsCountedOnce_5c3ff7")}</p>
    <p>{copy("native-accounting.filtersUseFirstServerRetentionThese_f419ed")}</p>
    </details>
    <dl className="usage-metrics-secondary">{dimensions().map(([label, key]) => <div key={key}><dt>{label}</dt><dd>{nativeMeasure(data.totals?.[key])}</dd></div>)}</dl>
    <Estimates totals={data.totals} />
    {data.groups.length ? <div className="usage-table" role="region" aria-label={copy("native-accounting.tables_ab268f", { v0: title })} tabIndex={0}>
      <table><caption><LocalizedText id="native-accounting.byOriginalSessionAccountAndModel_4d92e1" components={{ s0: <>{title}</> }} /></caption><thead><tr><th scope="col">{copy("native-accounting.sessionProject_59a44c")}</th><th scope="col">{copy("native-accounting.account_7e1b0d")}</th><th scope="col">{copy("native-accounting.modelApi_6a8129")}</th><th scope="col">{copy("native-accounting.units_9fb666")}</th><th scope="col">{copy("native-accounting.knownCategories_2b133a")}</th><th scope="col">{copy("native-accounting.historicalEstimate_178dcf")}</th></tr></thead><tbody>{data.groups.map((group) => <tr key={`${group.sessionId}:${group.accountId}:${group.providerId}:${group.modelId}`}><td><button type="button" onClick={() => open(group.sessionId)}>{group.sessionId}</button><p>{group.projectId || copy("native-accounting.extra.f634bca1f142")}</p></td><td>{group.accountId}</td><td>{group.modelId}<p>{group.providerId}</p></td><td>{group.totals?.units.toLocaleString(displayLocale())}</td><td>{dimensions().map(([label, key]) => <p key={key}>{label}: {nativeMeasure(group.totals?.[key])}</p>)}</td><td><Estimates totals={group.totals} /></td></tr>)}</tbody></table>
      {data.days.length ? <table><caption><LocalizedText id="native-accounting.daily_3b940d" components={{ s0: <>{title}</>, s1: <>{timeZone}</> }} /></caption><thead><tr><th scope="col">{copy("native-accounting.from_218197")}</th><th scope="col">{copy("native-accounting.untilExclusive_fac510")}</th><th scope="col">{copy("native-accounting.units_9fb666")}</th>{dimensions().map(([label, key]) => <th scope="col" key={key}>{label}</th>)}</tr></thead><tbody>{data.days.map((day) => <tr key={`${day.fromUnixMs}:${day.untilUnixMs}`}><td>{format.format(new Date(Number(day.fromUnixMs)))}</td><td>{format.format(new Date(Number(day.untilUnixMs)))}</td><td>{day.totals?.units.toLocaleString(displayLocale())}</td>{dimensions().map(([, key]) => <td key={key}>{nativeMeasure(day.totals?.[key])}</td>)}</tr>)}</tbody></table> : null}
      {data.models.length ? <table><caption><LocalizedText id="native-accounting.byModel_708927" components={{ s0: <>{title}</> }} /></caption><thead><tr><th scope="col">{copy("native-accounting.modelApi_6a8129")}</th><th scope="col">{copy("native-accounting.units_9fb666")}</th><th scope="col">{copy("native-accounting.historicalEstimate_178dcf")}</th></tr></thead><tbody>{data.models.map((model) => <tr key={`${model.providerId}:${model.modelId}`}><td>{model.modelId}<p>{model.providerId}</p></td><td>{model.totals?.units.toLocaleString(displayLocale())}</td><td><Estimates totals={model.totals} /></td></tr>)}</tbody></table> : null}
      {data.pricing.length ? <details className="usage-source-pricing"><summary>{copy("usage.historicalSourcePricing")}</summary><table><caption><LocalizedText id="native-accounting.originalPriceVersionsFor_074918" components={{ s0: <>{title}</> }} /></caption><thead><tr><th scope="col">{copy("native-accounting.priceVersion_3ba1a0")}</th><th scope="col">{copy("native-accounting.sourceAsOf_c4e9fa")}</th><th scope="col">{copy("native-accounting.categoryCoverage_a7251d")}</th></tr></thead><tbody>{data.pricing.map((price) => <tr key={price.pricing?.id}><td>{price.pricing?.id}<p><LocalizedText id="native-accounting.revision_058534" components={{ s0: <>{price.pricing?.revision.toString()}</> }} /></p></td><td>{price.pricing?.basis?.source}<p>{price.pricing?.basis?.asOf}</p></td><td>{(["input", "cacheRead", "cacheWrite", "output", "reasoning"] as const).map((key) => <p key={key}><LocalizedText id="native-accounting.pricedMissingUsageMissingPriceNot_393c9f" components={{ s0: <>{key}</>, s1: <>{historicalAmount(price[key]?.knownAmount, price.pricing?.basis?.currency)}</>, s2: <>{price[key]?.pricedUnits ?? 0}</>, s3: <>{price[key]?.missingUsageUnits ?? 0}</>, s4: <>{price[key]?.missingPriceUnits ?? 0}</>, s5: <>{price[key]?.notApplicableUnits ?? 0}</> }} /></p>)}</td></tr>)}</tbody></table></details> : null}
    </div> : <p>{copy("native-accounting.noOriginalUnitsAreRecordedFor_7dfc97")}</p>}
  </div></AccountingSource>;
}

export function NativeAccounting({ data, open, disclosures }: { data: GetUsageSummaryResponse; open: (id: string) => void; disclosures?: AccountingDisclosures }) {
  useLocale();
  const kinds = new Set(data.nativeAccounting.map((summary) => summary.totals?.kind));
  if (data.accountingProfile !== UsageAccountingProfile.NATIVE_UNITS_V1 || data.nativeAccounting.length !== 2 || kinds.size !== 2 || !kinds.has(AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT) || !kinds.has(AccountingUnitKind.OPENCODE_STEP)) return <p role="status">{copy("native-accounting.claudeAndOpencodeAccountingIsUnavailable_19b5e9")}</p>;
  return <>{data.nativeAccounting.map((summary) => <NativeSummary key={summary.totals?.kind} data={summary} timeZone={data.analytics?.timeZone || "UTC"} open={open} disclosures={disclosures} />)}</>;
}
