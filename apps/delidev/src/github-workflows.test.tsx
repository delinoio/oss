// SPDX-License-Identifier: Apache-2.0
import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { PRCI, validPRCI } from "./github-ci";
import { ciObservation } from "./github-ci-fixture";
import { validPRRules, PRRules } from "./github-rules";

const item = { state: "open", merged: false, base_ref: "main", base_sha: "a".repeat(40), head_sha: "b".repeat(40) };
function fixture() {
  const base = ciObservation();
  const workflow: { repository_id: string; path: string; sha?: string } = { repository_id: "11", path: ".github/workflows/required.yml", sha: "d".repeat(40) };
  const rules = { ...base.rules, rules: [{ type: "workflows", ruleset_id: "19", source_kind: "organization", native_source_kind: "Organization", source: "fixture", digest: "e".repeat(64), required_workflows: { workflows: [workflow] } }] };
  const row = structuredClone(base.head.contexts[0]);
  row.node_id = "CHECK_merge"; row.commit_sha = "c".repeat(40);
  const test_merge = { commit_sha: row.commit_sha, total_count: "1", contexts: [row] };
  const workflow_runs = [{ run_id: "91", node_id: "RUN_1", suite_node_id: "SUITE_1", commit_sha: row.commit_sha, attempt: "2", event: "pull_request", native_status: "COMPLETED", native_conclusion: "FAILURE", source: workflow, source_file_node_id: "FILE_1", source_blob_sha: "f".repeat(40), jobs: [{ node_id: row.node_id, native_status: "COMPLETED", native_conclusion: "FAILURE" }] }];
  const result = { source: "test-merge", evaluated_sha: row.commit_sha, state: "terminal-failure", reason: "observed", requirements: [{ ruleset_id: "19", context: workflow.path, workflow, state: "terminal-failure", reason: "observed", result_node_ids: [row.node_id] }] };
  return { ...base, rules, test_merge, workflow_runs, result };
}

it("validates and renders numeric workflow source, immutable SHA and independently attributed attempt", () => {
  const value = fixture();
  expect(validPRCI(value, item)).toBe(true);
  render(<PRCI value={value} />);
  expect(screen.getByText(/Run 91 · attempt 2/)).toBeTruthy();
  expect(screen.getByText("d".repeat(40))).toBeTruthy();
  expect(screen.queryAllByRole("link")).toHaveLength(0);
});

it("rejects malformed sources, wrong merge commits, cross-workflow jobs and duplicate inventory identities", () => {
  for (const mode of ["path", "sha", "commit", "event", "jobs", "duplicate", "reference"] ) {
    const value = fixture();
    if (mode === "path") value.workflow_runs[0].source.path = ".github/workflows/../private";
    if (mode === "sha") value.workflow_runs[0].source.sha = "main";
    if (mode === "commit") value.workflow_runs[0].commit_sha = item.head_sha;
    if (mode === "event") value.workflow_runs[0].event = "pull_request_target";
    if (mode === "jobs") value.workflow_runs[0].jobs[0].node_id = "OLDER_JOB";
    if (mode === "duplicate") value.workflow_runs.push(structuredClone(value.workflow_runs[0]));
    if (mode === "reference") value.result.requirements[0].workflow = { ...value.result.requirements[0].workflow, repository_id: "12" };
    expect(validPRCI(value, item), mode).toBe(false);
  }
});

it("recomputes current workflow lifecycle before accepting retained failure proof", () => {
  const value = fixture();
  value.workflow_runs[0].native_status = "IN_PROGRESS";
  Object.assign(value.workflow_runs[0], { native_conclusion: undefined });
  expect(validPRCI(value, item)).toBe(false);
  value.result.state = value.result.requirements[0].state = "pending";
  expect(validPRCI(value, item)).toBe(true);
  value.workflow_runs[0].native_status = "COMPLETED";
  value.workflow_runs[0].native_conclusion = "SUCCESS";
  expect(validPRCI(value, item)).toBe(false);
  value.result.state = value.result.requirements[0].state = "unknown";
  value.result.reason = value.result.requirements[0].reason = "workflow-unverified";
  value.result.requirements[0].result_node_ids = [];
  expect(validPRCI(value, item)).toBe(true);
});

