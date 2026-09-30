# Issue #1079: Worker workspace snapshots

## Replacement scope

This replacement starts from main `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` after the feature-ownership refactor. It adapts the preserved implementation from closed, unmerged PR #1121 (`fb1a31e43cc8a87cce163ade660f960b96baa933`) into service-specific schema, CLI dispatch/client and server route/status files. Workspace storage uses the main-reserved system capability number **11**, preserving existing values 1, 2 and 3. The executable SQLite schema remains 24; no migration or backup requirement is introduced.

Authenticated owner/client RPC and CLI operations provide preview, snapshot create/inspect/restore/delete, manual cleanup, original-operation observation, cancellation and explicit recovery. Every repository is verified before whole-root cleanup, and restoration publishes the complete workspace at one unoccupied owned destination. Native replay, active work, original Local checkouts, unresolved dependents, stale previews and deletion of the only recoverable copy remain blocked. Complete contracts live in [storage operations](../../../cmds-delidev-storage-contract.md) and [workspace ownership](../../../cmds-delidev-workspace-contract.md).

## Executed replacement checks

- Focused snapshot/Worker/server race regressions pass: two-repository restoration preserves unpushed commits, staged/unstaged changes, untracked/ignored data, executable modes and escaping symlinks; original sources can be offline during restore, and local remote fixtures receive no push.
- Second-repository disk exhaustion, cancellation during copying and after publication, socket/FIFO/device rejection, stale preview, destination conflict, General Chat, original claim identity, interrupted-job recovery, receipt replay, Resume exclusion and only-copy protection pass.
- Protocol formatting/lint and FILE compatibility against current main pass.
- All 102 repository contract tests pass.
- API client build/typecheck and all 44 tests pass, retaining generated service and historical compatibility imports.

- The strengthened second-repository disk-full regression passes for both explicit snapshot creation and preview-bound manual cleanup, comparing the complete original workspace/Git digest and byte count afterward (`go test -race -timeout=5m ./cmds/delidev-cli/internal/workspace -run TestSnapshotSecondRepositoryDiskFailurePreservesAllSources`; 189.377s under concurrent host load).
- `go vet ./cmds/delidev-cli/...` passes.
- Complete root `pnpm proto:check` passes, including regenerated-source freshness.
- Workspace test binaries cross-compile for Windows amd64 and Linux arm64. This is compilation evidence only.
- `git lfs fsck` passes. Generated API-client `dist` output is removed.

The required full `go test -race -p 2 -timeout=30m ./cmds/delidev-cli/...` run is still in progress. Its CLI package has reported a workspace-file reader timeout in `TestCLISessionAcceptanceQueueAndArchive` (171.685s package time); an isolated rerun and a source-only current-main comparison are in progress. No full-suite success or regression classification is claimed yet.

## Evidence limits

Tests use isolated temporary repositories/state, injected faults and controlled local providers. No user credentials, hosted-account inference, remote snapshot storage or release publication is used. Cross-compilation does not establish native Windows/Linux runtime acceptance. Permanent session/dependent Sidechat deletion, database restore and desktop storage management retain their separate issue #964 boundaries.

[Historical PR validation](prior-pr-validation.md) preserves all earlier source-backed records and failures separately; none is claimed as execution of this replacement revision.
