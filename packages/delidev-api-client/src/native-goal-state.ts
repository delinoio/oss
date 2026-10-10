// SPDX-License-Identifier: Apache-2.0
/** Last observed original native goal. Decimal counters preserve exact int64 values. */
export interface NativeGoalSnapshot {
  readonly objective: string;
  readonly status: "active" | "paused" | "blocked" | "usageLimited" | "budgetLimited" | "complete";
  readonly token_budget: string | null;
  readonly tokens_used: string;
  readonly time_used_seconds: string;
  readonly created_at: string;
  readonly updated_at: string;
}

/** Session document native_goal projection. Missing observation means unknown;
 * explicit null means observed absence. Neither acknowledges an uncertain action. */
export interface NativeGoalView {
  readonly source_execution_id: string;
  readonly source_native_thread_id: string;
  readonly observation?: NativeGoalSnapshot | null;
  readonly observed_at?: string;
  readonly action_id?: string;
  readonly action_state?: "accepted" | "claimed" | "acknowledged" | "uncertain";
  readonly problem_code?: string;
}
