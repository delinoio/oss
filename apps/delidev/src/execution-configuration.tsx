import { NativeGrokTerminal } from "./native-grok";
import { type Resource } from "@delinoio/delidev-api-client";
import { memo } from "react";
import { Harness } from "./configuration-fields";
import { NativeClaudePermissionProgress } from "./native-claude-progress";
import { NativeClaudeTerminal } from "./native-claude-terminal";
import { NativeClaudeDenialCompletion } from "./native-claude-denial-completion";
import { NativeClaudeStop } from "./native-claude-stop";
import { document, items, object, text, type Document } from "./documents";

const knownOptions = new Set(["permission", "claude_permission", "approval_policy", "subagent_model", "subagent_effort", "max_concurrency", "approval_review_model", "service_tier"]);

function requested(value: unknown): string { return text(value) || "Not specified"; }
function observed(value: unknown): string {
  if (typeof value !== "string") return "Unavailable";
  return value === "" ? "Empty native value" : value;
}
function integer(value: unknown): string {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? String(value) : "Unavailable";
}

function NativeObservations({ progress, selection, harness }: { progress: Document; selection: Document; harness: unknown }) {
  const native = object(progress.observed);
  const available = Boolean(text(progress.execution_id) && text(progress.input_id) && text(progress.native_thread_id) && text(native.model));
  const current = available && progress.execution_id === selection.id && progress.input_id === selection.input_id;
  return <section aria-label="Native execution observations">
    <h3>Native observations</h3>
    {!available ? <p>No native settings have been observed for this execution.</p> : <>
      {!current ? <p className="notice">These retained observations belong to an earlier execution. The selected execution has no matching observations yet.</p> : null}
      <p>Observed execution: {text(progress.execution_id)} · Input: {text(progress.input_id)}</p>
      <dl>
        <dt>Observed model</dt><dd>{observed(native.model)}</dd>
        <dt>Observed reasoning effort</dt><dd>{observed(native.effort)}</dd>
        <dt>Observed service tier</dt><dd>{observed(native.service_tier)}</dd>
        {harness === Harness.Codex ? <><dt>Observed sandbox</dt><dd>{observed(native.permission)}</dd><dt>Observed approval policy</dt><dd>{observed(native.approval_policy)}</dd></> : null}
        {harness === Harness.Claude ? <><dt>Observed Claude permission</dt><dd>{observed(native.claude_permission)}</dd></> : null}
        {harness === Harness.OpenCode ? <><dt>Observed OpenCode primary agent</dt><dd>{observed(native.opencode_agent)}</dd><dt>Observed permission selection</dt><dd>{observed(native.permission)}</dd></> : null}
        {harness === Harness.Grok ? <><dt>Observed Grok initial mode</dt><dd>{observed(native.grok_mode)}</dd></> : null}
      </dl>
      {harness === Harness.Claude ? <NativeClaudePermissionProgress progress={progress} /> : null}
      {harness === Harness.Claude ? <NativeClaudeTerminal progress={progress} /> : null}
      {harness === Harness.Grok ? <NativeGrokTerminal progress={progress} /> : null}
      {harness === Harness.Claude ? <NativeClaudeStop progress={progress} /> : null}
      {harness === Harness.Claude ? <NativeClaudeDenialCompletion progress={progress} /> : null}
      <p>Observations describe this recorded execution. Missing values remain unavailable.</p>
    </>}
  </section>;
}

