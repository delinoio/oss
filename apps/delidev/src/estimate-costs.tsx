import { LocalizedText, copy, displayLocale, useLocale } from "./localization";
import { type EstimateMeasure, type EstimateTotals, type PricingUsage } from "@delinoio/delidev-api-client";
import { PricingBasis } from "./pricing";

export function EstimateAmounts({ value }: { value?: EstimateTotals }) {
  useLocale();
  if (!value) return <p>{copy("estimate-costs.unavailableNoEstimateEvidenceWasReturned_ef74c0")}</p>;
  return <div>{value.currencies.length ? <ul>{value.currencies.map((row) => <li key={row.currency}><strong>{row.currency} {row.knownAmount || "Unavailable"}</strong><p><LocalizedText id="estimate-costs.responsesWithCompleteTokenPriceCategories_c0673b" components={{ s0: <>{row.completeResponses.toLocaleString(displayLocale())}</>, s1: <>{row.partialResponses.toLocaleString(displayLocale())}</>, s2: <>{row.unavailableResponses.toLocaleString(displayLocale())}</> }} /></p></li>)}</ul> : <p>{copy("estimate-costs.unavailableNoMatchingHistoricalPricingBasis_b5d88c")}</p>}
    {value.unpricedResponses ? <p><LocalizedText id="estimate-costs.responsesHaveNoMatchingHistoricalPrice_cb4664" components={{ s0: <>{value.unpricedResponses.toLocaleString(displayLocale())}</> }} /></p> : null}</div>;
}
function EstimateCategory({ name, value }: { name: string; value?: EstimateMeasure }) {
  useLocale();
  return <tr><th scope="row">{name}</th><td>{value?.knownTokens ? BigInt(value.knownTokens).toLocaleString(displayLocale()) : copy("estimate-costs.unavailable_ca1844")}</td><td>{value?.knownAmount || "Unavailable"}</td><td>{value ? copy("estimate-costs.pricedMissingPriceMissingUsageUnsupported_f37de2", { v0: value.pricedResponses, v1: value.missingPriceResponses, v2: value.missingUsageResponses, v3: value.unsupportedBreakdownResponses, v4: value.notApplicableResponses }) : copy("estimate-costs.unavailable_ca1844")}</td></tr>;
}
export function EstimateCosts({ totals, pricing }: { totals?: EstimateTotals; pricing: PricingUsage[] }) {
  useLocale();
  return <section aria-label={copy("estimate-costs.historicalTokenPriceEstimates_3eb47a")}><p><strong>{copy("estimate-costs.tokenPriceEstimate_580be5")}</strong> {totals?.currencies.some((row) => row.knownAmount !== "") ? copy("estimate-costs.knownSubtotalsWithIncompleteTelemetry_9a1323") : copy("estimate-costs.unavailableForThisSelection_4ad32a")}</p><EstimateAmounts value={totals} />
    <p>{copy("estimate-costs.currenciesAreSeparateCompleteTokenPrice_8dc932")}</p>
    {pricing.map((row) => <details key={row.pricing?.id}><summary><LocalizedText id="estimate-costs.historicalBasisVersion_0fade6" components={{ s0: <>{row.pricing?.basis?.currency ?? copy("estimate-costs.unknownCurrency_4d3dc3")}</>, s1: <>{row.pricing?.revision.toString() ?? copy("estimate-costs.unknown_b23a6a")}</>, s2: <>{row.totals?.knownAmount || "Unavailable"}</> }} /></summary>{row.pricing ? <PricingBasis value={row.pricing} /> : null}<div className="usage-table"><table><caption>{copy("estimate-costs.coveredTokenPriceCategoriesInThis_db110c")}</caption><thead><tr><th scope="col">{copy("estimate-costs.category_292c06")}</th><th scope="col">{copy("estimate-costs.pricedTokens_37b8cd")}</th><th scope="col">{copy("estimate-costs.knownAmount_7ef51e")}</th><th scope="col">{copy("estimate-costs.responseEvidence_cb3bca")}</th></tr></thead><tbody><EstimateCategory name="Input" value={row.input} /><EstimateCategory name="Cached input" value={row.cachedInput} /><EstimateCategory name="Output" value={row.output} /></tbody></table></div></details>)}
  </section>;
}
