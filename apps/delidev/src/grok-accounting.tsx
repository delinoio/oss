import { LocalizedText, copy, displayLocale, useLocale } from "./localization";
import { AccountingUnitKind, UsageAccountingProfile, type GetUsageSummaryResponse, type UsageTotals } from "@delinoio/delidev-api-client";

function grok(totals?: UsageTotals) {
  return totals?.accounting.find((unit) => unit.kind === AccountingUnitKind.GROK_CLOSED_INPUT);
}

function total(totals?: UsageTotals): string {
  const value = grok(totals);
  return value && value.measuredUnits > 0 && /^\d+$/.test(value.knownTotal) ? BigInt(value.knownTotal).toLocaleString(displayLocale()) : "Unavailable";
}

export function GrokAccounting({ data, open }: { data: GetUsageSummaryResponse; open: (id: string) => void }) {
  useLocale();
  if (data.accountingProfile !== UsageAccountingProfile.NATIVE_UNITS_V1) return <p role="status">{copy("grok-accounting.verifiedGrokInputAccountingIsUnavailable_bad890")}</p>;
  const units = grok(data.totals);
  const timeFormatter = new Intl.DateTimeFormat(displayLocale(), { timeZone: data.analytics?.timeZone || "UTC", year: "numeric", month: "long", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit", timeZoneName: "shortOffset" });
  const groups = data.groups.filter((group) => grok(group.totals));
  const days = data.analytics?.days.filter((day) => grok(day.totals));
  const models = data.analytics?.models.filter((model) => grok(model.totals));
  return <section className="usage-detail" aria-labelledby="grok-accounting-title">
    <h2 id="grok-accounting-title">{copy("grok-accounting.verifiedGrokClosedInputs_f343dd")}</h2>
    <p>{units ? copy("grok-accounting.closedInputsKnownTotalTokens_a55f2e", { v0: units.units.toLocaleString(displayLocale()), v1: total(data.totals) }) : copy("grok-accounting.noVerifiedGrokClosedInputsAre_244c3c")}</p>
    <p>{copy("grok-accounting.eachInputRequiresMatchingOriginalHistory_2f9fce")}</p>
    <p>{copy("grok-accounting.grokInputTimesUseTheServer_55cef2")}</p>
    {groups.length ? <div className="usage-table" role="region" aria-label={copy("grok-accounting.verifiedGrokInputAccountingTables_ee8ebe")} tabIndex={0}>
      <table><caption>{copy("grok-accounting.grokInputsByOriginalSessionAccount_d2226c")}</caption><thead><tr><th scope="col">{copy("grok-accounting.sessionProject_59a44c")}</th><th scope="col">{copy("grok-accounting.account_7e1b0d")}</th><th scope="col">{copy("grok-accounting.modelApi_6a8129")}</th><th scope="col">{copy("grok-accounting.closedInputs_85c5d8")}</th><th scope="col">{copy("grok-accounting.knownTokens_682912")}</th></tr></thead><tbody>{groups.map((group) => <tr key={`${group.sessionId}:${group.accountId}:${group.providerId}:${group.modelId}`}>
        <td><button type="button" onClick={() => open(group.sessionId)}>{group.sessionName || group.sessionId}</button><small>{group.sessionId}</small><p>{group.projectId ? group.projectName || group.projectId : copy("grok-accounting.generalChat_f634bc")}</p>{group.projectId ? <small>{group.projectId}</small> : null}</td>
        <td>{group.accountName || group.accountId}<small>{group.accountId}</small></td><td>{group.modelName || group.modelId}<small>{group.modelId}</small><p>{group.providerName || group.providerId}</p><small>{group.providerId}</small></td><td>{grok(group.totals)?.units.toLocaleString(displayLocale())}</td><td>{total(group.totals)}</td>
      </tr>)}</tbody></table>
      {days?.length ? <table><caption><LocalizedText id="grok-accounting.dailyVerifiedGrokInputs_fc1da8" components={{ s0: <>{data.analytics?.timeZone}</> }} /></caption><thead><tr><th scope="col">{copy("grok-accounting.from_218197")}</th><th scope="col">{copy("grok-accounting.until_7caf85")}</th><th scope="col">{copy("grok-accounting.closedInputs_85c5d8")}</th><th scope="col">{copy("grok-accounting.knownTokens_682912")}</th></tr></thead><tbody>{days.map((day) => <tr key={`${day.fromUnixMs}:${day.untilUnixMs}`}><td><time dateTime={new Date(Number(day.fromUnixMs)).toISOString()}>{timeFormatter.format(new Date(Number(day.fromUnixMs)))}</time></td><td><time dateTime={new Date(Number(day.untilUnixMs)).toISOString()}>{timeFormatter.format(new Date(Number(day.untilUnixMs)))}</time> <span>{copy("grok-accounting.exclusive_b71bef")}</span></td><td>{grok(day.totals)?.units.toLocaleString(displayLocale())}</td><td>{total(day.totals)}</td></tr>)}</tbody></table> : null}
      {models?.length ? <table><caption>{copy("grok-accounting.verifiedGrokInputsByModel_1eac37")}</caption><thead><tr><th scope="col">{copy("grok-accounting.modelApi_6a8129")}</th><th scope="col">{copy("grok-accounting.closedInputs_85c5d8")}</th><th scope="col">{copy("grok-accounting.knownTokens_682912")}</th></tr></thead><tbody>{models.map((model) => <tr key={`${model.providerId}:${model.modelId}`}><td>{model.modelName || model.modelId}<small>{model.modelId}</small><p>{model.providerName || model.providerId}</p><small>{model.providerId}</small></td><td>{grok(model.totals)?.units.toLocaleString(displayLocale())}</td><td>{total(model.totals)}</td></tr>)}</tbody></table> : null}
    </div> : null}
  </section>;
}