// Read only the session's retained document. Looking up today's Agent/model or
// template would replace historical choices with later edits or deletions.
// Render only DeliDev-owned fields, never arbitrary native prompt/config data.
export const ExecutionConfiguration = memo(function ExecutionConfiguration({ resource }: { resource: Resource }) {
  const data = document(resource);
  const initial = object(data.initial_execution);
  const configuration = object(initial.configuration);
  const options = object(configuration.options);
  const current = object(data.current_execution);
  const selection = text(current.id) ? current : { id: initial.id, input_id: initial.input_id, account_id: initial.initial_account_id };
  const templates = items(configuration.templates);
  const accounts = items(configuration.accounts);
  const ready = Boolean(text(initial.id) && text(configuration.agent_id) && text(configuration.native_model));
  return <details className="execution-configuration">
    <summary>Execution configuration and instructions</summary>
    {resource.schemaVersion !== 1 ? <p>This session document version is not supported by this app.</p> : !ready ? <p>{data.initial_execution == null ? "No accepted execution configuration is available yet. Current Agent settings are not an execution snapshot." : "The saved execution configuration is incomplete or unavailable."}</p> : <>
      <section aria-label="Saved execution configuration">
        <h3>Saved configuration</h3>
        <p>Captured when the first execution was accepted. Later Agent, model and template edits do not change these saved choices.</p>
        <dl>
          <dt>Harness</dt><dd>{text(configuration.harness) || "Unavailable"}</dd>
          <dt>Requested model</dt><dd>{text(configuration.native_model)}</dd>
          <dt>Requested reasoning effort</dt><dd>{requested(configuration.effort)}</dd>
          <dt>Saved permission selection</dt><dd>{requested(options.permission)}</dd>
          {configuration.harness === Harness.Claude || options.claude_permission !== undefined ? <><dt>Saved Claude permission</dt><dd>{requested(options.claude_permission)}</dd></> : null}
          <dt>Requested approval policy</dt><dd>{requested(options.approval_policy)}</dd>
          <dt>Requested subagent model</dt><dd>{requested(options.subagent_model)}</dd>
          <dt>Requested subagent effort</dt><dd>{requested(options.subagent_effort)}</dd>
          <dt>Requested concurrency</dt><dd>{options.max_concurrency === undefined || options.max_concurrency === 0 ? "Native default requested" : integer(options.max_concurrency)}</dd>
          <dt>Requested approval review model</dt><dd>{requested(options.approval_review_model)}</dd>
          <dt>Requested service tier</dt><dd>{requested(options.service_tier)}</dd>
          <dt>Account routing</dt><dd>{text(configuration.routing) || "Unavailable"}</dd>
          <dt>First account</dt><dd>{text(initial.initial_account_id) || "Unavailable"}</dd>
          <dt>Selected execution account</dt><dd>{text(selection.account_id) || "Unavailable"}</dd>
        </dl>
        {Object.keys(options).some((key) => !knownOptions.has(key)) ? <p className="notice">This snapshot contains additional options that this app version cannot display.</p> : null}
        <details><summary>Snapshot references and account order</summary>
          <dl><dt>First execution</dt><dd>{text(initial.id)}</dd><dt>Selected execution</dt><dd>{text(selection.id)}</dd><dt>Agent Worker</dt><dd>{text(configuration.agent_id)} · revision {integer(configuration.agent_revision)}</dd><dt>Model</dt><dd>{text(configuration.model_id)} · revision {integer(configuration.model_revision)}</dd><dt>Provider</dt><dd>{text(configuration.provider_id)}</dd><dt>Accepted at</dt><dd>{text(initial.accepted_at) || "Unavailable"}</dd></dl>
          {Array.isArray(configuration.accounts) ? <ol aria-label="Saved account order">{accounts.map((value, index) => { const account = object(value); return <li key={index}>{text(account.id) || "Unavailable account"} · weight {integer(account.weight)}</li>; })}</ol> : <p>Saved account order is unavailable.</p>}
        </details>
      </section>
      <NativeObservations progress={object(data.execution)} selection={selection} harness={configuration.harness} />
      <section aria-label="Applied DeliDev instructions">
        <h3>Applied DeliDev instructions</h3>
        <p>Read-only additional instructions from this session's saved templates. Internal harness prompts are not shown.</p>
        {typeof configuration.instructions !== "string" ? <p>Applied instruction text is unavailable.</p> : configuration.instructions === "" ? <p>No additional DeliDev instructions were selected.</p> : <pre aria-label="Combined applied instructions">{configuration.instructions}</pre>}
        {Array.isArray(configuration.templates) ? <ol aria-label="Applied instruction template order">{templates.map((value, index) => { const template = object(value); return <li key={index}><details><summary>Template {index + 1} · {text(template.id) || "Unavailable reference"} · revision {integer(template.revision)}</summary>{typeof template.contents === "string" ? <pre>{template.contents}</pre> : <p>Saved template text is unavailable.</p>}</details></li>; })}</ol> : <p>Applied template order is unavailable.</p>}
      </section>
    </>}
  </details>;
});
