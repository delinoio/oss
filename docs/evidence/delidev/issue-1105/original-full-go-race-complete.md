# Issue #1105 original full Go race run: complete result

## Command and provenance

`go test -race -timeout=20m -p=2 ./cmds/delidev-cli/...` completed naturally
with **exit 1** on 2026-09-30 (exec session 60843, macOS arm64 / Go 1.26.8).
No second full native suite was launched and this original run was not interrupted.

The run started in the restored implementation worktree based on main
`ad0e3e9a29cb3d8375ab5d168bb160c35a023250`, before the implementation was
committed as `fc0abfb307444d7dde54cc94bf3bf54ed82171c2`. Follow-up fixture
coverage was added and main `7090de04621ece95b3c2cce8d88fcfcdeadad7cc` was
merged as `260b9a8ea7c404040ebd60a09fe39960fb44e71c` while later packages
were pending. The exact source/compile boundary for each package was not
retained. This is the complete output of the original command, **not** an
immutable-revision validation of either parent or the final merged tree.

## Complete package results

The command reported 15 passing packages, seven failing packages and two
packages without test files. Every package result is retained below; a timeout
can prevent remaining cases in that package from running. No complete test-case
count or passing full-suite claim is inferred.

Package names below are relative to `github.com/delinoio/oss/cmds/delidev-cli`.

| Package | Result | Reported duration / note |
| --- | --- | --- |
| `(command root)` | No test files | [no test files] |
| `internal/apiproxy` | Passed | 1.748s |
| `internal/cli` | Failed | 147.239s |
| `internal/connections` | Passed | 14.156s |
| `internal/credentials` | Passed | 3.553s |
| `internal/domain` | Passed | 1.894s |
| `internal/forwarding` | Passed | 2.240s |
| `internal/harness` | Passed | 12.152s |
| `internal/harness/claude` | Passed | 84.666s |
| `internal/harness/codex` | Failed | 1200.595s |
| `internal/harness/grok` | Failed | 1200.822s |
| `internal/harness/nativewire` | Passed | 83.864s |
| `internal/harness/opencode` | Failed | 264.671s |
| `internal/integrations/github` | Passed | 2.767s |
| `internal/presentation` | Passed | 1.818s |
| `internal/process` | Passed | 45.050s |
| `internal/providers` | Passed | 9.472s |
| `internal/rpc` | No test files | [no test files] |
| `internal/security` | Passed | 1.832s |
| `internal/server` | Failed | 1201.000s |
| `internal/store` | Passed | 910.233s |
| `internal/userservice` | Passed | 6.776s |
| `internal/worker` | Failed | 1201.198s |
| `internal/workspace` | Failed | 1200.792s |

## Observed failures and package deadlines

The following are all top-level failed cases reported before their package
footers. They are observations, not diagnoses or attribution to ALLGREEN.
Nested native failure content, paths, fixture messages and stack dumps are
excluded; their outcome boundaries are retained through the case names.

### `internal/cli`

- `TestCLISessionAcceptanceQueueAndArchive` (40.53s).

### `internal/harness/codex`

- `TestSteerInspectionConfirmsExactHistoryWithoutReplay` (28.99s).
- `TestThreadBindingUsesExactSettingsAndAdditiveInstructions` (18.51s).
- `TestThreadResumePreservesIdentityAndReportsActiveState` (24.56s).
- `TestThreadNativeDefaultsAreObservableWithoutInventingThem` (23.47s).
- `TestThreadInvalidSettingsDoNotConsumeRequestIdentity` (23.21s).
- `TestThreadChangedEffectiveSettingsAndMalformedRepliesRequireRecovery` (391.14s).
- `TestThreadLateAcknowledgmentRetainsOriginalIdentityWithoutRetry` (8.73s).

### `internal/harness/grok`

