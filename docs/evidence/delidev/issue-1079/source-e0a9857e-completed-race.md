# Completed full race run for source e0a9857e

This independent record retains the complete validation outcome for exact source
`e0a9857e32bf8b585dac72c9734669275ce69092`. Production source and tests remained
frozen while the command ran in the existing PR worktree. Later commits change
only independent evidence. This source includes the exact main 6c749670 merge,
common branch reflog preservation and original staging ownership repairs. See
[merge verification](pr-1231-main-6c749670.md),
[reflog regression evidence](pr-1231-common-reflogs.md) and
[staging ownership evidence](pr-1231-staging-ownership.md).

## Command and outcome

```sh
GOMAXPROCS=2 go test -race -p 2 -timeout=30m -count=1 ./cmds/delidev-cli/...
```

Started `2026-09-30T23:09:24.708575Z`; completion was observed at
`2026-10-01T00:33:48.974116Z`. The complete command exited **1**.
The package timeout applies cumulatively within each package, rather than to
the whole command. All package results are final; no validation session remains
pending from this run.

Log: `/tmp/delidev-1079-fourth-full-race.log`.
SHA-256: `90cf0bed807de5f1629120aa57004faba549ac4a65b8b3fdd861e282eaa6593f`.

## Package results

| Package under cmds/delidev-cli | Result | Seconds |
| --- | --- | ---: |
| `internal/apiproxy` | pass | 6.546 |
| `internal/cli` | fail | 426.958 |
| `internal/connections` | pass | 56.991 |
| `internal/credentials` | pass | 7.179 |
| `internal/domain` | pass | 10.342 |
| `internal/forwarding` | pass | 3.132 |
| `internal/harness` | fail | 157.803 |
| `internal/harness/claude` | fail | 714.126 |
| `internal/harness/codex` | fail | 716.715 |
| `internal/harness/grok` | pass | 1535.499 |
| `internal/harness/nativewire` | pass | 16.986 |
| `internal/harness/opencode` | pass | 72.587 |
| `internal/integrations/github` | pass | 14.141 |
| `internal/presentation` | pass | 2.746 |
| `internal/process` | pass | 17.739 |
| `internal/providers` | pass | 9.803 |
| `internal/security` | pass | 1.669 |
| `internal/server` | fail | 1801.613 |
| `internal/store` | pass | 923.791 |
| `internal/userservice` | pass | 6.426 |
| `internal/worker` | pass | 415.334 |
| `internal/workspace` | fail | 1446.128 |

Packages with no test files: `cmds/delidev-cli` and `internal/rpc`.
Grok, store and Worker passed their complete packages on this source. These
results do not relabel earlier source revisions' failures.

## Complete failure inventory

Every failed test/subtest heading and stable caller diagnostic is retained below.
The CLI's complete structured result, private temporary fixture logs and timeout
stacks remain in the original hashed log. Its pending session preparation and
claimed workspace job had not settled when `session create --wait` expired.
This failure occurs at `sessions_test.go:202`, before the file-read failure at
line 215 on source c50507de and before the earlier 46452512/main-7090 controls'
file-read stage. Those controls do not establish this new failure's cause.

```text
--- FAIL: TestCLISessionAcceptanceQueueAndArchive (65.26s)
sessions_test.go:202: [session create --wait] exited 4; unavailable: The operation timed out. (complete structured fixture result retained in log)
--- FAIL: TestDiscoveryVerifiesClaudeWithoutGrantingExecution (33.83s)
discovery_claude_test.go:92: recovery_required: Claude Code probe cleanup could not be confirmed.
--- FAIL: TestDiscoveryVerifiesGrokWithoutExecution (19.97s)
discovery_grok_test.go:99: Grok discovery did not verify the isolated profile: state=detected version=1.0.41 protocol=&{Protocol:grok-acp State:failed Problem:unavailable: The installed harness did not complete native protocol validation.}
--- FAIL: TestDiscoveryVerifiesOpenCodeWithoutExecution (37.86s)
discovery_opencode_test.go:105: recovery_required: OpenCode probe cleanup could not be confirmed.
--- FAIL: TestAPIStreamRebuildsPrivateRuntimeAndValidatesNativeAuthority (239.40s)
--- FAIL: TestAPIStreamRebuildsPrivateRuntimeAndValidatesNativeAuthority/valid (12.50s)
api_test.go:151: recovery_required: Claude Code request delivery is uncertain.
--- FAIL: TestStreamRejectsProtocolFailuresAndStopsOwnedScope (52.87s)
--- FAIL: TestStreamRejectsProtocolFailuresAndStopsOwnedScope/duplicate-response (8.81s)
stream_test.go:419: incompatible native scope did not stop
--- FAIL: TestPermissionAcceptanceMatchesExactGrantScopeAndPreservesOtherRecovery (31.97s)
--- FAIL: TestPermissionAcceptanceMatchesExactGrantScopeAndPreservesOtherRecovery/healthy (15.98s)
approval_acceptance_test.go:29: unavailable: Codex app-server did not complete its native handshake.
--- FAIL: TestPRRemediationWorkspaceReadBindsOriginalCandidateAndExclusiveProof (29.24s)
--- FAIL: TestPRRemediationWorkspaceReadBindsOriginalCandidateAndExclusiveProof/unlink (11.05s)
pr_remediation_workspace_test.go:60: <nil>
--- FAIL: TestGrokFirstDispatchRetainsUnsupportedSelectionsWithoutClaiming (38.24s)
--- FAIL: TestGrokFirstDispatchRetainsUnsupportedSelectionsWithoutClaiming/repository (20.35s)
session_grok_dispatch_test.go:22: unavailable: The operation timed out.
--- FAIL: TestWorkspaceDiffUnbornAndBoundedResults (21.40s)
diff_test.go:163: unborn diff invented a commit working-tree recovery_required: The Worker workspace result does not prove the accepted preparation.
```

