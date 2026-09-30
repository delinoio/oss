// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { validPRCI } from "./github-ci";
import { queueCIObservation } from "./github-ci-fixture";

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
