# Issue #1079 PR #1231 review repairs

## Source and repair scope

The maintenance pass started from clean PR head
`b14e7d657d861677e675b26054e952a3ef4bbf4d` on 2026-09-30.
The PR was open, non-draft and mergeable, with three unresolved Codex threads
and one failed Windows Worker CI shard. Each independent repair has its own commit:

- `5aacf19f1fff7009e1d4490f6a67259c1652c406`: restore recovery requires a
  synchronized original operation/snapshot/hash-bound publication proof written
  only after the successful no-replace rename. Pending bindings and matching
  foreign bytes cannot grant recovery, execution identity or deletion authority.
  Missing proof preserves uncertainty without replay. This addresses
  [the restoration finding](https://github.com/delinoio/oss/pull/1231#discussion_r4144477363).
- `c0b80d08b302b8adb309d8ccfaf2e241011c7b68`: snapshot walks compare opened and
  named root identity, mode, size and modification time around enumeration and
  copying, matching the existing nested-directory checks. This addresses
  [the root mutation finding](https://github.com/delinoio/oss/pull/1231#discussion_r4144477372).
- `2b857afea54daa5088cdaa1dbb9a56f69330d710`: server validation accepts only
  canonical lowercase hexadecimal SHA-256 for preview and snapshot reports.
  Malformed owning-Worker reports retain uncertainty without publishing unusable
  metadata or stored availability; explicit recovery can still read the original
  local copy. This addresses
  [the reported digest finding](https://github.com/delinoio/oss/pull/1231#discussion_r4144477381).
- `54f0b5a3d98b3c01a38c43960934a9a74d5aec42`: restored Git identity checks use
  the same offline/read-only profile as snapshot inspection, including the
  command-local Windows `core.longpaths=true` opt-in. Redacted failures expose
  closed head/branch/admin-directory/ownership phases, IDs and stable codes only.
  This repairs the profile gap identified while investigating
  [the failed Windows Worker shard](https://github.com/delinoio/oss/actions/runs/36714035654/job/109882364633).

That CI shard failed `TestSessionDeletionIncludesStoredAndRestoredSnapshots/worktree/restore`
at `session_deletion_storage_test.go:48` with recovery-required workspace identity.
Its workspace package passed separately. The earlier log has no identity phase,
so command-profile inspection establishes the long-path gap rather than a
conclusive native diagnosis. Native Windows confirmation remains with the next CI
run; local passing tests and cross-compilation cannot substitute for it.

## Focused executed verification

All commands ran from the repository module root; Go commands used
`GOMAXPROCS=2` on macOS arm64 with Go 1.26.8:

- `go test -race -p 2 -timeout=15m ./cmds/delidev-cli/internal/workspace
  ./cmds/delidev-cli/internal/worker
  -run 'SnapshotRestoreRecovery|SnapshotRecoveryInspectsLostCompletion|SnapshotFailedRestoreCleansOwnedStaging|SessionDeletionIncludesStoredAndRestoredSnapshots'
  -count=1` passed (workspace 6.087s; Worker 69.574s). New regressions reject an
  identical foreign destination after scratch cleanup, missing/pending/wrong
  operation/snapshot/hash proof, and pending-binding execution/deletion authority.
- `go test -race -p 2 -timeout=10m ./cmds/delidev-cli/internal/workspace
  -run 'SnapshotWalkRejectsRootMutation|SnapshotCleanup.*(Change|Race)|Snapshot.*Claim|SnapshotCancellationAfterPublication'
  -count=1` passed (2.622s). Root entry, mode and modification-time mutations are
  injected after enumeration for both inspection and copying; Unix permission
  mutation is explicitly platform-scoped.
- `go test -race -p 2 -timeout=10m ./cmds/delidev-cli/internal/server
  -run WorkspaceStorage -count=1` passed (8.572s). Authenticated report tests cover
  uppercase, non-hexadecimal and embedded-space digests for preview, create and
  cleanup, then complete explicit recovery using valid original evidence.
- `go test -race -p 2 -timeout=15m ./cmds/delidev-cli/internal/workspace
  ./cmds/delidev-cli/internal/worker
  -run 'SnapshotRestoredGitIdentityAtLongPrivatePath|SnapshotGitAtLongPrivatePath|SessionDeletionIncludesStoredAndRestoredSnapshots|SnapshotRestoresEveryRepository'
  -count=1` passed (workspace 18.082s; Worker 33.560s).
  The last pattern does not select the faithful-restoration test; the following
  final fixture check selects its actual name.
- `go test -race -p 2 -timeout=15m ./cmds/delidev-cli/internal/workspace
  -run 'SnapshotRestoredGitIdentityAtLongPrivatePath|WorkspaceSnapshotFaithfullyRestoresEveryRepository'
  -count=1` passed (43.005s), including the simplified bounded temporary-path
  fixture. Git object paths exceed 280 characters while process cwd stays within
  250; source `core.longpaths=false` remains unchanged. Restore, lost-report
  recovery and coordinated-deletion identity all validate.
- `GOOS=windows GOARCH=amd64 go test -c -o <temporary-test-binary>
  ./cmds/delidev-cli/internal/workspace` passed. This is cross-compilation only.
- `go vet -p 2 ./cmds/delidev-cli/...` passed.
- `go build -p 2 -o <temporary-cli-binary> ./cmds/delidev-cli` passed.
- Root `pnpm ci:contracts` passed all 113 tests.

The final two-repository/long-path verification and Windows cross-compilation
include the last test-fixture edit. The earlier paired workspace/Worker focused
run began before that fixture-only edit; the final command independently verifies
the simplified bounded construction.

## Full race result for the repaired source

The required full race command runs from this checkout's module root against
`89cedbb70e40b9dfeb5986235a42d5b5f4aee5c5`, whose code includes all four repairs:

```sh
GOMAXPROCS=2 go test -race -p 2 -timeout=30m ./cmds/delidev-cli/...
```

The command completed with exit status 1. Every package except workspace passed,
including CLI (156.566s), Grok (932.884s), server (555.419s), store (166.610s)
and Worker (369.724s). Workspace ran its complete suite (837.060s) and reported
one failed case: `TestWorkspaceDiffUnbornAndBoundedResults` at `diff_test.go:163`,
with recovery-required identity during the working-tree diff of an unborn Local
repository (21.77s test time). No other failing test or race-detector warning was
reported. The added storage, restoration, root-mutation and digest regressions
did not report failures; this is not full workspace/package success.

The exact isolated command, `GOMAXPROCS=2 go test -race -p 2 -timeout=10m
./cmds/delidev-cli/internal/workspace
-run '^TestWorkspaceDiffUnbornAndBoundedResults$' -count=1`, then passed
(21.858s). Neither its source nor its sixteen-second per-read deadline changed.
The diff implementation/test match inspected main
`d1f83cecee4e0c50ea094335392cf68845f85739`. Inspection shows that this Local
fixture has no restoration binding and does not enter the new restored-Git
profile, snapshot walk or report validation. No independently reproduced main
failure or conclusive cause is claimed. The isolated pass does not erase the
failed complete run; its unresolved full-suite limitation remains visible.

The completed source-`46452512` run is recorded independently in
`source-46452512-completed-race.md` and does not validate these later repairs.

## Limits

No frontend or protocol source changed in this pass. Their prior full validation
belongs to the source revisions in the preceding records. New tests use isolated
temporary repositories/state and controlled local children. No real account,
credential, Git push from product storage, native Windows/Linux acceptance or
release publication is established. The older failed checks and revision limits
remain intact. Repository-owned generated `dist` output is not retained.
