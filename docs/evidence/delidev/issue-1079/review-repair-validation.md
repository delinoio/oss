# Issue #1079: PR #1165 review repairs

## Revision and scope

This maintenance pass starts from PR #1165 head
`e40de0ad7757d1d02765172b945610f31716b207` and retains main
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7` as its base. The implementation
revision for the final focused checks is
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

## Limits

All new fixtures use isolated temporary state and controlled local files/Git
stores. They do not establish real Windows/Linux native runtime acceptance,
hosted-account acceptance, desktop storage management or broader issue #964
completion. The initial replacement's full-suite failures and unavailable final
exit status remain recorded in [the replacement evidence](README.md).
