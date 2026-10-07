import { LocalizedText, copy, useLocale } from "./localization";
import { useQuery } from "@connectrpc/connect-query";
import { SystemCapability, SystemQuery } from "@delinoio/delidev-api-client";
import { text, type Document } from "./documents";
import { Problem } from "./ui";
import { ReasoningEffortField, codexEffortSuggestions } from "./reasoning-effort-field";

export function CodexSubagentConfiguration({ options, active, change }: { options: Document; active: boolean; change: (key: string, value: unknown) => void }) {
  useLocale();
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const supported = status.data?.capabilities.includes(SystemCapability.CODEX_SUBAGENT_CONFIGURATION_V1) === true;
  return <fieldset><legend>{copy("codex-subagent-configuration.codexSubagents_8b1822")}</legend>
    <Problem error={status.error} />
    {!supported ? <p>{copy("codex-subagent-configuration.updateTheConnectedServerAndRunner_466379")}</p> : <p>{copy("codex-subagent-configuration.childrenUseTheParentSSelected_7b2922")}</p>}
    <label>{copy("codex-subagent-configuration.subagentModel_28463c")}<input maxLength={256} value={text(options.subagent_model)} disabled={!supported} onChange={event => change("subagent_model", event.target.value)} /></label>
    <p>{copy("codex-subagent-configuration.useTheExactRegisteredNativeModel_e6e196")}</p>
    <ReasoningEffortField label={copy("codex-subagent-configuration.subagentEffort_eea2b1")} value={options.subagent_effort} disabled={!supported} suggestions={codexEffortSuggestions} change={value => change("subagent_effort", value)} />
    <label>{copy("codex-subagent-configuration.maximumConcurrency0UsesNativeDefault_451d39")}<input disabled={!supported} type="number" min={0} max={4294967295} step={1} value={Number(options.max_concurrency ?? 0)} onChange={event => change("max_concurrency", Number(event.target.value))} /></label>
    <p>{copy("codex-subagent-configuration.requestedSettingsDoNotEstablishAn_4e5da8")}</p>
  </fieldset>;
}
