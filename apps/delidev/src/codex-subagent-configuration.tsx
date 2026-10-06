// SPDX-License-Identifier: Apache-2.0
import { useQuery } from "@connectrpc/connect-query";
import { SystemCapability, SystemQuery } from "@delinoio/delidev-api-client";
import { text, type Document } from "./documents";
import { Problem } from "./ui";
import { ReasoningEffortField, codexEffortSuggestions } from "./reasoning-effort-field";

export function CodexSubagentConfiguration({ options, active, change }: { options: Document; active: boolean; change: (key: string, value: unknown) => void }) {
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  const supported = status.data?.capabilities.includes(SystemCapability.CODEX_SUBAGENT_CONFIGURATION_V1) === true;
  return <fieldset><legend>Codex subagents</legend>
    <Problem error={status.error} />
    {!supported ? <p>Update the connected server and Runner Device to apply Codex subagent configuration. Saved values are retained.</p> : <p>Children use the parent's selected account. The accepted execution freezes these choices; later Agent edits apply to future sessions.</p>}
    <label>Subagent model<input maxLength={256} value={text(options.subagent_model)} disabled={!supported} onChange={event => change("subagent_model", event.target.value)} /></label>
    <p>Use the exact registered native model from the same provider or subscription service. An empty value leaves Codex's native default unspecified.</p>
    <ReasoningEffortField label="Subagent effort" value={options.subagent_effort} disabled={!supported} suggestions={codexEffortSuggestions} change={value => change("subagent_effort", value)} />
    <label>Maximum concurrency (0 uses native default)<input disabled={!supported} type="number" min={0} max={64} step={1} value={Number(options.max_concurrency ?? 0)} onChange={event => change("max_concurrency", Number(event.target.value))} /></label>
    <p>Requested settings do not establish an observed child model. Subagents shows available native observations separately.</p>
  </fieldset>;
}
