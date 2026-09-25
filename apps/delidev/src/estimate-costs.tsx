import { type EstimateMeasure, type EstimateTotals, type PricingUsage } from "@delinoio/delidev-api-client";
import { PricingBasis } from "./pricing";

export function EstimateAmounts({ value }: { value?: EstimateTotals }) {
  if (!value) return <p>Unavailable — no estimate evidence was returned.</p>;
  return <div>{value.currencies.length ? <ul>{value.currencies.map((row) => <li key={row.currency}><strong>{row.currency} {row.knownAmount || "Unavailable"}</strong><p>{row.completeResponses.toLocaleString()} responses with complete token-price categories · {row.partialResponses.toLocaleString()} partial · {row.unavailableResponses.toLocaleString()} unavailable.</p></li>)}</ul> : <p>Unavailable — no matching historical pricing basis.</p>}
    {value.unpricedResponses ? <p>{value.unpricedResponses.toLocaleString()} responses have no matching historical price.</p> : null}</div>;
}
function EstimateCategory({ name, value }: { name: string; value?: EstimateMeasure }) {
  return <tr><th scope="row">{name}</th><td>{value?.knownTokens ? BigInt(value.knownTokens).toLocaleString() : "Unavailable"}</td><td>{value?.knownAmount || "Unavailable"}</td><td>{value ? `${value.pricedResponses} priced · ${value.missingPriceResponses} missing price · ${value.missingUsageResponses} missing usage · ${value.unsupportedBreakdownResponses} unsupported breakdown · ${value.notApplicableResponses} not applicable` : "Unavailable"}</td></tr>;
}
export function EstimateCosts({ totals, pricing }: { totals?: EstimateTotals; pricing: PricingUsage[] }) {
  return <section aria-label="Historical token-price estimates"><p><strong>Token-price estimate:</strong> {totals?.currencies.some((row) => row.knownAmount !== "") ? "Known subtotals, with incomplete telemetry." : "Unavailable for this selection."}</p><EstimateAmounts value={totals} />
    <p>Currencies are separate. Complete token-price categories do not establish complete native usage, actual spend or budget compliance. Configure future prices in Settings → Models → Token pricing.</p>
    {pricing.map((row) => <details key={row.pricing?.id}><summary>Historical basis · {row.pricing?.basis?.currency ?? "Unknown currency"} · version {row.pricing?.revision.toString() ?? "unknown"} · {row.totals?.knownAmount || "Unavailable"}</summary>{row.pricing ? <PricingBasis value={row.pricing} /> : null}<div className="usage-table"><table><caption>Covered token-price categories in this selection</caption><thead><tr><th scope="col">Category</th><th scope="col">Priced tokens</th><th scope="col">Known amount</th><th scope="col">Response evidence</th></tr></thead><tbody><EstimateCategory name="Input" value={row.input} /><EstimateCategory name="Cached input" value={row.cachedInput} /><EstimateCategory name="Output" value={row.output} /></tbody></table></div></details>)}
  </section>;
}
