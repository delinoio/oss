import { LocalizedText, copy, useLocale } from "./localization";
import { useQuery } from "@connectrpc/connect-query";
import { SystemCapability, SystemQuery } from "@delinoio/delidev-api-client";
import { text, type Document } from "./documents";
import { Problem } from "./ui";

enum ChildEffort { None = "none", Minimal = "minimal", Low = "low", Medium = "medium", High = "high", XHigh = "xhigh", Max = "max", Ultra = "ultra", Persistent = "persistent" }

export function CodexSubagentConfiguration({ options, active, change }: { options: Document; active: boolean; change: (key: string, value: unknown) => void }) {
  useLocale();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const supported = status.data?.capabilities.includes(SystemCapability.CODEX_SUBAGENT_CONFIGURATION_V1) === true;
  const effort = text(options.subagent_effort);
  return <fieldset><legend>{copy("codex-subagent-configuration.codexSubagents_8b1822")}</legend>
    <Problem error={status.error} />
    {!supported ? <p>{copy("codex-subagent-configuration.updateTheConnectedServerAndRunner_466379")}</p> : <p>{copy("codex-subagent-configuration.childrenUseTheParentSSelected_7b2922")}</p>}
    <label>{copy("codex-subagent-configuration.subagentModel_28463c")}<input maxLength={256} value={text(options.subagent_model)} disabled={!supported} onChange={event => change("subagent_model", event.target.value)} /></label>
    <p>{copy("codex-subagent-configuration.useTheExactRegisteredNativeModel_e6e196")}</p>
    <label>{copy("codex-subagent-configuration.subagentEffort_eea2b1")}<select value={effort} disabled={!supported} onChange={event => change("subagent_effort", event.target.value)}>
      <option value="">{copy("codex-subagent-configuration.useNativeDefault_b5fc7e")}</option>
      {effort && !Object.values(ChildEffort).includes(effort as ChildEffort) ? <option value={effort}><LocalizedText id="codex-subagent-configuration.unsupportedSelection_7c6fcd" components={{ s0: <>{effort}</> }} /></option> : null}
      {Object.values(ChildEffort).map(value => <option key={value} value={value}>{value}</option>)}
    </select></label>
    <label>{copy("codex-subagent-configuration.maximumConcurrency0UsesNativeDefault_451d39")}<input disabled={!supported} type="number" min={0} max={64} step={1} value={Number(options.max_concurrency ?? 0)} onChange={event => change("max_concurrency", Number(event.target.value))} /></label>
    <p>{copy("codex-subagent-configuration.requestedSettingsDoNotEstablishAn_4e5da8")}</p>
  </fieldset>;
}
