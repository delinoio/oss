# Issue #1084: combined validation after proxy authority repair

## Revision and repair ownership

The 2026-09-30 18:16 UTC maintenance pass inspected PR #1216 at `49feceaa855c3fc2b10c453356a2c54d06c02ec7`: Open, conflicting, with a completed Codex security finding and one failed external Cloudflare Pages check. This one-shot repair merges main `6c749670727b30679e722821846bc8dc00f5ac32` in `a0d5eebf4`, then fixes proxy credential authority retention in `6200788ae2a297e9e818e8869b9da557927011cf`. All validation below uses that combined source tree. Separate evidence records retain each repair's focused results and the actual pre-fix credential leak. Existing historical ledgers and earlier failed/passing runs remain unchanged.

## Passed checks

- `pnpm proto:check`: passed formatting, lint, current-main breaking comparison and forced uncached generation freshness, with no tracked or untracked generated-source drift.
- `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...`: passed.
- `GOMAXPROCS=2 go build -p 2 -o /tmp/delidev-1084-e6e9-repair-1816-binary ./cmds/delidev-cli`: passed; output stays outside the repository.
- Client typecheck and six files / 47 tests passed during main composition.
- The focused Go race composition passed server/store/CLI/domain tests for Network, Fork and existing capability preservation. The complete server Network-focused race selection after the authority fix passed in 124.470s, including exact receipts, private publication/deletion intents and the paired-client CONNECT leak regression.
- Desktop client build and typecheck passed in the complete `pnpm test` attempt. After that attempt failed at Vitest, its later stages were explicitly run separately: `pnpm test:bundle-dry-run`, `pnpm test:desktop-launch`, `pnpm test:widget` and `pnpm build` all passed. Results include eight bundle fixtures, 16 launch/assets fixtures, widget isolation/storage checks and the production Rsbuild bundle. They do not change the failed full-test result.

## Failed desktop attempt

`pnpm test` from `apps/delidev` exited 1: 100 files, 94 passed / six failed; 1,279 tests, 1,215 passed / 64 failed. Vitest duration was 642.51s. Failed files were `App.test.tsx`, `settings.test.tsx`, `settings-models.test.tsx`, `agent-configuration.test.tsx`, `settings-workspace.integration.test.tsx` and `tray-presentation.test.tsx`. Forty-three diagnostics explicitly reported 5-second or 15-second test timeouts; remaining failures included absent async UI elements/labels. The real Worker workspace test could not find its expected Done button after accepted repository saving. These failures are retained, without changing assertions or observation deadlines.

Only the per-process Vitest worker count was temporarily capped at two; `vitest.config.ts` was restored byte-for-byte in a `finally` block. The complete desktop command still failed. Its independent later-stage results above cannot imply component-suite or native application acceptance.

## Focused desktop diagnostic after changed host load

After the Go run ended and observed load had fallen from above 300 to roughly 45, the client output was rebuilt before a **single** diagnostic selection:

`GOMAXPROCS=2 pnpm exec vitest run src/App.test.tsx src/settings.test.tsx src/settings-models.test.tsx src/agent-configuration.test.tsx src/settings-workspace.integration.test.tsx src/tray-presentation.test.tsx --maxWorkers 2`

This exited 1: six files, four passed / two failed; 145 tests, 143 passed / two failed, in 108.80s. The remaining failures were `App.test.tsx:172` (uncertain New Project save/reopening) and `settings.test.tsx:723` (independent unfiltered picker cursor / retained provider). Both reported their existing 5-second timeout. The real Worker workspace test and the other formerly failing files passed in this selection. No assertion/deadline or source code changed between attempts. The changed results support investigating execution conditions, but do not prove a cause, fix either remaining failure or replace the failed complete desktop run. No further retry was performed.

## Full Go race attempt

`GOMAXPROCS=2 go test -race -p 4 ./cmds/delidev-cli/... -timeout=20m` exited 1: **13 passing and 10 failing test packages**, with one unchanged security result cached and three no-test packages. The log contains 33 top-level failed tests, four package timeout reports and three race-warning blocks. No full Go pass is claimed.

| Failed package under `cmds/delidev-cli/internal/` | Result duration | Retained observations |
| --- | ---: | --- |
| `cli` | 420.184s | Configuration transfer remained incomplete; repository inspection timed out; session acceptance failed at repository creation before reaching the earlier creation-diff failure. |
| `harness` | 203.800s | Claude/Grok/OpenCode isolated discovery, owned cleanup and explicit-path classifications failed. |
| `harness/claude` | 744.159s | API stream private-runtime/native-authority rebuild failed. |
| `harness/codex` | 1202.070s | Permission/continuation/Steer/native-setting evidence failed; package timed out with `TestThreadLateAcknowledgmentRetainsOriginalIdentityWithoutRetry` only three seconds into execution. |
| `harness/grok` | 1202.545s | Package timed out in `TestInitialPlanRequiresOriginalClaimAckAndMode/mode-result-null`. |
| `harness/nativewire` | 162.465s | Previously recorded concurrent test log-buffer read/write race reproduced. |
| `harness/opencode` | 113.190s | Native fixture failure; the full log's test inventory is retained below. |
| `server` | 1203.274s | Package timed out in `TestExecutionToolBoundsKeepPriorEvidenceAndSequence/patch`. |
| `worker` | 617.611s | Native Worker/stream fixtures failed, including targeted cancellation ownership. |
| `workspace` | 1200.990s | Package timed out in `TestPRFirstExecutionRechecksRemoteAndPreservesOriginalPreparation/deleted`. |

