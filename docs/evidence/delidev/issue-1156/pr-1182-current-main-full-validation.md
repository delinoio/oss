# PR #1182 current-main validation

Date: 2026-09-30. Validated implementation revision: `383501cd0e37b62b86385a70cc49901bd001c3e3`. This composes main `c49b08027e5b6679cd0fd01066544ac1a72d4a0e` and the additionally observed `82020a7ef2916534f3342aa5208f43d2a051b052`. Host: macOS arm64, Node.js `v24.11.0`, Go `go1.26.8`, Chrome `154.0.8037.59`.

## Required frontend pipeline

Ran package-local `pnpm test` from `apps/delidev`. Only a temporary `maxWorkers: 1` Vitest resource limit was applied and the original runner configuration was restored in cleanup. This run did not change test, hook or asynchronous lookup timeouts; main's committed fixture-owned UI waits and restoration remained intact.

Generated API client build and desktop typecheck passed. All 1,181 Vitest tests in 93 files passed (175.96 seconds). Native-package dry-run checks passed all eight tests; asset/launcher checks passed all 16; widget fixtures and production frontend build passed. Desktop assets were hydrated before validation. Generated `apps/delidev/dist` and `packages/delidev-api-client/dist` were removed afterward; no repository-owned generated distribution remains.

## Go merge regression

Selected every top-level Test function from the Go test files changed between previous PR head `33cd86bd3874981d7d32f263979e1df1265abe91` and this implementation. Executed the exact command below with `GOMAXPROCS=2` to bound host resource use. This reruns the relevant tests instead of relying on cached results.

```sh
go test -json -count=1 -p=1 -timeout=20m -run '^(TestClaudeContinuationRequiresOriginalSettledPermissionAndReport|TestClaudeFailedCheckpointStillRequiresOriginalNativeEOFProof|TestClaudeFailedContinuationRequiresCorrelatedNonAbortedSettledInput|TestClaudeFailedResumeClaimsNextFIFOInputOnce|TestClaudeLostFailedReportRecoveryPreservesOriginalFailure|TestClaudeQuestionContinuationRequiresOriginalAcceptedResponse|TestClaudeRecoveryRechecksOriginalSettledBoundary|TestClaudeRecoveryRequiresExplicitOriginalComparison|TestClaudeToolContinuationRetainsOnlyOriginalAcceptedPermission|TestClosedContinuationConcurrentClaimsLaunchOnce|TestClosedContinuationKeepsOriginalHistoryAndConsumesOneNativeLaunch|TestClosedContinuationPreservesFailureUntilExplicitResume|TestClosedContinuationRefusesIneligibleNativeBoundariesBeforeCleanup|TestClosedContinuationRejectsChangedAuthorityOrHistoryWithoutReplay|TestFailedCheckpointRejectsChangedSettingsWithoutNativeReplay|TestOriginalEOFHistoryRetentionRequiresExactOriginalCleanCompletion|TestOriginalFailedEOFCheckpointPreservesFailureAndRequiresExplicitResume|TestRecoveryHarnessSelectionPreservesHistoricalCodexWire)$' ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/harness/claude ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/worker
```

All four packages and 18 selected top-level tests passed, with 298 passing test events including subtests, no skipped tests and no failures. These retained-state, EOF/checkpoint, explicit-Resume and original-receipt tests are independent from real-provider/platform acceptance. No manual backend or protocol edits were made in this repair.

## Activity stylesheet composition

Reran `node /private/tmp/oss-1182-layout-check.cjs fixed` with the complete composed stylesheet, bundled Playwright, installed Chrome and an owned headless browser context. All five bounded fixture assertions passed: short desktop and dialog content fills the lower Activity area; long content retains scroll travel and reachable Apply; hidden Activity leaves the other outlet's layout unchanged. Header/footer color and the existing 8px list bottom padding remain intact. The browser context closed after verification. This synthetic CSS fixture uses no account or business RPC state and is not the packaged app.

## Evidence and limits

Local logs remain at `/private/tmp/oss-1182-0851-full.log`, `/private/tmp/oss-1182-0851-go.jsonl` and `/private/tmp/oss-1182-0851-layout-fixed.json`. Focused merge records retain all 70 and 66 frontend tests plus 58 CI-script tests in `main-c49b0-merge-validation.md` and `main-82020-merge-validation.md`. Earlier attempts and results remain unchanged in their original records.

These checks do not establish packaged CEF geometry/focus, actual 200% zoom, Windows/X11 visuals, real-provider lifecycle or measured Windows CI improvement. Preserve the original Activity acceptance limits in `activity-sidebar-validation.md`. Remote CI and Codex review must evaluate the new pushed head; observations for the previous head are invalidated by the push.
