// SPDX-License-Identifier: Apache-2.0
import { SystemCapability, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text, type Document } from "./documents";
export const sourceHarness = (resource: Resource) => text(object(object(document(resource).initial_execution).configuration).harness);
export const settled = (resource: Resource) => {
  const data = document(resource), execution = object(data.execution), initial = object(data.initial_execution);
  const harness = text(object(initial.configuration).harness);
  const profile = harness === "codex" || harness === "opencode" && data.workspace === "general-chat" && !Object.keys(object(execution.subagents)).length && !Object.keys(object(execution.native_compactions)).length && !execution.latest_workspace_event_id && !execution.latest_todo_id && !execution.latest_plan_id && object(execution.observed).opencode_agent === "build";
  return resource.schemaVersion === 1 && (data.storage === undefined || object(data.storage).state === "present") && data.fork === undefined && !data.compaction_job_id && (data.context_revision ?? 0) === (execution.context_revision ?? 0) && data.archive === "active" && data.recovery === "none" && data.outcome === "succeeded" && !data.active_execution_id && !data.pending_steer_id && execution.cleanup_verified === true && !execution.unconfirmed_responses && !object(execution.waiting).user_input && !object(execution.waiting).approval && text(execution.native_turn_id) && profile;
};

export function sidechatSupported(source: Resource, capabilities: readonly SystemCapability[] | undefined, runner: Document) {
  const managed = object(object(document(source).initial_execution).configuration).subscription === true;
  const workers = runner.worker_capabilities;
  return sourceHarness(source) === "codex" && (!managed || capabilities?.includes(SystemCapability.MANAGED_CODEX_SIDECHAT_V1) && Array.isArray(workers) && workers.includes("managed-codex-sidechat-v1") && workers.includes("managed-codex-subscriptions-v1")) && !Object.keys(object(object(document(source).execution).subagents)).length && capabilities?.includes(SystemCapability.NATIVE_SIDECHAT_V1) && Array.isArray(workers) && workers.includes("codex-read-only-sidechat-v1");
}
