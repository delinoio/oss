# Issue #1079: PR #1165 review repairs

## Revision and scope

This maintenance pass starts from PR #1165 head
`e40de0ad7757d1d02765172b945610f31716b207` with main
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7` as its initial base. The implementation
revision for the pre-merge focused checks is
`7e16523905230dfe76e1e342a6b48c7455a0aa25`. No public wire schema, reserved
workspace-storage capability number 11, or executable database schema 24 changes
in this pass. Changes remain within Worker-local snapshot/recovery, Worker
report acknowledgment, server result validation and their owning contracts.

## Review findings

| Finding | Resulting behavior | Repair commits |
| --- | --- | --- |
| Failed restore retains incomplete scratch | Clean only staging created by that original operation, independently of caller cancellation; unconfirmed cleanup remains uncertain. | `505adfb0` |
| Reported removal retains a large private inventory forever | Retire original removal evidence only after matching terminal server acknowledgment and a durable reported Worker journal. A small private retirement receipt retries interrupted retirement at startup. Failed/canceled direct reports and successful recovery of unpublished cleanup have explicit safe terminal paths; uncertain reports and failed recovery preserve predecessor evidence. | `46ff9539`, `dc818758` |
| Nested Git directories hide undeclared external stores | Reject undeclared nested Git administration in every entry kind, including filesystem case aliases, before preview/create/cleanup. Declared repositories retain their independent closure validation. | `0378e0af`, `38a91fbc` |
| Absent original and claimed names fabricate removal success | Require original synchronized intent plus an independently synchronized claim bound to its exact digest, retained after private-namespace inventory verification and before unlink. A pre-transition intent alone cannot prove removal. Existing claimed subsets can be verified and finished during explicit recovery. Retire claim before intent after acknowledgment. | `5cefbe34`, `7e165239` |
| Failed recovery can bypass action/artifact checks | Validate every recovery outcome against the original action, previous availability, zero removed bytes on failure, and required exact retained snapshot identity/digest/non-deleted metadata. Malformed reports retain job/session uncertainty. | `1dca2337` |
| A fresh inventory can adopt uncaptured concurrent writes into cleanup | Pin complete source inventory in the verified snapshot, bind removal to that proof, compare after whole-root claim, and restore the original name without replacement before unlink on mismatch. Recheck cancellation after intent persistence before namespace claim. | `78fd6b56`, `dc818758` |
| A maximum-sized workspace cannot delete its snapshot wrappers | Reserve exactly two wrapper entries beyond the 8,192 workspace entries, validate the wrapper structure and pinned content, and retain strict private JSON validation with the existing explicit 8 MiB manifest bound. Public JSON remains capped at 1 MiB. | `370a41f9` |

`c32bb37c` separately fixes new acknowledgment test fixtures to clone Protobuf
messages instead of copying their internal mutex. Repository hooks ran for every
repair commit without bypassing verification.

## Executed checks

- Individual race regressions passed for every listed repair before its commit.
  The actual 8,192-entry snapshot create/inspect/delete/lost-completion test
  passed (workspace package 126.405s). Strict private JSON bounds and malformed
  JSON regressions passed (domain package 3.514s).
- `GOMAXPROCS=2 go test -race -p 2 -timeout=5m
  ./cmds/delidev-cli/internal/workspace ./cmds/delidev-cli/internal/worker
  -run 'TestSnapshotAbsentRemoval|TestSnapshotRemovalClaim|TestSnapshotRecovery|TestStorageRemovalRetires|TestStorageRetirement|TestStorageJournal'
  -count=1` passed at `7e165239` (workspace 2.788s; Worker 2.784s). This covers
  pre-transition external loss, changed claim identity/digest, positively verified
  lost completion and partial removal, uncertain/report acknowledgment retention,
  interrupted retirement startup and unpublished/failed terminal reports.
- `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...` passed after the Protobuf
  fixture correction and again at `7e165239`.
- `GOMAXPROCS=2 go test -race -p 2 -timeout=15m
  ./cmds/delidev-cli/internal/workspace ./cmds/delidev-cli/internal/server
  ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/domain
  -run 'Snapshot|WorkspaceStorage|StorageRemoval|StorageRetirement|StorageJournal|DecodeBounded'`
  passed at `7e165239` (workspace 335.195s; server 12.551s; Worker 6.495s;
  domain 1.481s). The separate exact
  `TestPrivateJSONBoundDoesNotExpandPublicDocuments` race regression also passed
  at that revision (1.431s); the broader name filter does not select that test.
- `pnpm ci:contracts` passed all 102 tests at `7e165239` (9.196s).
- `GOOS=windows GOARCH=amd64 GOMAXPROCS=2 go test -p 2 -c
  ./cmds/delidev-cli/internal/workspace -o /private/tmp/issue-1079-workspace-windows.test.exe`
  and the corresponding Linux arm64 command passed at `7e165239`. These are
  compilation checks, not native runtime acceptance. Binaries remain outside the
  repository.
- No repository-owned generated `dist` directories remain in the maintenance
  worktree; the scan excludes dependency and Git metadata directories.
- `git lfs fsck` passed in the maintenance worktree at `7e165239`.

GitHub CI was successful for the previous published head `e40de0ad`, including
Go Quality and Go Test on Ubuntu, macOS and Windows. That result does not validate
the new repair commits. Newly pushed CI and review remain separate observations.

## Full-run limitations

The separate fixed-source `GOMAXPROCS=2 go test -race -timeout=15m
./cmds/delidev-cli/internal/workspace` run used the implementation at `7e165239`
and exited 1 (902.749s). It reports these existing read/match failures:

- `TestWorkspaceDiffUnbornAndBoundedResults`: `diff_test.go:163` reports
  `recovery_required` during the working-tree comparison (24.41s).
- `TestPRWorkspaceMatchReadsCurrentHeadWithoutTakingExecutionOwnership/local`:
  `pr_match_test.go:62` reports a timed-out native head observation (29.57s;
  parent test 54.30s).
- `TestPRWorkspaceMatchPreservesMismatchesAndDistinguishesUnknownAccess`:
  `pr_match_test.go:152` reports `recovery_required` (45.77s).

The package then reaches its cumulative 15-minute limit while
`TestPRPreparationRejectsChangedRemoteWithoutAlteringOriginalCheckout/fail` is
running (parent 32s; subtest 1s). The read/match implementation and tests have no
diff from main `74701b89`; `prepare.go` differs only by the snapshot test-injection
fields on Manager. These failures have not been independently reproduced on
main. The full package is not claimed as passing.

The three named tests were then rerun together with
`GOMAXPROCS=2 go test -race -timeout=5m ./cmds/delidev-cli/internal/workspace
-run '^(TestWorkspaceDiffUnbornAndBoundedResults|TestPRWorkspaceMatchReadsCurrentHeadWithoutTakingExecutionOwnership|TestPRWorkspaceMatchPreservesMismatchesAndDistinguishesUnknownAccess)$'
-count=1`: the package exited 1 after 129.895s, with the diff test again failing
at line 163 (27.42s); both PR-match tests passed. The exact diff test alone,
`GOMAXPROCS=2 go test -race -timeout=3m ./cmds/delidev-cli/internal/workspace
-run '^TestWorkspaceDiffUnbornAndBoundedResults$' -count=1`, passed on both
the source-only main archive (38.355s) and the current implementation (37.135s).
The baseline's go.mod, go.sum, diff.go, diff_test.go and prepare.go bytes were
independently matched to `74701b8948694e2bf8f8ba6d07e596c2d2f358a7`. These
isolated passes do not erase the earlier broader failures or establish their
cause.

A broader `GOMAXPROCS=2 go test -race -p 2 -timeout=15m
./cmds/delidev-cli/...` run began at `38a91fbc` while later repairs were made in
the same checkout. Its build is not attributable to one fixed revision, so it
cannot validate the final implementation. The retained diagnostic log observes
CLI `TestCLISessionAcceptanceQueueAndArchive` failure at `sessions_test.go:239`
(244.555s package time), Grok's cumulative timeout while
`TestReadInputOwnsToolsResponsesAndNoPlainTextHistory/read-missing-response`
is running (901.099s), and the server's cumulative timeout while
`TestScheduleRPCCursorScopeEpochAndRetainedOrder` has just started (901.089s).
It also reports missing verified-claim files in both subcases of
`TestStorageRemovalRetiresOnlyAfterDurableReport` (Worker package 474.292s).
No failure cause is inferred from this non-isolated build.
At 2026-09-30 08:56 UTC the overall command was still running. It subsequently
exited 1, also timing out the workspace package at 900.495s while
`TestRemoteFetchUsesUpdatedCommitAndNeverStaleFallback` was running (5s).
Its diagnostic log is outside the repository. It also
contains successful package outcomes, including Claude, Codex, native-wire,
OpenCode, process, store and user services, which do not validate a single final
revision either.

The exact latter Worker regression was rerun against the fixed implementation
at `7e165239` with `GOMAXPROCS=2 go test -race -timeout=3m
./cmds/delidev-cli/internal/worker -run '^TestStorageRemovalRetiresOnlyAfterDurableReport$'
-count=1` and passed (2.643s), matching its earlier fixed-source focused result.
Neither the broader diagnostic run nor the failed full workspace run is a
complete race-suite success.

## Main merge

The final PR inventory found a new main conflict after the review fixes.
Main `82020a7ef2916534f3342aa5208f43d2a051b052` was fetched and merged without
rebasing. Conflicts were limited to domain/server/Worker AGENTS.md append regions:
the snapshot/strict-JSON/removal-retirement policies and main's verified settled
Claude-failure policies are independently scoped and both are preserved verbatim.
No code conflict was resolved by choosing a side. Capability 11 and schema 24
remain unchanged.

Post-merge checks completed against the combined source before its merge commit:

- The explicit storage-focused race command across workspace/server/Worker/domain
  passed with `-count=1` and a five-minute package bound (5.693s, 7.543s, 5.592s,
  and 1.729s respectively). It selects General Chat whole-workspace restoration,
  absent intent/claim and changed claim ownership, lost-completion and partial
  removal recovery, every `TestWorkspaceStorage` server regression, all three
  storage journal/retirement Worker regressions, and strict private JSON bounds.
- `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...` passed.
- `pnpm proto:check` passed, including generated-source freshness and compatibility
  against the updated main.
- `pnpm ci:contracts` passed all 113 tests (7.300s).

These post-merge focused checks do not relabel the full-run failures above or
establish complete native/platform acceptance. New CI remains required for the
published repair and merge commits.

## Limits

All new fixtures use isolated temporary state and controlled local files/Git
stores. They do not establish real Windows/Linux native runtime acceptance,
hosted-account acceptance, desktop storage management or broader issue #964
completion. The initial replacement's full-suite failures and unavailable final
exit status remain recorded in [the replacement evidence](README.md).
