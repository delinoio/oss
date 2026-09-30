# PR #1231 restored ownership, unlink and read repairs

This independent record covers the maintenance pass that began on 2026-09-30
from `2f376f4de03024090e5c6adbbb715025220a5f9d`. It preserves prior evidence
files and the historical ledger. The pass merged exact main
`9efb1917e0127a9223cee0969877238ab37c0e1d` in `2f4650fad6ac7e64b7697d3aa630a0450b5e825b`;
[main reconciliation](pr-1231-main-9efb1917.md) records the schema, contract and
frontend composition and its executed checks. A later advance of main is not
part of this source revision. A read-only merge-tree comparison of final source
with observed main `6c749670727b30679e722821846bc8dc00f5ac32` records further
conflicts in `/tmp/delidev-1079-third-main-6c-merge-check.log`. They require a
separate scheduled repair; this pass does not claim readiness against that base.

## Four original Codex findings

Each independent finding has its own repair commit and focused checks. The
thread identifiers below bind this record to the original review; resolution
must follow a successful final push.

### Published restored-directory identity

Commit `3bdff538d` handles `PRRT_kwDORRAKg86nj9DD`. Version-2 restore bindings
retain a digest of native directory identities for the root, repositories and
independent Git stores, captured in verified staging before rename and checked
again afterward. Subsequent continuation, recovery and deletion compare those
identities before accepting the original logical workspace identity. Ordinary
file edits and commits remain allowed. Byte-identical directory replacement
does not inherit ownership; legacy version-1 proofs remain uncertain.

Focused race selection (with the execution bound omitted):

```sh
GOMAXPROCS=2 go test -race -p 2 \
  ./cmds/delidev-cli/internal/workspace \
  -run 'RestoredIdentity|SnapshotRestoreRecovery|WorkspaceSnapshotFaithfullyRestoresEveryRepository' -count=1
```

The initial group passed in 69.103 seconds. After moving identity capture into
verified staging, the final focused group passed in 67.395 seconds. Tests cover
byte-identical Git-store/repository replacement, General Chat root replacement,
legacy proofs and ordinary later commits. Windows amd64 test compilation passed.
Logs: `/tmp/delidev-1079-third-identity.log`,
`/tmp/delidev-1079-third-identity-final.log`,
`/tmp/delidev-1079-third-identity-windows.log`.

### Restored bytes after rename

Commit `0e0008532` handles `PRRT_kwDORRAKg86nj9DO`. The renamed live root must
match the complete pinned snapshot inventory before the publication proof is
synchronized. A changed, added or removed entry preserves the live tree and
pending binding with recovery required. General Chat and real Git worktree
fixtures inject changes between verification and publication; HEAD alone cannot
prove faithful restoration. The original snapshot remains valid.

The focused selection `GOMAXPROCS=2 go test -race -p 2
./cmds/delidev-cli/internal/workspace
-run 'RestoreRechecks|SnapshotRestoreRecovery|GeneralChatSnapshotRestores'
-count=1` passed in 14.621 seconds
(`/tmp/delidev-1079-third-restored-bytes.log`).

### Pinned removal during unlink

Commit `38a11799962a6ebde0b3fdb4ddfdc39aecb39ddf` handles
`PRRT_kwDORRAKg86nj9DT`. Version-2 removal claims pin the native root identity.
Claimed cleanup/deletion and recovery use the closed original intent graph,
verify anchored file bytes/mode/identity and symlink text/type immediately
before unlink, and reject unknown entries. Empty-directory removal fails if a
writer added remaining content. Partial recovery permits missing pinned entries
and owned directory mode changes, without adopting new bytes. Legacy claims do
not authorize new unlink. The original scratch-only remover has separate scope.

Fixtures retain an open directory root across rename and exercise late root or
child additions, replacements, in-place byte changes and final root additions,
for both cleanup and snapshot deletion. Uncaptured bytes remain; removal is not
reported as verified and recovery stays uncertain. These immediate checks and
empty-directory removal do not claim an atomic content-check/unlink primitive.

