# Failed execution-job diagnostic recovery repair

## Source and behavior — 2026-09-30

Repair base: `1fabab7d`, PR #1166. This record accompanies the repair for
review thread `PRRT_kwDORRAKg86na_yK`.

The lost failed-report recovery fixture already checked the session diagnostic.
Adding an exact execution-job diagnostic comparison reproduced the review defect:
`go test -race ./cmds/delidev-cli/internal/server -run '^TestClaudeLostFailedReportRecoveryPreservesOriginalFailure$' -count=1`
failed with `recovery erased the failed execution job diagnostic`.

Recovery finalization now copies the preserved session problem into the original
failed job, matching direct completion reporting. The original failed completion,
session problem, confirmed cleanup, paused dispatch, absent next-input intent and
single recovery receipt remain independently checked.

## Executed validation

Passed with task-private temporary Go module/build caches:

`GOFLAGS=-p=1 GOMAXPROCS=2 go test -race ./cmds/delidev-cli/internal/server -run '^(TestClaudeLostFailedReportRecoveryPreservesOriginalFailure|TestClaudeRecoveryRechecksOriginalSettledBoundary)$' -count=1`.

The package passed in 39.236 seconds. `git diff --check` passed. These temporary
server/native-history fixtures grant no new installed-native, hosted-account,
desktop-platform or release acceptance.
