# Issue #1079: Worker workspace snapshots

## Replacement scope

This replacement starts from main `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` after the feature-ownership refactor. It adapts the preserved implementation from closed, unmerged PR #1121 (`fb1a31e43cc8a87cce163ade660f960b96baa933`) into service-specific schema, CLI dispatch/client and server route/status files. Workspace storage uses the main-reserved system capability number **11**, preserving existing values 1, 2 and 3. The executable SQLite schema remains 24; no migration or backup requirement is introduced.

Authenticated owner/client RPC and CLI operations provide preview, snapshot create/inspect/restore/delete, manual cleanup, original-operation observation, cancellation and explicit recovery. Every repository is verified before whole-root cleanup, and restoration publishes the complete workspace at one unoccupied owned destination. Native replay, active work, original Local checkouts, unresolved dependents, stale previews and deletion of the only recoverable copy remain blocked. Complete contracts live in [storage operations](../../../cmds-delidev-storage-contract.md) and [workspace ownership](../../../cmds-delidev-workspace-contract.md).

## Executed replacement checks

Implementation checks below cover commits `ea83fc363a278caacfe0746c9fef9d0a958820c5` and `6e472548da7f4006814e166c3f4c1b42b47c642a`. The focused initial command was `go test -race -p 2 -timeout=15m ./cmds/delidev-cli/internal/workspace ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/cli -run 'Snapshot|WorkspaceStorage|WorkspaceCapability'`; its CLI package matched no tests.

- Focused snapshot/Worker/server race regressions pass: two-repository restoration preserves unpushed commits, staged/unstaged changes, untracked/ignored data, executable modes and escaping symlinks; original sources can be offline during restore, and local remote fixtures receive no push.
- Second-repository disk exhaustion, cancellation during copying and after publication, socket/FIFO/device rejection, stale preview, destination conflict, General Chat, original claim identity, interrupted-job recovery, receipt replay, Resume exclusion and only-copy protection pass.
- Protocol formatting/lint and FILE compatibility against current main pass.
- All 102 repository contract tests pass.
- API client build/typecheck and all 44 tests pass, retaining generated service and historical compatibility imports.

- The strengthened second-repository disk-full regression passes for both explicit snapshot creation and preview-bound manual cleanup, comparing the complete original workspace/Git digest and byte count afterward (`go test -race -timeout=5m ./cmds/delidev-cli/internal/workspace -run TestSnapshotSecondRepositoryDiskFailurePreservesAllSources`; 189.377s under concurrent host load).
- Explicit storage CLI/journal regressions pass: `go test -race -p 2 -timeout=10m ./cmds/delidev-cli/internal/cli ./cmds/delidev-cli/internal/worker -run '^(TestStorageCLIUsesExactConnectMutationAndConfirmation|TestStorageJournalDoesNotReplayCleanupAndRecoveryBindsOriginal)$'` (CLI 7.896s; Worker 3.457s).
- `go vet ./cmds/delidev-cli/...` passes.
- Complete root `pnpm proto:check` passes, including regenerated-source freshness.
- Workspace test binaries cross-compile for Windows amd64 and Linux arm64. This is compilation evidence only.
- `git lfs fsck` passes. Generated API-client `dist` output is removed.

The initial required full `go test -race -p 2 -timeout=30m ./cmds/delidev-cli/...` run was still in progress at 2026-09-30 07:00 UTC. Its process and final exit status are no longer available; the retained partial log contains these failures:

- CLI: `TestCLISessionAcceptanceQueueAndArchive` reports the workspace-file reader unavailable at `sessions_test.go:215` (171.685s package time). An isolated branch rerun fails earlier during preparation at line 202 (126.401s package time). The exact isolated command on a source-only archive of current main `74701b8948694e2bf8f8ba6d07e596c2d2f358a7`, `go test -race -timeout=5m ./cmds/delidev-cli/internal/cli -run '^TestCLISessionAcceptanceQueueAndArchive$' -count=1`, reproduces the original line-215 reader failure (70.349s package time). That specific failure is therefore also present on the baseline on this host.
- Claude harness (910.445s package time): bounded probe cleanup/timing, late acknowledgments, original callback cancellation, protocol-failure shutdown, and mismatched reply echo tests fail.
- Codex harness (1164.175s package time): original approval response/execution, permission grant, and exact workspace-root tests fail with handshake or uncertain-delivery outcomes.

The retained log later also reports Grok original text/file/input/plan ownership and acknowledgment failures (package timeout after 1800.963s), native-wire JSON-RPC envelope/protocol failures (83.313s), OpenCode probe cleanup failure (326.575s), and process descendant/scope/cancellation failures (173.565s). The Claude, Codex, Grok, native-wire, OpenCode and process source directories have no diff from the inspected main revision; those failures have not been independently reproduced on main. Concurrent native test processes are present on the host, but the record does not infer that scheduling explains every failure. No complete race-suite success is claimed.

[PR review repairs and validation](review-repair-validation.md) records the subsequent seven review fixes, their follow-up regression checks and the separately executed maintenance validation. It does not replace or relabel the initial run above.

## Evidence limits

Tests use isolated temporary repositories/state, injected faults and controlled local providers. No user credentials, hosted-account inference, remote snapshot storage or release publication is used. Cross-compilation does not establish native Windows/Linux runtime acceptance. Permanent session/dependent Sidechat deletion, database restore and desktop storage management retain their separate issue #964 boundaries.

[Historical PR validation](prior-pr-validation.md) preserves all earlier source-backed records and failures separately; none is claimed as execution of this replacement revision.
