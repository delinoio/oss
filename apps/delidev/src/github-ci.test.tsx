import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { PRCI, validPRCI } from "./github-ci";

const item = { state: "open", merged: false, base_ref: "main", base_sha: "a".repeat(40), head_sha: "b".repeat(40) };
import { ciObservation, queueCIObservation } from "./github-ci-fixture";

it("shows required failure with exact rule, App and evaluated commit provenance", () => {
  const value = ciObservation();
  expect(validPRCI(value, item)).toBe(true);
  render(<PRCI value={value} />);
  expect(screen.getByRole("status").textContent).toBe("Terminal required CI failure");
  expect(screen.getByText(item.head_sha)).toBeTruthy();
  expect(screen.getByRole("table", { name: "Active ruleset CI requirements" })).toBeTruthy();
  expect(screen.getByText(/ruleset 9007199254740993/)).toBeTruthy();
  expect(screen.queryAllByRole("link")).toHaveLength(0);
});
it("rejects partial contexts, foreign sources, optional references and duplicate IDs", () => {
  const partial = ciObservation(); partial.head.total_count = "2";
  expect(validPRCI(partial, item)).toBe(false);
  const foreign = ciObservation(); foreign.result.evaluated_sha = "f".repeat(40);
  expect(validPRCI(foreign, item)).toBe(false);
  const optional = ciObservation(); optional.head.contexts[0].required = false;
  expect(validPRCI(optional, item)).toBe(false);
  const duplicate = ciObservation(); duplicate.result.requirements[0].result_node_ids.push("CHECK_1");
  expect(validPRCI(duplicate, item)).toBe(false);
  const mixed = ciObservation(); mixed.in_merge_queue = true;
  expect(validPRCI(mixed, item)).toBe(false);
});
it("keeps an unknown evaluated commit explicit without a required-failure table", () => {
  const value = ciObservation();
  const unknown = { ...value, native_mergeability: "UNKNOWN", test_merge: undefined, result: { source: "unknown", state: "unknown", reason: "commit-unverified", requirements: [] } };
  expect(validPRCI(unknown, item)).toBe(true);
  render(<PRCI value={unknown} />);
  expect(screen.getByRole("status").textContent).toBe("Unknown");
  expect(screen.queryByRole("table")).toBeNull();
  expect(screen.getByText(/evaluated commit could not be verified/)).toBeTruthy();
});

it("renders original CI output inertly and labels the workflow attempt as aggregate evidence", () => {
  const value = ciObservation();
  const view = render(<PRCI value={value} />);
  expect(screen.getByText("<script>inert output</script>")).toBeTruthy();
  expect(view.container.querySelector("script")).toBeNull();
  expect(screen.getByText(/observed workflow attempt 2/)).toBeTruthy();
  expect(screen.getByText(/does not prove that each retained check ran again/)).toBeTruthy();
  expect(screen.getByText("2026-09-28T00:01:00Z")).toBeTruthy();
});

it("rejects missing or mixed lifecycle proof, impossible attempts and oversized original output", () => {
  const noEvidence = ciObservation();
  const { evidence: _evidence, ...row } = noEvidence.head.contexts[0];
  expect(validPRCI({ ...noEvidence, head: { ...noEvidence.head, contexts: [row] } }, item)).toBe(false);
  const invalid = ciObservation(); invalid.head.contexts[0].evidence.workflow.observed_attempt = "2147483648";
  expect(validPRCI(invalid, item)).toBe(false);
  const backwards = ciObservation(); backwards.head.contexts[0].evidence.completed_at = "2026-09-27T00:00:00Z";
  expect(validPRCI(backwards, item)).toBe(false);
  const large = ciObservation(); large.head.contexts[0].evidence.summary = "x".repeat((64 << 10) + 1);
  expect(validPRCI(large, item)).toBe(false);
  const mixed = ciObservation();
  expect(validPRCI({ ...mixed, head: { ...mixed.head, contexts: [{ ...mixed.head.contexts[0], evidence: { ...mixed.head.contexts[0].evidence, description: "status-only evidence" } }] } }, item)).toBe(false);
});

it("shows ALLGREEN queue commit evidence and keeps queue state independent of failed checks", () => {
  const value = queueCIObservation(), queueItem = { ...item, node_id: "ITEM_stable", number: "17", repository_node_id: "R_37" };
  expect(validPRCI(value, queueItem)).toBe(true);
  render(<PRCI value={value} />);
  expect(screen.getByText(value.merge_queue.entry.head_sha)).toBeTruthy();
  expect(screen.getByText(/entry ENTRY_E.*strategy ALLGREEN/)).toBeTruthy();
  expect(screen.getByText(/UNMERGEABLE does not establish a failed check/)).toBeTruthy();
  expect(screen.getByText(/merge_group/)).toBeTruthy();
  expect(screen.queryByText(item.head_sha)).toBeNull();
});

it("rejects mixed queue membership, foreign commits, HEADGREEN and unverified original workflows", () => {
  const queueItem = { ...item, node_id: "ITEM_stable", number: "17", repository_node_id: "R_37" };
  for (const mode of ["HEADGREEN", "entry", "repository", "position", "partial", "head", "event", "app", "workflow", "source"]) {
    const value = queueCIObservation(), queue = value.merge_queue, run = queue.rollup.contexts[0];
    if (mode === "HEADGREEN") queue.strategy = "HEADGREEN";
    if (mode === "entry") queue.entry.pull_request_node_id = "FOREIGN";
    if (mode === "repository") queue.repository_node_id = "FOREIGN";
    if (mode === "position") queue.entry = { ...queue.entry, position: "2" };
    if (mode === "partial") queue.total_count = "2";
    if (mode === "head") run.commit_sha = item.head_sha;
    if (mode === "event") run.workflow_event = "pull_request";
    if (mode === "app") run.application.id = "99";
    if (mode === "workflow") run.evidence.workflow.commit_sha = item.head_sha;
    if (mode === "source") value.result.source = "head";
    expect(validPRCI(value, queueItem), mode).toBe(false);
  }
});

it("preserves an unknown queue strategy without presenting historical head failures as current", () => {
  const value = queueCIObservation();
  const unknown = { ...value, merge_queue: { ...value.merge_queue, strategy: "HEADGREEN" }, result: { source: "unknown", state: "unknown", reason: "commit-unverified", requirements: [] } };
  expect(validPRCI(unknown, { ...item, node_id: "ITEM_stable", number: "17" })).toBe(true);
  render(<PRCI value={unknown} />);
  expect(screen.getByRole("status").textContent).toBe("Unknown");
  expect(screen.queryByText(/Inspected check/)).toBeNull();
});
