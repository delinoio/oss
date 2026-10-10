// SPDX-License-Identifier: Apache-2.0
import { NativeGoalStatus, type NativeGoalSnapshot, type NativeGoalView } from "@delinoio/delidev-api-client";
import { object } from "./documents";
import { timestampInstant } from "./timestamp-format";

export const goalStatuses = ["active", "paused", "blocked", "usageLimited", "budgetLimited", "complete"] as const;
export const goalStatusValues = [NativeGoalStatus.ACTIVE, NativeGoalStatus.PAUSED, NativeGoalStatus.BLOCKED, NativeGoalStatus.USAGE_LIMITED, NativeGoalStatus.BUDGET_LIMITED, NativeGoalStatus.COMPLETE] as const;
const decimal = (value: unknown): value is string => typeof value === "string" && /^(0|[1-9][0-9]{0,18})$/.test(value) && BigInt(value) <= 9223372036854775807n;
export function goalObjective(value: string): boolean {
  return value.trim().length > 0 && Array.from(value).length <= 4000 && !/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/u.test(value);
}
export function goalBudget(value: string): bigint | undefined {
  return decimal(value) && BigInt(value) > 0n ? BigInt(value) : undefined;
}
export function nativeGoalView(value: unknown): (NativeGoalView & { readonly enabled: boolean }) | undefined {
  const v = object(value);
  const allowed = ["enabled", "source_execution_id", "source_native_thread_id", "observation", "observed_at", "action_id", "action_state", "problem_code"];
  if (typeof v.enabled !== "boolean" || Object.keys(v).some(key => !allowed.includes(key)) || typeof v.source_execution_id !== "string" || !v.source_execution_id || typeof v.source_native_thread_id !== "string" || !v.source_native_thread_id) return;
  if (v.observed_at !== undefined && (typeof v.observed_at !== "string" || timestampInstant(v.observed_at) === undefined)) return;
  if (v.action_id !== undefined && (typeof v.action_id !== "string" || !v.action_id) || v.problem_code !== undefined && typeof v.problem_code !== "string") return;
  if (v.action_state !== undefined && !["accepted", "claimed", "acknowledged", "uncertain", "canceled"].includes(v.action_state as string)) return;
  if ((v.action_id === undefined) !== (v.action_state === undefined)) return;
  if (v.observation !== undefined && v.observation !== null) {
    const o = object(v.observation);
    const fields = ["objective", "status", "token_budget", "tokens_used", "time_used_seconds", "created_at", "updated_at"];
    if (Object.keys(o).length !== fields.length || Object.keys(o).some(key => !fields.includes(key)) || typeof o.objective !== "string" || !goalObjective(o.objective) || !goalStatuses.includes(o.status as NativeGoalSnapshot["status"]) || !decimal(o.tokens_used) || !decimal(o.time_used_seconds) || !decimal(o.created_at) || !decimal(o.updated_at) || o.token_budget !== null && (!decimal(o.token_budget) || BigInt(o.token_budget) <= 0n)) return;
  }
  return v as unknown as NativeGoalView & { readonly enabled: boolean };
}
export function goalActionPending(view?: NativeGoalView): boolean {
  return Boolean(view?.action_state && view.action_state !== "acknowledged" && view.action_state !== "canceled");
}