The focused selection `GOMAXPROCS=2 go test -race -p 2
./cmds/delidev-cli/internal/workspace
-run 'ClaimedRemoval|Snapshot.*Removal|SnapshotMaximumInventory|SnapshotCleanupNever|GeneralChatSnapshot|SnapshotAccounting'
-count=1` passed in 75.712 seconds
(`/tmp/delidev-1079-third-unlink.log`).

### In-flight observations and native cleanup

Commit `2c8adc716` handles `PRRT_kwDORRAKg86nj9DW`. A separate cross-process
per-session observation gate covers file/diff/private PR reads through anchored
handles and native read-child cleanup. Preparation, recovery, storage and
permanent deletion share that gate; execution retains its independent lifetime
lease so views remain available during native runs. Busy admission returns
conflict before workspace effects. New version-2 read indexes bind the original
session before child launch, are reconciled before storage and are included in
deletion absence checks. Unassigned legacy or unknown ownership remains
uncertain rather than being inferred from current paths. New reads reject the
session deletion tombstone. Scoped instructions and internal contracts describe
these ownership changes.

The final focused fixtures use a General Chat anchored read with a controlled
native child to isolate this lifetime gate from complete Git identity
preflight. They prove storage and permanent deletion cannot pass a held read,
an unrelated session can proceed, storage blocks new observations, cleanup joins
the original child, and unknown/legacy ownership is preserved. The fixture
originally used a Worktree and failed before its controlled child was admitted;
the final fixture changes only the tested preparation shape, not production
deadlines or ownership behavior.

`GOMAXPROCS=2 go test -race -p 1 -timeout=10m
./cmds/delidev-cli/internal/workspace
-run 'WorkspaceObservationGate|WorkspaceStoragePreservesUnassigned' -count=1`
passed in 7.233 seconds (`/tmp/delidev-1079-third-gates-isolated.log`).

## Observation validation failures and control

The focused gate success does not erase broader native-read failures:

- The first compile attempt referred to nonexistent `security.CheckPrivateFile`;
  it was corrected to existing `security.RegularPrivate`
  (`/tmp/delidev-1079-third-read-compat.log`).
- The `WorkspaceRead|PRWorkspaceMatch` race group failed the mismatch/access
  case at `pr_match_test.go:152` with `RecoveryRequired` (85.069 seconds package
  time, `/tmp/delidev-1079-third-read-compat-final.log`).
- An overlapping workspace/Worker group selected
  `WorkspaceObservationGate|WorkspaceStoragePreservesUnassigned|WorkspaceRead|PRWorkspaceMatch|SessionDeletionIncludesStoredAndRestored`.
  Workspace failed the same PR case at line 152 with an unavailable timeout
  (122.044 seconds), while Worker passed (118.921 seconds).
  Log: `/tmp/delidev-1079-third-read-storage.log`.
- The unchanged exact isolated PR mismatch/access case passed (59.960 seconds):
  `GOMAXPROCS=2 go test -race -p 1 -timeout=10m
  ./cmds/delidev-cli/internal/workspace
  -run '^TestPRWorkspaceMatchPreservesMismatchesAndDistinguishesUnknownAccess$'
  -count=1`, `/tmp/delidev-1079-third-pr-match-isolated.log`.
- The combined workspace/Worker group with serial package execution (`-p 1`,
  `-timeout=15m`) still failed. Workspace (196.490 seconds) reported current-head
  worktree PR matching at line 62 and mismatch/access matching at line 152 as
  unavailable timeouts, Local read at `read_test.go:143` as `RecoveryRequired`,
  and the original Worktree gate fixture failing to establish native ownership
  at line 81 before its held child (59.36 seconds test time). Worker (494.835
  seconds) failed stored/restored snapshot deletion in worktree/create and
  worktree/cleanup at line 48 with `RecoveryRequired`; worktree/restore failed
  initial preparation at line 22 because owned descendants could not be
  confirmed stopped. No race warning appears in this log:
  `/tmp/delidev-1079-third-read-storage-final.log`.

