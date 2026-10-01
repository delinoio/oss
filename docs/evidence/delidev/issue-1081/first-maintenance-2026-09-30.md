# Manual PR fixes: first maintenance pass

PR: #1227. Source revision: `61f97c3a9`, merging main `65eca3341` into the
manual-fix branch on 2026-09-30. The first PR check found documentation conflicts
after main advanced; no unresolved Codex review thread or failed CI check was
present in the initial repair inventory.

## Reconciliation

Preserved manual-fix instructions together with main's Activity filter treatment,
repository-inspection metadata reservations and pinned required-workflow profile.
The integration scope retains the remaining workflow, merge-queue and
provider-resolution limits while describing implemented manual handling. New
manual-fix execution-device messages follow main's Runner Device terminology;
technical Worker Git authentication terminology and all identifiers remain intact.
Bindings were regenerated from the reconciled sources without drift. No shared
wire numbers or migration reservations were changed by this repair.

## Completed verification

- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/domain -run 'ManualPRFixRPC|PRFix|RemediationWorkflow|RequiredWorkflow' -count=1 -timeout=10m`:
  passed both packages (36.824 and 1.442 seconds).
- `pnpm test:unit src/pr-fix.test.tsx src/github-workflows.test.tsx src/activity-sidebar.test.tsx --maxWorkers=1`
  from `apps/delidev`: passed all three files and 23 tests.
- `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...`: passed, both immediately
  before this main reconciliation and again after it.
- `pnpm proto:check`: passed after reconciliation, including lint, breaking and
  generated-client freshness.
- The original current-main serial frontend rerun finished with 1,242 passing
  tests and six skipped tests; all five manual-fix UI cases passed. Nine suites
  failed: four 120-second build preparations, four 15-second teardown hooks and
  one temporary server readiness failure. This is not a complete frontend pass.
- Removed the seven generated repository-owned `dist` directories created for
  compilation/frontend checks, preserving dependency directories. No tracked
  generated output or new credentials were introduced.

## Outstanding verification

The original all-package race rerun started at `0ce9f8e06` remains running. It has
already reported failures in unrelated CLI recovery/forwarding fixtures and
Claude/Codex lifecycle fixtures. Main reconciliation occurred after that run
started, so its eventual remaining package results cannot prove one immutable
current-branch snapshot. Do not report a full-suite pass or start duplicate
background copies. CI and Codex review must be assessed against the eventual
pushed head, independently of earlier successful checks. Original local failures
and real-account/platform limits remain in `local-validation-2026-09-30.md`.
