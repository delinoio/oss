import { AccountingUnitKind, UsageAccountingProfile, type GetUsageSummaryResponse, type UsageTotals } from "@delinoio/delidev-api-client";

function grok(totals?: UsageTotals) {
  return totals?.accounting.find((unit) => unit.kind === AccountingUnitKind.GROK_CLOSED_INPUT);
}

function total(totals?: UsageTotals): string {
  const value = grok(totals);
  return value && value.measuredUnits > 0 && /^\d+$/.test(value.knownTotal) ? BigInt(value.knownTotal).toLocaleString() : "Unavailable";
}

export function GrokAccounting({ data, open }: { data: GetUsageSummaryResponse; open: (id: string) => void }) {
  if (data.accountingProfile !== UsageAccountingProfile.NATIVE_UNITS_V1) return <p role="status">Verified Grok input accounting is unavailable from this server version. Update the server to view it.</p>;
  const units = grok(data.totals);
  const timeFormatter = new Intl.DateTimeFormat(undefined, { timeZone: data.analytics?.timeZone || "UTC", year: "numeric", month: "long", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit", timeZoneName: "shortOffset" });
  const groups = data.groups.filter((group) => grok(group.totals));
  const days = data.analytics?.days.filter((day) => grok(day.totals));
  const models = data.analytics?.models.filter((model) => grok(model.totals));
  return <section className="usage-detail" aria-labelledby="grok-accounting-title">
    <h2 id="grok-accounting-title">Verified Grok closed inputs</h2>
    <p>{units ? `${units.units.toLocaleString()} closed inputs · ${total(data.totals)} known total tokens` : "No verified Grok closed inputs are recorded for these filters. This does not establish zero usage."}</p>
    <p>Each input requires matching original history, native closure and confirmed cleanup. Response dimensions are retained separately in the session. These totals are separate from Codex response totals; pricing, actual cost and estimated-budget contribution are unavailable.</p>
    {groups.length ? <div className="usage-table" role="region" aria-label="Verified Grok input accounting tables" tabIndex={0}>
      <table><caption>Grok inputs by original session, account and model</caption><thead><tr><th scope="col">Session / project</th><th scope="col">Account</th><th scope="col">Model / API</th><th scope="col">Closed inputs</th><th scope="col">Known tokens</th></tr></thead><tbody>{groups.map((group) => <tr key={`${group.sessionId}:${group.accountId}:${group.providerId}:${group.modelId}`}>
        <td><button type="button" onClick={() => open(group.sessionId)}>{group.sessionName || group.sessionId}</button><small>{group.sessionId}</small><p>{group.projectId ? group.projectName || group.projectId : "General Chat"}</p></td>
        <td>{group.accountName || group.accountId}<small>{group.accountId}</small></td><td>{group.modelName || group.modelId}<small>{group.modelId}</small><p>{group.providerName || group.providerId}</p><small>{group.providerId}</small></td><td>{grok(group.totals)?.units.toLocaleString()}</td><td>{total(group.totals)}</td>
      </tr>)}</tbody></table>
      {days?.length ? <table><caption>Daily verified Grok inputs ({data.analytics?.timeZone})</caption><thead><tr><th scope="col">From</th><th scope="col">Until</th><th scope="col">Closed inputs</th><th scope="col">Known tokens</th></tr></thead><tbody>{days.map((day) => <tr key={`${day.fromUnixMs}:${day.untilUnixMs}`}><td><time dateTime={new Date(Number(day.fromUnixMs)).toISOString()}>{timeFormatter.format(new Date(Number(day.fromUnixMs)))}</time></td><td><time dateTime={new Date(Number(day.untilUnixMs)).toISOString()}>{timeFormatter.format(new Date(Number(day.untilUnixMs)))}</time> <span>(exclusive)</span></td><td>{grok(day.totals)?.units.toLocaleString()}</td><td>{total(day.totals)}</td></tr>)}</tbody></table> : null}
      {models?.length ? <table><caption>Verified Grok inputs by model</caption><thead><tr><th scope="col">Model / API</th><th scope="col">Closed inputs</th><th scope="col">Known tokens</th></tr></thead><tbody>{models.map((model) => <tr key={`${model.providerId}:${model.modelId}`}><td>{model.modelName || model.modelId}<small>{model.modelId}</small><p>{model.providerName || model.providerId}</p><small>{model.providerId}</small></td><td>{grok(model.totals)?.units.toLocaleString()}</td><td>{total(model.totals)}</td></tr>)}</tbody></table> : null}
    </div> : null}
  </section>;
}