Passing packages were API relay, connections, credentials, domain, forwarding, GitHub integration, outbound routing, presentation, process ownership, providers, security, store (1092.148s) and user services. No new authority regression is reported as failed, but an unfinished package is not proof that every test ran; the independent focused checks above provide that coverage.

Top-level failed tests, without raw native output or fixture content:
- `TestCLIConfigurationTransferThroughRealWorker`
- `TestCLIPairWorkerAndInspectRealRepository`
- `TestCLISessionAcceptanceQueueAndArchive`
- `TestDiscoveryVerifiesClaudeWithoutGrantingExecution`
- `TestDiscoveryVerifiesGrokWithoutExecution`
- `TestDiscoveryVerifiesOpenCodeWithoutExecution`
- `TestDiscoveryUsesIsolatedEnvironmentAndOwnedProcesses`
- `TestExplicitPathFailuresNeverFallBack`
- `TestDiscoveryBoundsAndGrokUpdateSuppression`
- `TestAPIStreamRebuildsPrivateRuntimeAndValidatesNativeAuthority`
- `TestPermissionAcceptanceMatchesExactGrantScopeAndPreservesOtherRecovery`
- `TestPermissionAcceptanceRejectsForeignAmbiguousAndChangedEvidence`
- `TestContinuationUnavailableAndChangedStateKeepInputBlocked`
- `TestSteerInspectionConfirmsExactHistoryWithoutReplay`
- `TestThreadChangedEffectiveSettingsAndMalformedRepliesRequireRecovery`
- `TestAPIInitializationOwnsConfigurationAndNativeAuthority`
- `TestSessionBindingRequiresOriginalReadyModeAndConfiguration`
- `TestTextClosureRequiresOriginalSummaryAcknowledgmentAndRemoval`
- `TestOwnedInputRetainsClaimsAndRejectsUncertainReplay`
- `TestInputPreflightPreservesUnusedBoundary`
- `TestInputPublicationJoinsNativeClosure`
- `TestInstructionChangeBeforeNativeInputRefusesClaim`
- `TestExplicitJSONRPCProfileRequestsRepliesAndNotifications`
- `TestExplicitJSONRPCRejectsForeignEnvelopesAndBoundedDiagnostics`
- `TestEventReconciliationCountsRepeatedWireBytesBeforeCanonicalization`
- `TestWorkerPRStartupRejectionBindsJournalAndNeverStartsHarness`
- `TestSessionDeletionWorkerRemovesOnlySelectedWorktreeAndResumesAfterRemoval`
- `TestTargetedCancellationKeepsStreamAndNextOperationUsable`
- `TestWorkspaceDiffKeepsOriginalCreationCommitAndLiveExecution`
- `TestWorkspaceDiffLiteralPathBinaryAndNoExternalHelpers`
- `TestWorkspaceDiffUnbornAndBoundedResults`
- `TestClosedExecutionInspectionHoldsOwnershipAndPreservesAgentResults`
- `TestPRWorkspaceMatchPreservesMismatchesAndDistinguishesUnknownAccess`


## Execution conditions and unresolved limits

During broad validation, host load averages included 325.88 / 304.84 / 231.16 and 312.55 / 308.33 / 244.95. The Go and desktop commands overlapped, and other host work was also active. Those observations suggest resource contention, but they do not prove the cause of any individual failure or establish a branch/baseline comparison. Deadline, ownership and UI failures remain unresolved rather than relabeled as successful or unrelated.

The native-wire race report independently proves concurrent asynchronous process logging and `bytes.Buffer.String` at `harness/nativewire/wire_test.go:186`; it is the previously recorded shared test-log capture defect. The affected test source is unchanged from merged main. That source comparison does not establish the cause of the other failures or fix this race. The full Go and desktop validators each ran once. No standalone Go retry or deadline/assertion relaxation was used. A separate focused desktop diagnostic is recorded below only after the observed host load changed substantially.

Cloudflare Pages check `110005065008`, deployment `9534bf19-3029-4057-bdc5-5838d2320c8a`, failed on reviewed head `49feceaa8`. Both the PR check inventory and the GitHub CheckRun output expose only Build failed and a Cloudflare dashboard link, with no text or annotations. Under repair-pr's external-check rule it remains report-only without gh-accessible logs. No external build cause was inferred or repaired.

The icon source was hydrated in Git LFS before asset checks. Generated desktop/client `dist` directories were removed after consumption, and no Rust source changed in this pass. Real provider/GitHub accounts, enterprise networks, native OS credential lifecycle, installed desktop/Worker fork execution, Windows/Linux runtime, release and #1085 Worker bootstrap acceptance remain unexercised. Earlier CLI branch/baseline uncertainty and historical broad failures remain visible. A new push requires fresh CI and Codex review; earlier-head results cannot certify it.
