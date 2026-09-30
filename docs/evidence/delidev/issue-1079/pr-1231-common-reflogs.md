# PR #1231 common reflog preservation

Codex thread `PRRT_kwDORRAKg86noHzs` correctly identified that linked-worktree
capture omitted all common `logs/`, losing shared branch reflogs. The selected
worktree's overlay restores its own HEAD log, but cannot replace branch history.

Capture now omits other worktree administration and only individual files that
collide with the selected worktree overlay. Common branch reflogs remain within
the existing bounded, synchronized, independently verified Git inventory. The
selected HEAD log still replaces the common HEAD log. Scoped instructions and
the storage contract record this fidelity requirement.

A real temporary Git fixture creates an unpushed commit on the original main
branch and resets it. It proves the commit is absent from all refs and present in
the common branch reflog. The managed detached worktree is cleaned and restored
with the original source renamed offline. The test verifies exact common branch
reflog history, the selected worktree's own HEAD reflog and the reset commit's file
contents without access to the original store.

Before the fix, the exact new regression failed at the restored branch reflog
comparison (13.677 seconds), proving the retained object alone was insufficient.
After the fix, `GOMAXPROCS=2 go test -race -p 1 -timeout=15m
./cmds/delidev-cli/internal/workspace
-run 'TestSnapshotRetainsCommonBranchReflogOffline|TestWorkspaceSnapshotFaithfullyRestoresEveryRepository|TestSnapshotCopyBudget|TestSnapshotGitAtLongPrivatePath'
-count=1` passed in 51.045 seconds. Logs:
`/tmp/delidev-1079-fourth-reflog-before.log` and
`/tmp/delidev-1079-fourth-reflog-after.log`.

This focused validation does not establish final full-source race or native
Windows success. Previous failures and source distinctions remain unchanged.
