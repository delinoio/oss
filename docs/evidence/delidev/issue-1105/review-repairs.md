# ALLGREEN queue review repairs

Date: 2026-09-30. PR: https://github.com/delinoio/oss/pull/1168.
The repair starts from `c74aacb68d86462f8b24fa064e7f6c25e396e32d` in a
replacement isolated worktree because the previously recorded checkout was
absent. Original validation and its limits remain in `validation.md`.

## Queue entry after the final rules read

Review thread: `PRRT_kwDORRAKg86nbIcA`.

The complete CI inventory is now rechecked after the final rules read for every
PR, including one that was initially unqueued. An entry into the queue rejects
the earlier PR-head/test-merge observation before publishing failure evidence.
The contract and scoped domain instructions now state this boundary explicitly.

- Before the fix, `go test -race ./cmds/delidev-cli/internal/integrations/github -run '^TestCIQueryRejectsMergeQueueEntryAfterFinalRulesRead$' -count=1`
  failed: only two inventory reads occurred and stale head failure evidence was
  returned. Log: `/private/tmp/issue-1105-membership-before.log`.
- After the fix, `go test -race ./cmds/delidev-cli/internal/integrations/github -count=1`
  passed the complete GitHub adapter package in 2.507s, including the new
  transition regression and existing queue pagination/provenance cases.
  Log: `/private/tmp/issue-1105-membership-after.log`.

These are controlled fixture results, not live queued-account acceptance.

## Replacement queue identity with an unchanged native result

Review thread: `PRRT_kwDORRAKg86nbIcF`.

CI storage identity now includes the evaluated source and original queue/entry
nodes alongside the check node and unchanged native-result content version. A
replacement queue or entry creates its own current unhandled problem and proof.
The original remains historical with its dismissal and proof unchanged. Legacy
plain-node indexes stay readable and deduplicate only within their original
source/queue identity; existing non-queue proofs remain usable. No schema or wire
migration is introduced.

- Before the fix, `go test -race ./cmds/delidev-cli/internal/store -run '^TestQueueCIFailureSeparatesReplacementIdentityWithReusedNativeResult$' -count=1`
  failed all four replacement entry/queue and legacy-index cases: history retained
  only the old problem. Log: `/private/tmp/issue-1105-queue-identity-before.log`.
- After the fix, `go test -race ./cmds/delidev-cli/internal/store -run 'TestQueueCI|TestPRCIRetains|TestPRConflictHistory|TestPRProblemV18Migration' -count=1`
  passed in 9.338s. Cases cover repeated identical entries, removal, replacement
  entry/queue with the same commit/check result, original proof/dismissal,
  restart, legacy queue and non-queue indexes, and unchanged migration fixtures.
  Log: `/private/tmp/issue-1105-queue-identity-after.log`.
- `go vet ./cmds/delidev-cli/...`, `pnpm proto:lint` and `pnpm proto:fresh` passed.
  Protobuf generation reproduced the committed sources without drift.

## Complete validation of the repaired source

Source: `a1d486adcfa5ee5af27cd814c704218e8c3e8134`.

`GOMAXPROCS=2 go test -race -p 2 -timeout=20m ./cmds/delidev-cli/...` completed
with exit code 1. The complete domain (1.880s), GitHub adapter (2.474s), store
(339.794s), Claude (197.193s), Codex (185.324s), Grok (1,154.982s), OpenCode
(100.697s) and other reported packages passed. No package hit the twenty-minute
budget in this run. Four packages failed:

- CLI: `TestCLISessionAcceptanceQueueAndArchive` could not obtain the owning
  Worker's workspace file reader. The package completed in 184.322s. The earlier
  untouched-base control failed this test at a different preparation wait;
  that control does not establish the cause of the current failure.
- Server: `TestGrokFirstDispatchRetainsUnsupportedSelectionsWithoutClaiming/repository`
  failed its preparation with `Unavailable` / an operation timeout. The package
  completed in 1,145.966s.
- Worker: `TestStreamTerminationCancelsRunningOwnedWork` failed the
  `permission_denied` and `code_0` five-second termination waits. The test canceled
  and joined its watcher before reporting these failures. The package completed
  in 539.029s.
- Workspace: `TestWorkspaceDiffUnbornAndBoundedResults` and
  `TestPRWorkspaceMatchPreservesMismatchesAndDistinguishesUnknownAccess` rejected
  accepted-preparation proof; `TestPRWorkspaceMatchReadsCurrentHeadWithoutTakingExecutionOwnership/local`
  timed out. The package completed in 1,069.875s.

The complete log is `/private/tmp/issue-1105-maintenance-go-race.log`. These
failures remain unresolved; the full command is not reported as passing, and no
unrelated native lifecycle or workspace implementation was changed to hide them.

Repository hook preparation and required Go embed builds completed before
commits. All fourteen LFS assets were hydrated from verified objects. Generated
repository-owned `dist` output was removed after validation. No new frontend,
Rust, protobuf or migration source changes were introduced by these repairs.
Live queued-account, native product/platform and release acceptance remain
unperformed.

## Integration with the latest main branch

Merged main at `82020a7ef2916534f3342aa5208f43d2a051b052` after it advanced during
the complete race run. The domain instruction conflict retained both the
ALLGREEN evidence boundary and incoming Claude continuation requirements.
Incoming PR activity publication remains attached to new problem creation,
including the separate queue identities introduced by this repair.

Validation of the merge result:

- `go test -race ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/integrations/github ./cmds/delidev-cli/internal/store -run 'CI|Queue|MergeQueue|PRActivity|PRProblem|PRRemediation' -count=1`
  passed all selected domain, GitHub and store cases; the store completed in
  149.588s. Log: `/private/tmp/issue-1105-merge-go.log`.
- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli -run 'TestPRProblem|TestCLIPRProblem|TestPRRemediationHistory|Test.*PRActivity' -count=1`
  passed selected server and CLI cases, including the incoming PR activity
  integration. Log: `/private/tmp/issue-1105-merge-rpc.log`.
- `go vet ./cmds/delidev-cli/...`, `pnpm proto:lint`, `pnpm proto:fresh`, the
  DeliDev API client build and desktop `pnpm typecheck` passed. Generated sources
  did not drift from the merged index.
- Desktop `pnpm exec vitest run src/github-ci.test.tsx src/pr-problems.test.tsx`
  passed 17 tests in two files. The three DeliDev structure checks passed.
- Required DevHud administrator and async-commit-hook embedded builds passed
  before the merge commit's Go formatting hook.

These focused checks validate the merged queue/history/activity boundaries.
The complete race result above belongs to `a1d486adcfa5ee5af27cd814c704218e8c3e8134`,
not this merge result; its unresolved failures and the original frontend suite
limits remain recorded. No full-suite success or live queue acceptance is
inferred from the merge checks.
