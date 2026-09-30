# Issue #1095: isolated controls for broader race-run failures

Feature source: `20e9a04f2b2cde1b33cad880aaf3c2a5e5aa1822`.
Baseline: an independent temporary `git archive` of incorporated main
`98df29c41ddf3c8b1274c51fe8f406b6dae6ca74`, containing the original Go module,
DeliDev command source and generated Go protocols. No checkout state, credentials
or generated distribution is copied into these controls. Earlier records remain
unchanged.

## CLI workspace fixture

The broader race run failed `TestCLISessionAcceptanceQueueAndArchive` at
`sessions_test.go:215`, reading a tracked workspace file, with the existing
unavailable file-reader diagnostic. An isolated feature run failed at line 258,
creating a review through that reader (46.79-second case; 48.331-second package).
The same isolated command in the unchanged-main archive failed at the same
line 258 and operation (49.16-second case; 51.215-second package):

```sh
GOMAXPROCS=4 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/cli \
  -run '^TestCLISessionAcceptanceQueueAndArchive$' -count 1
```

One attempted baseline invocation accidentally remained in the feature working
directory and failed at line 239's creation diff. It is a second feature run,
not baseline evidence. The corrected baseline command ran from the independent
archive. The baseline demonstrates the review-file failure, not an exact
reproduction of the broader run's earlier line-215 operation or a proven cause
for every reader failure. The original earlier baseline diff failure remains
separately recorded in `replacement-2026-09-30.md`.

## Codex Steer fixtures

The broader race run failed
`TestSteerInspectionConfirmsExactHistoryWithoutReplay` in its `late-steer` and
`ready` cases, and `TestSteerInspectionDefiniteRejectionNeverNeedsHistoryOrResends`
in `steer-unsupported`. Diagnostics respectively reported missing retained
attempt evidence, uncertain delivery and an operation timeout. These fixtures
use their original 70 ms request deadline; it was not modified.

```sh
GOMAXPROCS=4 go test -race -p 1 -timeout 5m \
  ./cmds/delidev-cli/internal/harness/codex \
  -run '^TestSteerInspection(ConfirmsExactHistoryWithoutReplay|DefiniteRejectionNeverNeedsHistoryOrResends)$' \
  -count 1
```

Both complete test functions passed on isolated feature retry (4.147 seconds)
and in the unchanged-main archive (3.993 seconds). Their test source is unchanged
against that main revision. This is a successful isolated control, not a
complete Codex package pass or a baseline reproduction of the full-run failures.

The broader race run's completed outcome is recorded separately. These controls
add no real-account OAuth/inference, installed-native/platform/release or complete
uncertain-lease recovery acceptance.