- `TestSessionBindingRequiresOriginalReadyModeAndConfiguration` (24.39s).
- `TestTextClosureRequiresOriginalSummaryAcknowledgmentAndRemoval` (184.79s).
- `TestTextClosureClaimCancelsWithNativeLifetime` (25.37s).
- `TestOriginalFileReplyClaimsAndTerminalFaults` (312.71s).
- `TestOriginalFileReplyIsIndependentOfBlockedPublication` (34.56s).
- `TestOriginalFileReplyClaimJoinsNativeLifetimeLoss` (28.89s).
- `TestOriginalTextFootprintIsIndependentOfPublicationCopies` (54.33s).
- `TestOwnedInputRetainsClaimsAndRejectsUncertainReplay` (296.36s).
- `TestInputPreflightPreservesUnusedBoundary` (21.54s).
- `TestInstructionChangeBeforeNativeInputRefusesClaim` (14.53s).

### `internal/harness/opencode`

- `TestProbeOwnsAuthenticatedServerAndCleanup` (81.50s).

### `internal/server`

- `TestClaudeFailedResumeClaimsNextFIFOInputOnce` (9.47s).
- `TestClaudeInterruptionPreservesOriginalRecordsAndPauseWithoutInputOutcome` (23.95s).

### `internal/worker`

The package reached its deadline without a preceding top-level failed-case
footer for its running case.

### `internal/workspace`

- `TestWorkspaceDiffKeepsOriginalCreationCommitAndLiveExecution` (32.90s).
- `TestWorkspaceDiffLiteralPathBinaryAndNoExternalHelpers` (31.78s).
- `TestWorkspaceDiffUnbornAndBoundedResults` (31.55s).
- `TestPRWorkspaceMatchReadsCurrentHeadWithoutTakingExecutionOwnership` (78.89s).
- `TestPRWorkspaceMatchPreservesMismatchesAndDistinguishesUnknownAccess` (24.71s).

Five packages reached their 20-minute package deadline. The tests active
at timeout are listed below. Their displayed elapsed times belong to those
active cases, not the whole package, and do not mean each case ran for 20 minutes.

- `internal/harness/codex`: `TestMultipleWorkspaceRootsRejectIncompleteOrBroadenedNativeAuthority (9s)`; `TestMultipleWorkspaceRootsRejectIncompleteOrBroadenedNativeAuthority/thread-missing-runtime-roots (4s)`.
- `internal/harness/grok`: `TestInitialPlanRequiresOriginalClaimAckAndMode (1m32s)`; `TestInitialPlanRequiresOriginalClaimAckAndMode/mode-missing-event (25s)`.
- `internal/server`: `TestExecutionGrantRechecksMutableOwnership (56s)`; `TestExecutionGrantRechecksMutableOwnership/opencode (12s)`; `TestExecutionGrantRechecksMutableOwnership/opencode/replaced-instance (7s)`.
- `internal/worker`: `TestOpenCodeBuiltinsBlockChangedIdentityAndUncertainPublication (5s)`; `TestOpenCodeBuiltinsBlockChangedIdentityAndUncertainPublication/call (1s)`.
- `internal/workspace`: `TestPRFirstExecutionRechecksRemoteAndPreservesOriginalPreparation (2m18s)`; `TestPRFirstExecutionRechecksRemoteAndPreservesOriginalPreparation/deleted (3s)`.

## Evidence limits and original log

The CLI fixture failed during `session review create` because the workspace
file reader was unavailable. The server also reported unavailable original
Worker connectivity and lost native API authority before its deadline.
OpenCode probe failures included native handshake/cleanup uncertainty.
The workspace package reported diff/PR-match failures before its deadline.
These distinct errors and deadlines do not establish one shared root cause.

The [untouched-main control](native-cli-baseline-control.md) independently
failed only the documented CLI fixture, at an earlier preparation stage.
It neither explains these other package failures nor proves that they are
environmental or pre-existing. Successful queue-specific and merged-tree
checks remain separately recorded in the initial implementation and
[merged validation evidence](merge-validation-and-ci-2026-09-30.md).

Captured log: `/tmp/delidev-1105-go-race.log` (local, untracked).
Bytes: 166192. SHA-256:
`348f46836fc6043e9fc675d7b8252697798112f4e1fc24d7d8b5036018818aaf`.

The captured output contains no `WARNING: DATA RACE` or `race detected during
execution of test` report. That observation cannot establish the absence of
races in cases prevented from completing by failures or timeouts. The log is
not committed because raw native fixture output and temporary paths are
outside evidence-document boundaries. This record retains all package results,
reported top-level failures and active deadline cases without claiming hosted
account, native packaged-app, supported-platform or release acceptance.
