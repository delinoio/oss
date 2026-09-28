export function ciObservation() {
  return {
    rules: { base_ref: "main", base_sha: "a".repeat(40), head_sha: "b".repeat(40), digest: "c".repeat(64), rules: [{ type: "required_status_checks", ruleset_id: "9007199254740993", source_kind: "repository", native_source_kind: "Repository", source: "fixture-owner/repo", digest: "d".repeat(64), required_checks: { strict: false, checks: [{ context: "CI Result", integration_id: "15368" }] } }] },
    native_mergeability: "MERGEABLE", in_merge_queue: false,
    head: { commit_sha: "b".repeat(40), total_count: "1", contexts: [{ kind: "check-run", node_id: "CHECK_1", name: "CI Result", commit_sha: "b".repeat(40), required: true, native_status: "COMPLETED", native_conclusion: "FAILURE", application: { id: "15368", node_id: "APP_15368", slug: "github-actions" }, workflow_event: "pull_request" }] },
    test_merge: { commit_sha: "e".repeat(40), total_count: "0", contexts: [] },
    result: { source: "head", evaluated_sha: "b".repeat(40), state: "terminal-failure", reason: "observed", requirements: [{ ruleset_id: "9007199254740993", context: "CI Result", integration_id: "15368", state: "terminal-failure", reason: "observed", result_node_ids: ["CHECK_1"] }] },
  };
}
