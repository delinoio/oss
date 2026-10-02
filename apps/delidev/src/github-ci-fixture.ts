export function ciObservation() {
  return {
    rules: { base_ref: "main", base_sha: "a".repeat(40), head_sha: "b".repeat(40), digest: "c".repeat(64), rules: [{ type: "required_status_checks", ruleset_id: "9007199254740993", source_kind: "repository", native_source_kind: "Repository", source: "fixture-owner/repo", digest: "d".repeat(64), required_checks: { strict: false, checks: [{ context: "CI Result", integration_id: "15368" }] } }] },
    native_mergeability: "MERGEABLE", in_merge_queue: false,
    head: { commit_sha: "b".repeat(40), total_count: "1", contexts: [{ kind: "check-run", node_id: "CHECK_1", name: "CI Result", commit_sha: "b".repeat(40), required: true, native_status: "COMPLETED", native_conclusion: "FAILURE", application: { id: "15368", node_id: "APP_15368", slug: "github-actions" }, workflow_event: "pull_request", evidence: { suite_node_id: "SUITE_1", started_at: "2026-09-28T00:00:00Z", completed_at: "2026-09-28T00:01:00Z", title: "Original result", summary: "<script>inert output</script>", workflow: { node_id: "RUN_1", run_number: "12", observed_attempt: "2", created_at: "2026-09-28T00:00:00Z", updated_at: "2026-09-28T00:01:00Z" } } }] },
    test_merge: { commit_sha: "e".repeat(40), total_count: "0", contexts: [] },
    result: { source: "head", evaluated_sha: "b".repeat(40), state: "terminal-failure", reason: "observed", requirements: [{ ruleset_id: "9007199254740993", context: "CI Result", integration_id: "15368", state: "terminal-failure", reason: "observed", result_node_ids: ["CHECK_1"] }] },
  };
}

export function queueCIObservation() {
  const value = ciObservation(), sha = "f".repeat(40);
  const entry = { node_id: "ENTRY_E", pull_request_node_id: "ITEM_stable", pull_request_number: "17", position: "1", base_sha: "a".repeat(40), head_sha: sha, state: "UNMERGEABLE" };
  const original = value.head.contexts[0];
  const row = { ...original, node_id: "CHECK_G", commit_sha: sha, workflow_event: "merge_group", evidence: { ...original.evidence, workflow: { ...original.evidence.workflow, suite_node_id: original.evidence.suite_node_id, commit_sha: sha } } };
  return { ...value, in_merge_queue: true, merge_queue: { node_id: "QUEUE_Q", repository_node_id: "R_37", strategy: "ALLGREEN", entry, total_count: "1", entries: [entry], rollup: { commit_sha: sha, total_count: "1", contexts: [row] } }, result: { ...value.result, source: "merge-queue", evaluated_sha: sha, requirements: value.result.requirements.map(row => ({ ...row, result_node_ids: ["CHECK_G"] })) } };
}
