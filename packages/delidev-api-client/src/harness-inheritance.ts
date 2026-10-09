// SPDX-License-Identifier: Apache-2.0
export enum HarnessInheritance { Inherit = "inherit", Override = "override" }
export const harnessValueFields = ["model", "effort", "permission", "claude_permission", "approvals_reviewer", "subagent_model", "subagent_effort", "max_concurrency", "approval_policy", "approval_review_model", "service_tier"] as const;
export type HarnessValueField = typeof harnessValueFields[number];
export interface HarnessSelection { state: HarnessInheritance; value?: string | number }
export type HarnessValues = Record<HarnessValueField, HarnessSelection>;
export interface HarnessDefaultEntry { harness: string; provider_id?: string; subscription_service?: string; api_protocol?: string; values: HarnessValues }
const record = (v: unknown): v is Record<string, unknown> => v !== null && typeof v === "object" && !Array.isArray(v);
export function inheritedHarnessValues(): HarnessValues { return Object.fromEntries(harnessValueFields.map(key => [key, { state: HarnessInheritance.Inherit }])) as HarnessValues; }
export function readableHarnessValues(value: unknown): value is HarnessValues {
 return record(value) && harnessValueFields.every(key => {const selection=value[key];return record(selection) && (selection.state===HarnessInheritance.Inherit && !Object.hasOwn(selection,"value") || selection.state===HarnessInheritance.Override && Object.hasOwn(selection,"value") && (key==="max_concurrency" ? typeof selection.value==="number" && Number.isInteger(selection.value) && selection.value>=0 && selection.value<=4294967295 : typeof selection.value==="string"));});
}
export function readableHarnessInheritance(kind: "agent" | "defaults", value: Record<string, unknown>): boolean {
 if(kind==="agent") {const settings=value.harness_settings;const count=Array.isArray(value.routes)&&value.routes.length?value.routes.length:1;return record(settings) && settings.version===1 && readableHarnessValues(settings.values) && settings.values.model.state===HarnessInheritance.Inherit && Array.isArray(settings.models)&&settings.models.length===count&&settings.models.every(v=>record(v)&&(v.state===HarnessInheritance.Inherit&&!Object.hasOwn(v,"value")||v.state===HarnessInheritance.Override&&typeof v.value==="string"));}
 return value.harness_defaults_version===1 && Array.isArray(value.harness_defaults) && value.harness_defaults.length<=256 && value.harness_defaults.every(entry=>record(entry)&&["codex","claude-code","opencode","grok-build"].includes(entry.harness as string)&&readableHarnessValues(entry.values));
}
export function effectiveHarnessSelection(field: HarnessValueField, harness: string, source: Pick<HarnessDefaultEntry,"provider_id"|"subscription_service"|"api_protocol">, server: readonly HarnessDefaultEntry[], project: readonly HarnessDefaultEntry[], agent?: HarnessValues): { selection: HarnessSelection; source: "native" | "server" | "project" | "agent" } {
 let selection:HarnessSelection={state:HarnessInheritance.Inherit};let owner:"native"|"server"|"project"|"agent"="native";
 for(const [entries,scope] of [[server,"server"],[project,"project"]] as const) for(let rank=0;rank<3;rank++) for(const entry of entries) {
  const specificity=entry.api_protocol?2:entry.provider_id||entry.subscription_service?1:0;
  if(entry.harness!==harness||rank!==specificity||entry.provider_id&&entry.provider_id!==source.provider_id||entry.subscription_service&&entry.subscription_service!==source.subscription_service||entry.api_protocol&&entry.api_protocol!==source.api_protocol)continue;
  if(entry.values[field]?.state===HarnessInheritance.Override){selection=entry.values[field];owner=scope;}
 }
 if(agent?.[field]?.state===HarnessInheritance.Override){selection=agent[field];owner="agent";}
 return {selection,source:owner};
}