Server also reached its cumulative 30-minute package bound:

```text
panic: test timed out after 30m0s
running tests:
TestSessionNativeUncertaintyCannotBeEditedOrDeclaredStopped (1s)
```

The one-second running-test entry is not evidence that this individual test hung
for 30 minutes. The PR remediation `unlink` failure is at line 60, where the
scripted read stream did not deliver its expected request and its reported error
was nil; it is not the later assertion that changed candidate ownership was
incorrectly accepted. The Grok first-dispatch failure occurs while building the
repository fixture. No cause for these native initialization, delivery, cleanup,
stream or cumulative timing failures is proven by this command.

## Unchanged exact workspace rerun

The only failed workspace case was rerun once with unchanged source and tests:

```sh
GOMAXPROCS=2 go test -race -p 1 -timeout=10m -count=1 ./cmds/delidev-cli/internal/workspace -run '^TestWorkspaceDiffUnbornAndBoundedResults$'
```

It passed in **21.733 seconds**, exit 0, from
`2026-10-01T00:34:20.679127Z` to `2026-10-01T00:34:43.854224Z`.
Log: `/tmp/delidev-1079-fourth-unborn-isolated.log`.
SHA-256: `ca42ec98ef65952af26ed851a16da522a3a9ef0fcbaa9fab5fc1f1b9055e34e7`.
The full-command failure remains valid evidence. This isolated pass does not
prove its cause or establish a blanket baseline exception. Previous unchanged
isolated reruns remain tied to their own source revisions.

## Other executed verification

On the repaired source, `GOMAXPROCS=2 go vet ./cmds/delidev-cli/...`, the CLI build
and Windows amd64 workspace test cross-compilation passed. Their logs are
`/tmp/delidev-1079-fourth-vet.log`, `fourth-cli-build.log` and
`fourth-windows-compile.log` under `/tmp/delidev-1079-`. Cross-compilation does
not execute Windows filesystem, Git or process behavior.

`pnpm proto:check` passed, including generated-source drift validation, and
`pnpm ci:contracts` passed all 113 checks on the final source. Logs are
`/tmp/delidev-1079-fourth-proto-check.log` and
`/tmp/delidev-1079-fourth-final-contracts.log`. Merge verification separately
retains protocol generation/lint/breaking checks, identical regeneration of
120 generated files, the client suite and focused fork/storage server, store,
workspace and Worker selections. Reflog and staging evidence separately retains
the reproduced negative tests and passing regression/deletion selections.

The required complete desktop command did **not** pass: the second run reported
113 failures/1166 passes across 24 failing/76 passing files; its unchanged serial
selection retained 10 App timeouts while backups and the Go workspace fixture
passed. The first full run's distinct App focus timeout, its unchanged exact
isolated pass, and separately executed successful bundle, launch/assets, widget
and production build checks remain in
[the complete desktop record](source-e0a9857e-desktop-failures.md) and merge
evidence. Neither complete desktop command is presented as green.

Git LFS fsck passed. No Rust files changed in this pass. Generated repository-owned
`dist` directories were removed after frontend commands completed; imported
toolchain and dependency output was preserved.

## Review assessment and remaining limits

The post-rename parent-sync finding `PRRT_kwDORRAKg86noHzm` requires no production
change. Both `security.SyncParent` error branches in `renameStorage` already
return `ResultUncertain()` on reviewed source 2f376f4de and current source; only
the rename call itself has the raw-error branch. Blame attributes both existing
uncertainty branches to original implementation ea83fc363a. An English reply to
that inline thread will cite this existing behavior after the final push.

A read-only host sample at `2026-09-30T23:17:28.404080Z` recorded load averages
316.72/224.45/118.37 and concurrent native/Node activity. Redacted process metadata
is `/tmp/delidev-1079-fourth-validation-host-load.json`. This context does not
prove any individual failure's cause. No production or test deadline was raised
to convert uncertain ownership into success. The full log contains zero explicit
`WARNING: DATA RACE` blocks; their absence does not prove all concurrent behavior.

All earlier source-46452512, source-89cedbb7, source-3530d3d9d and source-c50507de
full failures, their controls, isolated reruns and later repairs remain separate.
The original native Windows long-path initial-Prepare failure remains unproven;
the fixture repair and cross-compilation do not establish native Windows success.
Native Windows/Linux runtime, release acceptance, new pushed-head CI and fresh
Codex approval remain unclaimed. Temporary fixtures used local controlled
processes and isolated repositories, without real credentials or hosted inference.
