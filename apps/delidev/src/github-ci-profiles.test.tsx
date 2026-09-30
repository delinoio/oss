// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { validPRCI } from "./github-ci";
import { queueCIObservation } from "./github-ci-fixture";
import { object, type Document } from "./documents";

const item = { state: "open", merged: false, base_ref: "main", base_sha: "a".repeat(40), head_sha: "b".repeat(40), node_id: "ITEM_stable", number: "17", repository_node_id: "R_37" };
const queueRule = { type: "merge_queue", ruleset_id: "20", source_kind: "repository", native_source_kind: "Repository", source: "fixture", digest: "e".repeat(64) };

it("accepts the supported queue rule only with original ALLGREEN entry evidence", () => {
  const queue = queueCIObservation();
  const value = { ...queue, rules: { ...queue.rules, rules: [...queue.rules.rules, queueRule] } };
  expect(validPRCI(value, item)).toBe(true);
  const unknown = { ...value, result: { source: "unknown", state: "unknown", reason: "commit-unverified", requirements: [] } };
  expect(validPRCI(unknown, item)).toBe(false);
  expect(validPRCI({ ...unknown, merge_queue: { ...queue.merge_queue, strategy: "HEADGREEN" } }, item)).toBe(true);
});

it("keeps pinned workflows unknown in ALLGREEN without erasing ordinary entry failures", () => {
  const queue = queueCIObservation();
  const workflow = { repository_id: "11", path: ".github/workflows/required.yml", sha: "d".repeat(40) };
  const check = { ...queue.head.contexts[0], node_id: "CHECK_merge", commit_sha: "c".repeat(40) };
  const run = { run_id: "91", node_id: "RUN_1", suite_node_id: "SUITE_1", commit_sha: check.commit_sha, attempt: "2", event: "pull_request", native_status: "COMPLETED", native_conclusion: "FAILURE", source: workflow, source_file_node_id: "FILE_1", source_blob_sha: "f".repeat(40), jobs: [{ node_id: check.node_id, native_status: "COMPLETED", native_conclusion: "FAILURE" }] };
  const rule = { ...queueRule, type: "workflows", ruleset_id: "19", required_workflows: { workflows: [workflow] } };
  const requirement = { ruleset_id: "19", context: workflow.path, workflow, state: "unknown", reason: "workflow-unverified", result_node_ids: [] as string[] };
  const value = { ...queue, test_merge: { commit_sha: check.commit_sha, total_count: "1", contexts: [check] }, workflow_runs: [run], rules: { ...queue.rules, rules: [...queue.rules.rules, queueRule, rule] }, result: { ...queue.result, state: "unknown", reason: "workflow-unverified", requirements: [...queue.result.requirements, requirement] } };
  expect(validPRCI(value, item)).toBe(true);
  expect(validPRCI({ ...value, workflow_runs: undefined }, item)).toBe(true);
  expect(validPRCI({ ...value, result: { ...value.result, state: "terminal-failure", reason: "observed" } }, item)).toBe(false);
  requirement.state = "terminal-failure";
  requirement.reason = "observed";
  requirement.result_node_ids = [check.node_id];
  expect(validPRCI(value, item)).toBe(false);
});

it("rejects stale queue failure labels and accepts only the native check assessment", () => {
  for (const [status, conclusion, state, reason] of [
    ["COMPLETED", "SUCCESS", "non-failing", "observed"],
    ["COMPLETED", "NEUTRAL", "non-failing", "observed"],
    ["COMPLETED", "SKIPPED", "non-failing", "observed"],
    ["IN_PROGRESS", undefined, "pending", "observed"],
    ["COMPLETED", "FUTURE", "unknown", "unknown-native-result"],
    ["FUTURE", undefined, "unknown", "unknown-native-result"],
    ["IN_PROGRESS", "FAILURE", "unknown", "unknown-native-result"],
  ]) {
    const value = queueCIObservation();
    Object.assign(value.merge_queue.rollup.contexts[0], { native_status: status, native_conclusion: conclusion });
    expect(validPRCI(value, item), `${status}/${conclusion} cannot retain failure`).toBe(false);
    Object.assign(value.result, { state, reason });
    Object.assign(value.result.requirements[0], { state, reason });
    expect(validPRCI(value, item), `${status}/${conclusion} native assessment`).toBe(true);
    value.result.requirements[0].reason = "observed";
    if (reason !== "observed") expect(validPRCI(value, item)).toBe(false);
  }
});

