# Preferences and Agent presentation main merge validation

## Source and revision

- Date: 2026-09-30.
- Issue: https://github.com/delinoio/oss/issues/1136.
- PR: https://github.com/delinoio/oss/pull/1172.
- First-parent revision: `d1fae9e36865bd67a70a2e4f1e6231fd7cc2269a`.
- Merged main: `c49b08027e5b6679cd0fd01066544ac1a72d4a0e`.

This heartbeat observed an open, conflicting PR, one passing current-head check and no failing checks. Review/thread/comment/reaction inventories were complete without additional pages; both prior bot threads remain resolved and outdated. The latest head's Codex code/security reviews were running, and the bot's earlier [repository review-quota notice](https://github.com/delinoio/oss/pull/1172#issuecomment-5907022565) remained visible. Neither old reviewed revisions nor a running review prove current-head acceptance.

Main added verified settled Claude failure Resume (#1166), Windows shard precompilation (#1207) and Agent Workers category presentation (#1186). The sole textual conflict is the preferences integration test: both branches independently use exactly five seconds for the two post-save lookups. The resolution retains this PR's shared budget and explanatory scope/replacement comment, with no change to those waits, the test limit or singleton/default/document/ID/revision assertions.

Inspection confirms all 13 incoming Go files, Agent category CSS and CI runner source match the pinned merge parent exactly. The Settings component differs from that parent only in the existing Runner Devices category/help copy; Agent and Projects presentation, opening disposal and schema/action gates remain intact. Existing Runs on and schedule Runner Device labels and technical/stored identifiers retain their meanings. No new issue-owned behavior or policy change is introduced by the resolution.

## Focused verification

- `pnpm exec vitest run src/settings.test.tsx src/settings-projects.test.tsx src/settings-lifetime.test.tsx --maxWorkers=1`: all 80 tests in three files passed.
- `node --test scripts/ci/go-test.test.mjs scripts/ci/ci-contract.test.mjs scripts/ci/plan.test.mjs`: all 58 tests passed.
- Required administrator/async-commit-hook embeds and the generated DeliDev client were built before compilation. `GOCACHE=<task-owned isolated cache> GOMAXPROCS=2 go build -p 2 -o <temporary validation binary> ./cmds/delidev-cli` passed.

## Completed validation and limits

The complete `GOCACHE=<task-owned isolated cache> GOMAXPROCS=2 pnpm test` from `apps/delidev` passed: all 1,146 tests in 89 files, eight packaging fixtures, sixteen asset/desktop-launch fixtures, widget checks and the production build. It includes the equivalent-wait preferences fixture and the new Agent presentation coverage. Original tracked Vitest and Testing Library configuration stayed unchanged, with no global wait, worker-count or runner-timeout override.

The targeted Go command passed in all four packages:

```sh
GOCACHE=<task-owned isolated cache> GOMAXPROCS=2 go test -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/harness/claude \
  ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/server \
  -run 'TestClaudeFailedContinuationRequiresCorrelatedNonAbortedSettledInput|TestClaudeQuestionContinuationRequiresOriginalAcceptedResponse|TestClaudeToolContinuationRetainsOnlyOriginalAcceptedPermission|TestClaudeRecoveryRequiresExplicitOriginalComparison|TestFailedCheckpointRejectsChangedSettingsWithoutNativeReplay|TestClaudeFailedCheckpointStillRequiresOriginalNativeEOFProof|TestClaudeFailedResumeClaimsNextFIFOInputOnce|TestClaudeLostFailedReportRecoveryPreservesOriginalFailure|TestPRRemediationWorkspacePlanUsesCurrentExplicitSelectionWithoutDispatch|TestScheduleRPCReferencedDeletionDisablesAtomically' -count=1
```

`GOCACHE=<task-owned isolated cache> GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...` passed. `git lfs fsck`, worktree/staged diff checks and unchanged test-configuration checks passed.

Generated repository-owned `dist` output and the temporary CLI binary are removed after the Go formatting hook and before the final clean-worktree check. The task-owned isolated Go build cache remains outside the repository for later heartbeat reuse. This is fixture/build evidence; it does not replace the earlier incomplete broad local Go gate or establish native/account/platform acceptance. CI and review evidence for a new pushed head must be collected on a later heartbeat.