it("rejects a stale aggregate state or reason after validating workflow rows", () => {
  for (const state of ["pending", "unknown"]) {
    const value = fixture();
    value.workflow_runs[0].native_status = state === "pending" ? "IN_PROGRESS" : "COMPLETED";
    Object.assign(value.workflow_runs[0], { native_conclusion: state === "pending" ? undefined : "SUCCESS" });
    value.result.requirements[0].state = state;
    value.result.requirements[0].reason = state === "pending" ? "observed" : "workflow-unverified";
    if (state === "unknown") value.result.requirements[0].result_node_ids = [];
    expect(validPRCI(value, item), state).toBe(false);
    value.result.state = state;
    value.result.reason = value.result.requirements[0].reason;
    expect(validPRCI(value, item), state).toBe(true);
    value.result.reason = "no-matching-result";
    expect(validPRCI(value, item), state).toBe(false);
  }
});

it("includes unsupported rule authority when recomputing the complete aggregate", () => {
  for (const mode of ["parameters", "source", "future-rule"]) {
    const value = fixture();
    if (mode === "parameters") Object.assign(value.rules.rules[0].required_workflows, { unknown_parameters: true });
    if (mode === "source") Object.assign(value.rules.rules[0], { source_kind: "unknown", native_source_kind: "Future" });
    if (mode === "future-rule") Object.assign(value.rules.rules[0], { type: "code_scanning", required_workflows: undefined });
    if (mode === "parameters") {
      value.result.requirements[0].state = "unknown";
      value.result.requirements[0].reason = "workflow-unverified";
      value.result.requirements[0].result_node_ids = [];
    }
    if (mode === "future-rule") value.result.requirements = [];
    expect(validPRCI(value, item), mode).toBe(false);
    value.result.state = "unknown";
    value.result.reason = "unsupported-rule";
    expect(validPRCI(value, item), mode).toBe(true);
  }
});

it("cannot replace an evaluable commit with an unexplained unknown headline", () => {
  const value = fixture();
  const unknown = { ...value, result: { source: "unknown", state: "unknown", reason: "commit-unverified", requirements: [] } };
  expect(validPRCI(unknown, item)).toBe(false);
  expect(validPRCI({ ...unknown, in_merge_queue: true }, item)).toBe(true);
  expect(validPRCI({ ...unknown, result: { ...unknown.result, reason: "closed-pr" } }, { ...item, state: "closed" })).toBe(true);
});

it("rejects terminal aggregate proof with pending or unknown attributed jobs", () => {
  for (const status of ["IN_PROGRESS", "UNRECOGNIZED"]) {
    const value = fixture();
    Object.assign(value.workflow_runs[0].jobs[0], { native_status: status, native_conclusion: undefined });
    Object.assign(value.test_merge.contexts[0], { native_status: status, native_conclusion: undefined });
    expect(validPRCI(value, item)).toBe(false);
    value.result.state = value.result.requirements[0].state = "unknown";
    value.result.reason = value.result.requirements[0].reason = "workflow-unverified";
    value.result.requirements[0].result_node_ids = [];
    expect(validPRCI(value, item)).toBe(true);
  }
});

it("preserves missing source SHA as Unknown and shows its limitation", () => {
  const value = fixture();
  delete value.rules.rules[0].required_workflows.workflows[0].sha;

  value.result.state = "unknown";
  value.result.reason = "workflow-unverified";
  value.result.requirements[0].state = "unknown";
  value.result.requirements[0].reason = "workflow-unverified";
  value.result.requirements[0].result_node_ids = [];
  expect(validPRCI({ ...value, workflow_runs: undefined }, item)).toBe(true);
  expect(validPRRules(value.rules, item)).toBe(true);
  render(<PRRules value={value.rules} />);
  expect(screen.getByText(/No immutable source SHA/)).toBeTruthy();
});

it("rejects workflow rule cross-family payloads and duplicate source references", () => {
  const value = fixture();
  const mixed = { ...value.rules, rules: [{ ...value.rules.rules[0], required_checks: { strict: false, checks: [] } }] };
  expect(validPRRules(mixed, item)).toBe(false);
  value.rules.rules[0].required_workflows.workflows.push(value.rules.rules[0].required_workflows.workflows[0]);
  expect(validPRRules(value.rules, item)).toBe(false);
});