it("requires every matching queue result and preserves unknown duplicate-App authority", () => {
  const value = queueCIObservation();
  const second = structuredClone(value.merge_queue.rollup.contexts[0]);
  second.node_id = "CHECK_second";
  second.native_conclusion = "SUCCESS";
  value.merge_queue.rollup.contexts.push(second);
  value.merge_queue.rollup.total_count = "2";
  expect(validPRCI(value, item)).toBe(false);
  value.result.state = value.result.requirements[0].state = "unknown";
  value.result.reason = value.result.requirements[0].reason = "unknown-native-result";
  value.result.requirements[0].result_node_ids.push(second.node_id);
  expect(validPRCI(value, item)).toBe(true);
  value.result.requirements[0].result_node_ids.reverse();
  expect(validPRCI(value, item)).toBe(false);
});

it("classifies queued commit statuses independently of Checks and App restrictions", () => {
  for (const [native, state] of [["SUCCESS", "non-failing"], ["ERROR", "terminal-failure"], ["EXPECTED", "pending"], ["FUTURE", "unknown"]]) {
    const base = queueCIObservation();
    const source = base.merge_queue.rollup.contexts[0];
    const status = { kind: "commit-status", node_id: "STATUS_1", name: source.name, commit_sha: source.commit_sha, required: true, native_status: native, evidence: { created_at: source.evidence.started_at, updated_at: source.evidence.completed_at } };
    const value: Document = { ...base, merge_queue: { ...base.merge_queue, rollup: { ...base.merge_queue.rollup, contexts: [status] } }, rules: { ...base.rules, rules: [{ ...base.rules.rules[0], required_checks: { strict: false, checks: [{ context: status.name }] } }] }, result: { ...base.result, requirements: [{ ...base.result.requirements[0], integration_id: undefined, result_node_ids: [status.node_id] }] } };
    const result = object(value.result), requirement = object((result.requirements as Document[])[0]);
    expect(validPRCI(value, item)).toBe(native === "ERROR");
    const reason = state === "unknown" ? "unknown-native-result" : "observed";
    Object.assign(result, { state, reason }); Object.assign(requirement, { state, reason });
    expect(validPRCI(value, item)).toBe(true);
    const rules = object(value.rules).rules as Document[];
    const checks = object(rules[0].required_checks).checks as Document[];
    checks[0].integration_id = requirement.integration_id = "15368";
    Object.assign(result, { state: "unknown", reason: "app-unverified" });
    Object.assign(requirement, { state: "unknown", reason: "app-unverified" });
    expect(validPRCI(value, item)).toBe(true);
  }
});

it("recomputes missing and unknown queue provenance instead of accepting forged failure", () => {
  for (const mode of ["missing", "optional", "wrong-app", "zero-app", "missing-app", "wrong-event"]) {
    const value = queueCIObservation(), context = value.merge_queue.rollup.contexts[0];
    let reason = "no-matching-result";
    const ids: string[] = [];
    if (mode === "missing") { value.merge_queue.rollup.contexts = []; value.merge_queue.rollup.total_count = "0"; }
    if (mode === "optional") context.required = false;
    if (mode === "wrong-app") context.application.id = "99";
    if (mode === "zero-app") {
      value.rules.rules[0].required_checks.checks[0].integration_id = value.result.requirements[0].integration_id = "0";
      reason = "app-unverified";
    }
    if (mode === "missing-app") { Object.assign(context, { application: undefined }); reason = "app-unverified"; ids.push(context.node_id); }
    if (mode === "wrong-event") { context.workflow_event = "pull_request"; reason = "workflow-unverified"; ids.push(context.node_id); }
    expect(validPRCI(value, item), mode).toBe(false);
    Object.assign(value.result, { state: "unknown", reason });
    Object.assign(value.result.requirements[0], { state: "unknown", reason, result_node_ids: ids });
    expect(validPRCI(value, item), mode).toBe(true);
  }
});