A source-only control archived the immediately preceding unlink commit
`38a11799962a6ebde0b3fdb4ddfdc39aecb39ddf`, including `go.mod`, `go.sum`, CLI
source and generated DeliDev Go protocols. It used an independent temporary
directory recorded in `/tmp/delidev-1079-third-read-control-path`, without a new
branch or worktree. The command was:

```sh
GOMAXPROCS=2 go test -race -p 1 -timeout=10m \
  ./cmds/delidev-cli/internal/workspace \
  -run '^Test(PRWorkspaceMatchPreservesMismatchesAndDistinguishesUnknownAccess|WorkspaceReadLocalPreservesSeparateProcessOwnership)$' -count=1
```

This control failed both cases (154.795 seconds): PR mismatch/access at line
120, an earlier stage than the repaired-source line-152 failure, and Local read
at line 143, both `RecoveryRequired`
(`/tmp/delidev-1079-third-read-control.log`). These selected failures also occur
before the new observation gate. That does not identify every failure's cause
or establish a complete baseline reproduction. Concurrent host compilation was
observed; its causal role remains unproven.

## Final source and other checks

[Long-path fixture preparation](pr-1231-long-path-fixture.md) preserves the
previous Windows CI evidence, the explicit temporary-source opt-in, a failed
local setup attempt, its unchanged passing rerun and native-runtime limits.
The final source revision for the full race command is
`c50507de8967c2fea2184a0107a4c155ed9df405`. Production source and tests remain
unchanged during that command; a later evidence-only commit is separate.

After all four repairs and the fixture change, `GOMAXPROCS=2 go vet
./cmds/delidev-cli/...`, the CLI build, Windows amd64 workspace-test compilation,
`pnpm ci:contracts` (113 checks) and `pnpm proto:fresh` passed. Logs:
`/tmp/delidev-1079-third-{vet,build,windows-final,contracts-final,proto-fresh-final}.log`.
These checks overlapped the first long-path fixture attempt, not the final full
race run. Git LFS integrity passed afterward, and an inventory confirmed no
repository-owned generated `dist` directory remains after removing desktop/client
outputs. The merge-phase frontend/API-client checks are separately pinned in
the reconciliation record.

## Full race run

```sh
GOMAXPROCS=2 go test -race -p 2 -timeout=30m -count=1 ./cmds/delidev-cli/...
```

Started against exact source `c50507de8967c2fea2184a0107a4c155ed9df405` at
2026-09-30 19:07 UTC. Log: `/tmp/delidev-1079-third-full-race.log`.
The complete command exited 1. Store (941.659 seconds), Worker (526.629 seconds)
and all other packages passed except CLI, discovery, Codex, Grok, server and
workspace. Grok/server reached their cumulative 30-minute package limits;
workspace failed unborn-diff and PR-mismatch fixtures (1,345.542 seconds package
time). The log contains no explicit race-warning block.
[Complete package and failure inventory](source-c50507de-completed-race.md)
preserves all 52 reported failed test/subtest headings and exact caller lines.
No complete suite success is claimed. The validation session has completed.

The unchanged exact isolated selection of the two full-run workspace failures
subsequently passed in 38.143 seconds (result in
`/tmp/delidev-1079-third-final-isolated.log`):

```sh
GOMAXPROCS=2 go test -race -p 1 -timeout=10m \
  ./cmds/delidev-cli/internal/workspace \
  -run '^Test(WorkspaceDiffUnbornAndBoundedResults|PRWorkspaceMatchPreservesMismatchesAndDistinguishesUnknownAccess)$' -count=1
```

This rerun does not erase the full-run line-163 unborn-diff or line-161 PR-match
failures and does not prove their causes. No production source changed.

## Preserved limits

All fixtures use isolated temporary state and controlled local processes, with
no real credentials, hosted inference or remote publication. Prior full runs
for `46452512`, `89cedbb7` and `3530d3d9d`, including failures, isolated reruns,
source controls and later fixture fixes, retain their separate evidence files.
This run cannot relabel their outcomes. Cross-compilation does not establish
native Windows/Linux runtime acceptance. The original Windows preparation
failure and other native initialization/cumulative timeout causes remain
unproven; new CI and Codex evidence is required after the final push.
