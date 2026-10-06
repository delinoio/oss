import { LocalizedText, copy, useLocale } from "./localization";
import { NativeGrokToolsTerminal } from "./native-grok-interactions";
import { NativeGrokTerminal, NativeGrokStop } from "./native-grok";
import { type Resource } from "@delinoio/delidev-api-client";
import { memo } from "react";
import { Harness } from "./configuration-fields";
import { NativeClaudePermissionProgress } from "./native-claude-progress";
import { NativeClaudeTerminal } from "./native-claude-terminal";
import { NativeClaudeDenialCompletion } from "./native-claude-denial-completion";
import { NativeClaudeStop } from "./native-claude-stop";
import { document, items, object, text, type Document } from "./documents";

const knownOptions = new Set(["permission", "claude_permission", "approval_policy", "subagent_model", "subagent_effort", "max_concurrency", "approval_review_model", "service_tier"]);

function requested(value: unknown): string { return text(value) || copy("execution-configuration.extra.dc12bec5d71f"); }
function observed(value: unknown): string {
  if (typeof value !== "string") return copy("execution-configuration.extra.ca1844969742");
  return value === "" ? copy("execution-configuration.extra.4ee7e86d4220") : value;
}
function integer(value: unknown): string {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? String(value) : copy("execution-configuration.extra.ca1844969742");
}
function grokContext(value: unknown): string {
  return typeof value === "number" && Number.isInteger(value) && value >= 1024 && value <= 1_000_000_000 ? String(value) : copy("execution-configuration.extra.ca1844969742");
}

function NativeObservations({ progress, selection, harness }: { progress: Document; selection: Document; harness: unknown }) {
  useLocale();
  const native = object(progress.observed);
  const available = Boolean(text(progress.execution_id) && text(progress.input_id) && text(progress.native_thread_id) && text(native.model));
  const current = available && progress.execution_id === selection.id && progress.input_id === selection.input_id;
  return <section aria-label={copy("execution-configuration.nativeExecutionObservations_f1235b")}>
    <h3>{copy("execution-configuration.nativeObservations_e8b2af")}</h3>
    {!available ? <p>{copy("execution-configuration.noNativeSettingsHaveBeenObserved_053128")}</p> : <>
      {!current ? <p className="notice">{copy("execution-configuration.theseRetainedObservationsBelongToAn_a47a6f")}</p> : null}
      <p><LocalizedText id="execution-configuration.observedExecutionInput_e6a4af" components={{ s0: <>{text(progress.execution_id)}</>, s1: <>{text(progress.input_id)}</> }} /></p>
      <dl>
        <dt>{copy("execution-configuration.observedModel_11caed")}</dt><dd>{observed(native.model)}</dd>
        <dt>{copy("execution-configuration.observedReasoningEffort_9f998e")}</dt><dd>{observed(native.effort)}</dd>
        <dt>{copy("execution-configuration.observedServiceTier_e525bf")}</dt><dd>{observed(native.service_tier)}</dd>
        {harness === Harness.Codex ? <><dt>{copy("execution-configuration.observedSandbox_2dccbb")}</dt><dd>{observed(native.permission)}</dd><dt>{copy("execution-configuration.observedApprovalPolicy_5aef26")}</dt><dd>{observed(native.approval_policy)}</dd></> : null}
        {harness === Harness.Claude ? <><dt>{copy("execution-configuration.observedClaudePermission_d99838")}</dt><dd>{observed(native.claude_permission)}</dd></> : null}
        {harness === Harness.OpenCode ? <><dt>{copy("execution-configuration.observedOpencodePrimaryAgent_0cf8a6")}</dt><dd>{observed(native.opencode_agent)}</dd><dt>{copy("execution-configuration.observedPermissionSelection_4bd92e")}</dt><dd>{observed(native.permission)}</dd></> : null}
        {harness === Harness.Grok ? <><dt>{copy("execution-configuration.observedGrokCurrentMode_3ced65")}</dt><dd>{observed(progress.grok_current_mode)}</dd><dt>{copy("execution-configuration.observedGrokInitialMode_0ede1b")}</dt><dd>{observed(native.grok_mode)}</dd><dt>{copy("execution-configuration.observedGrokContextWindow_6bc29e")}</dt><dd>{grokContext(native.grok_context_tokens)}</dd></> : null}
      </dl>
      {harness === Harness.Claude ? <NativeClaudePermissionProgress progress={progress} /> : null}
      {harness === Harness.Claude ? <NativeClaudeTerminal progress={progress} /> : null}
      {harness === Harness.Grok ? <><NativeGrokToolsTerminal progress={progress}/><NativeGrokTerminal progress={progress} /><NativeGrokStop progress={progress} /></> : null}
      {harness === Harness.Claude ? <NativeClaudeStop progress={progress} /> : null}
      {harness === Harness.Claude ? <NativeClaudeDenialCompletion progress={progress} /> : null}
      <p>{copy("execution-configuration.observationsDescribeThisRecordedExecutionMissing_bc0495")}</p>
    </>}
  </section>;
}

