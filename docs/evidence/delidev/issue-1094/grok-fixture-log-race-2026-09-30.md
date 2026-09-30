# Grok fixture failure-log race during PR #1225 validation

## Original evidence

The full backend race command began at merge revision `368e3419031d9b59ca1d11e66636d09fab1f729e`. Its Grok package reported an actual race between the owned-process exit logger writing a `bytes.Buffer` (`process.Start.func1`, `process.go:136`) and `TestOriginalPlanControllerPreservesUncertainty` reading `logs.String()` from failure cleanup (`plan_reply_test.go:177`). The original initialization failure at line 183 allowed the late process-exit log to overlap the test's diagnostic snapshot. This is a proven fixture race, separate from the unproved native initialization, delivery and cleanup failures.

## Repair

The shared `fixtureConfig` and `fixtureAPIConfig` now return a private test-only synchronized log buffer. Both the logger's Write and diagnostic String hold the same mutex; snapshots retain all original structured log lines. Production process ownership, logging, protocol validation, claims and cleanup are unchanged. No test deadline or assertion was relaxed. No dependency, protocol, storage or product-policy change is introduced.

`TestFixtureLogSnapshotDuringNativeExit` exercises concurrent real slog writes and diagnostic snapshots. Every nonempty snapshot row must remain complete JSON, and the final snapshot must retain all 256 original exit records. Before the fix, this controlled regression failed under the race detector (1.304s) with the same writer/read race. After the fix, the standalone regression passed with verbose confirmation of the named test.

## Executed checks and limits

- Red: `GOMAXPROCS=4 go test -race -p 2 ./cmds/delidev-cli/internal/harness/grok -run '^TestFixtureLogSnapshotDuringNativeExit$' -count=1 -timeout=20m` failed with a data race before the sink change.
- Green: the same command with `-v` passed after the sink change; the complete package compiled and the named concurrent regression ran.
- `GOMAXPROCS=4 go vet ./cmds/delidev-cli/internal/harness/grok` passed.
- Combined regression plus the original `planning-valid` case used `-run '^(TestFixtureLogSnapshotDuringNativeExit|TestOriginalPlanControllerPreservesUncertainty)$/^planning-valid$'`. It failed (21.967s) because original native initialization still returned `RecoveryRequired`: request delivery is uncertain. Failure cleanup printed complete logs without another race report. This does not establish that the complete Grok suite passes.

The ongoing broad command compiled Grok before this test-fixture repair and recorded the original race and a twenty-minute package timeout. Its results remain historical evidence for the merge revision; focused post-fix checks prove only the synchronized log sink. The broader failure remains unresolved and is not retried without new evidence.
