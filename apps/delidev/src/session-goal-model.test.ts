// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import { goalActionPending, goalBudget, goalObjective, nativeGoalView } from "./session-goal-model";
const source = { enabled: true, source_execution_id: "original-execution", source_native_thread_id: "original-thread" };
const goal = { objective: "Keep original objective", status: "active", token_budget: null, tokens_used: "9007199254740993", time_used_seconds: "0", created_at: "1", updated_at: "2" };
describe("original native goal projection", () => {
  it("preserves unknown, observed absence, false enabled, null budget and exact decimal counters", () => {
    expect(nativeGoalView(source)?.observation).toBeUndefined();
    expect(nativeGoalView({ ...source, enabled: false, observation: null })?.observation).toBeNull();
    expect(nativeGoalView({ ...source, observation: goal })?.observation).toEqual(goal);
    expect(nativeGoalView({ ...source, enabled: undefined })).toBeUndefined();
  });
  it("keeps action uncertainty independent from a readable or absent native observation", () => {
    for (const observation of [undefined, null, goal]) {
      const view = nativeGoalView({ ...source, observation, action_id: "original-action", action_state: "uncertain" });
      expect(goalActionPending(view)).toBe(true);
    }
    expect(goalActionPending(nativeGoalView({ ...source, observation: goal, action_id: "original-action", action_state: "acknowledged" }))).toBe(false);
  });
  it("rejects malformed counters, unsupported status and substituted projection fields", () => {
    for (const patch of [{ tokens_used: 0 }, { tokens_used: "9223372036854775808" }, { token_budget: "0" }, { status: "stopped" }, { objective: " " }, { extra: true }]) expect(nativeGoalView({ ...source, observation: { ...goal, ...patch } })).toBeUndefined();
    expect(nativeGoalView({ ...source, action_state: "acknowledged" })).toBeUndefined();
  });
  it("counts Unicode scalars and retains exact positive signed 64-bit budgets", () => {
    expect(goalObjective("🙂".repeat(4000))).toBe(true);
    expect(goalObjective("🙂".repeat(4001))).toBe(false);
    expect(goalObjective("\ud800")).toBe(false);
    expect(goalBudget("9223372036854775807")).toBe(9223372036854775807n);
    for (const value of ["0", "01", "-1", "1.0", "9223372036854775808"]) expect(goalBudget(value)).toBeUndefined();
  });
});