// Read only the session's retained document. Looking up today's Agent/model or
// template would replace historical choices with later edits or deletions.
// Render only DeliDev-owned fields, never arbitrary native prompt/config data.
export const ExecutionConfiguration = memo(function ExecutionConfiguration({ resource }: { resource: Resource }) {
  useLocale();
  const data = document(resource);
  const initial = object(data.initial_execution);
  const configuration = object(initial.configuration);
  const options = object(configuration.options);
  const context = object(configuration.grok_context);
  const childModel = object(configuration.subagent_model);
  const contextSource = context.source === "known" ? copy("execution-configuration.extra.4eb2581de1a8") : context.source === "user-declared" ? copy("execution-configuration.extra.5852e0c77d3d") : copy("execution-configuration.extra.ca1844969742");
  const current = object(data.current_execution);
  const selection = text(current.id) ? current : { id: initial.id, input_id: initial.input_id, account_id: initial.initial_account_id };
  const templates = items(configuration.templates);
  const accounts = items(configuration.accounts);
  const ready = Boolean(text(initial.id) && text(configuration.agent_id) && text(configuration.native_model));
  return <details className="execution-configuration">
    <summary>{copy("execution-configuration.executionConfigurationAndInstructions_2a7935")}</summary>
    {resource.schemaVersion !== 1 ? <p>{copy("execution-configuration.thisSessionDocumentVersionIsNot_9413f8")}</p> : !ready ? <p>{data.initial_execution == null ? copy("execution-configuration.noAcceptedExecutionConfigurationIsAvailable_17c6b2") : copy("execution-configuration.theSavedExecutionConfigurationIsIncomplete_2c32e0")}</p> : <>
      <section aria-label={copy("execution-configuration.savedExecutionConfiguration_2d8ee9")}>
        <h3>{copy("execution-configuration.savedConfiguration_d194b3")}</h3>
        <p>{copy("execution-configuration.capturedWhenTheFirstExecutionWas_e736dd")}</p>
        <dl>
          <dt>{copy("execution-configuration.harness_e3b5b4")}</dt><dd>{text(configuration.harness) || copy("execution-configuration.extra.ca1844969742")}</dd>
          <dt>{copy("execution-configuration.requestedModel_b36591")}</dt><dd>{text(configuration.native_model)}</dd>
          {configuration.harness === Harness.Grok ? <><dt>{copy("execution-configuration.savedGrokContextWindow_df1d70")}</dt><dd>{context.source !== "known" && context.source !== "user-declared" ? copy("execution-configuration.unavailable_ca1844") : grokContext(context.tokens)}</dd><dt>{copy("execution-configuration.savedContextSource_adf512")}</dt><dd>{contextSource}</dd></> : null}
          <dt>{copy("execution-configuration.requestedReasoningEffort_dc59bb")}</dt><dd>{requested(configuration.effort)}</dd>
          <dt>{copy("execution-configuration.savedPermissionSelection_abd7db")}</dt><dd>{requested(options.permission)}</dd>
          {configuration.harness === Harness.Claude || options.claude_permission !== undefined ? <><dt>{copy("execution-configuration.savedClaudePermission_0741e2")}</dt><dd>{requested(options.claude_permission)}</dd></> : null}
          <dt>{copy("execution-configuration.requestedApprovalPolicy_617392")}</dt><dd>{requested(options.approval_policy)}</dd>
          <dt>{copy("execution-configuration.requestedSubagentModel_cbe7e9")}</dt><dd>{requested(options.subagent_model)}</dd>
          {text(childModel.model_id) ? <><dt>{copy("execution-configuration.savedChildModelIdentity_be5624")}</dt><dd><LocalizedText id="execution-configuration.revision_dfefbf" components={{ s0: <>{text(childModel.model_id)}</>, s1: <>{integer(childModel.model_revision)}</> }} /></dd></> : null}
          <dt>{copy("execution-configuration.requestedSubagentEffort_bb821c")}</dt><dd>{requested(options.subagent_effort)}</dd>
          <dt>{copy("execution-configuration.requestedConcurrency_406d92")}</dt><dd>{options.max_concurrency === undefined || options.max_concurrency === 0 ? copy("execution-configuration.nativeDefaultRequested_6a1fdb") : integer(options.max_concurrency)}</dd>
          <dt>{copy("execution-configuration.requestedApprovalReviewModel_136d13")}</dt><dd>{requested(options.approval_review_model)}</dd>
          <dt>{copy("execution-configuration.requestedServiceTier_14f88a")}</dt><dd>{requested(options.service_tier)}</dd>
          <dt>{copy("execution-configuration.accountRouting_0c3707")}</dt><dd>{text(configuration.routing) || copy("execution-configuration.extra.ca1844969742")}</dd>
          <dt>{copy("execution-configuration.firstAccount_2896cc")}</dt><dd>{text(initial.initial_account_id) || copy("execution-configuration.extra.ca1844969742")}</dd>
          <dt>{copy("execution-configuration.selectedExecutionAccount_012de8")}</dt><dd>{text(selection.account_id) || copy("execution-configuration.extra.ca1844969742")}</dd>
        </dl>
        {Object.keys(options).some((key) => !knownOptions.has(key)) ? <p className="notice">{copy("execution-configuration.thisSnapshotContainsAdditionalOptionsThat_76c12b")}</p> : null}
        <details><summary>{copy("execution-configuration.snapshotReferencesAndAccountOrder_513309")}</summary>
          <dl><dt>{copy("execution-configuration.firstExecution_5294a1")}</dt><dd>{text(initial.id)}</dd><dt>{copy("execution-configuration.selectedExecution_c6fa3c")}</dt><dd>{text(selection.id)}</dd><dt>{copy("execution-configuration.agentWorker_a4caa7")}</dt><dd><LocalizedText id="execution-configuration.revision_dfefbf" components={{ s0: <>{text(configuration.agent_id)}</>, s1: <>{integer(configuration.agent_revision)}</> }} /></dd><dt>{copy("execution-configuration.model_5e2c61")}</dt><dd><LocalizedText id="execution-configuration.revision_dfefbf" components={{ s0: <>{text(configuration.model_id)}</>, s1: <>{integer(configuration.model_revision)}</> }} /></dd><dt>{copy("execution-configuration.provider_472590")}</dt><dd>{text(configuration.provider_id)}</dd><dt>{copy("execution-configuration.acceptedAt_1b8950")}</dt><dd>{text(initial.accepted_at) || copy("execution-configuration.extra.ca1844969742")}</dd></dl>
          {Array.isArray(configuration.accounts) ? <ol aria-label={copy("execution-configuration.savedAccountOrder_c00a0a")}>{accounts.map((value, index) => { const account = object(value); return <li key={index}><LocalizedText id="execution-configuration.weight_0d7216" components={{ s0: <>{text(account.id) || copy("execution-configuration.extra.643fd990f260")}</>, s1: <>{integer(account.weight)}</> }} /></li>; })}</ol> : <p>{copy("execution-configuration.savedAccountOrderIsUnavailable_e54c4a")}</p>}
        </details>
      </section>
      <NativeObservations progress={object(data.execution)} selection={selection} harness={configuration.harness} />
      <section aria-label={copy("execution-configuration.appliedDelidevInstructions_f2bcde")}>
        <h3>{copy("execution-configuration.appliedDelidevInstructions_f2bcde")}</h3>
        <p>{copy("execution-configuration.readOnlyAdditionalInstructionsFromThis_8850cd")}</p>
        {typeof configuration.instructions !== "string" ? <p>{copy("execution-configuration.appliedInstructionTextIsUnavailable_b0a185")}</p> : configuration.instructions === "" ? <p>{copy("execution-configuration.noAdditionalDelidevInstructionsWereSelected_7b2306")}</p> : <pre aria-label={copy("execution-configuration.attribute.e9ae418fcc27")}>{configuration.instructions}</pre>}
        {Array.isArray(configuration.templates) ? <ol aria-label={copy("execution-configuration.appliedInstructionTemplateOrder_be0ff4")}>{templates.map((value, index) => { const template = object(value); return <li key={index}><details><summary><LocalizedText id="execution-configuration.templateRevision_10577b" components={{ s0: <>{index + 1}</>, s1: <>{text(template.id) || copy("execution-configuration.extra.ccd130d59f4b")}</>, s2: <>{integer(template.revision)}</> }} /></summary>{typeof template.contents === "string" ? <pre>{template.contents}</pre> : <p>{copy("execution-configuration.savedTemplateTextIsUnavailable_95e25e")}</p>}</details></li>; })}</ol> : <p>{copy("execution-configuration.appliedTemplateOrderIsUnavailable_02e090")}</p>}
      </section>
    </>}
  </details>;
});
