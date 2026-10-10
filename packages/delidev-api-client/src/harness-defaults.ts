// SPDX-License-Identifier: Apache-2.0
export enum HarnessInheritanceState { Inherit = "inherit", Override = "override" }
export type InheritedHarnessValue<T> = { state: HarnessInheritanceState.Inherit } | { state: HarnessInheritanceState.Override; value: T };
export interface HarnessModelDefinition {
  provider_id?: string;
  subscription_service?: string;
  native_id: string;
  name?: string;
  context_limit?: number;
  input_modalities?: string[];
  metadata_source: string;
}
export interface HarnessSelection {
  effort?: InheritedHarnessValue<string>;
  permission?: InheritedHarnessValue<string>;
  claude_permission?: InheritedHarnessValue<string>;
  approval_policy?: InheritedHarnessValue<string>;
  approvals_reviewer?: InheritedHarnessValue<string>;
  approval_review_model?: InheritedHarnessValue<string>;
  subagent_model?: InheritedHarnessValue<string>;
  subagent_effort?: InheritedHarnessValue<string>;
  max_concurrency?: InheritedHarnessValue<number>;
  service_tier?: InheritedHarnessValue<string>;
}
export interface HarnessDefault {
  harness: string;
  provider_id?: string;
  subscription_service?: string;
  api_protocol?: string;
  model?: HarnessModelDefinition;
  selection: HarnessSelection;
}
const object = (value: unknown): value is Record<string, unknown> => value !== null && typeof value === "object" && !Array.isArray(value);
const keys = new Set(["effort", "permission", "claude_permission", "approval_policy", "approvals_reviewer", "approval_review_model", "subagent_model", "subagent_effort", "max_concurrency", "service_tier"]);
export function validInheritedHarnessValue(value: unknown, model = false): boolean {
  if (!object(value) || Object.keys(value).some(key => key !== "state" && key !== "value")) return false;
  if (value.state === HarnessInheritanceState.Inherit) return !Object.hasOwn(value, "value");
  return value.state === HarnessInheritanceState.Override && Object.hasOwn(value, "value") && (model ? object(value.value) : typeof value.value === "string" || typeof value.value === "number" && Number.isSafeInteger(value.value) && value.value >= 0 && value.value <= 0xffffffff);
}
export function validHarnessSelection(value: unknown): boolean {
  return object(value) && Object.entries(value).every(([key, entry]) => {
    if (!keys.has(key) || !object(entry) || !validInheritedHarnessValue(entry)) return false;
    if (entry.state === HarnessInheritanceState.Inherit) return true;
    return key === "max_concurrency" ? typeof entry.value === "number" : typeof entry.value === "string";
  });
}
