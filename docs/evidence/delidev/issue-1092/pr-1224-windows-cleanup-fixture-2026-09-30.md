# PR #1224: Windows cleanup-failure fixture repair

## Failure and revisions

On head `c2338205ba5aace86231a06182abc65623d8b0eb`, the
[Windows Worker shard](https://github.com/delinoio/oss/actions/runs/36721624119/job/109908027441)
failed only `TestSessionForkInspectionCannotClaimFailedRuntimeRemoval`. The
fixture supplied a missing descendant beneath a regular file and assumed
cleanup must return RecoveryRequired. Windows instead classified that child as
absent, so the helper preserved the original Unsupported inspection result.
The shard's workspace package passed. CI Result separately reported only the
unsuccessful `go-test` dependency, not another repair cause.

Main conflict composition is committed independently as
`48971c25618728a6305b2fce386a77e088448247`, merging main
`98df29c41ddf3c8b1274c51fe8f406b6dae6ca74`. Its
[merge record](pr-1224-account-switch-main-merge-2026-09-30.md) preserves the
original before-commit observations.

## Fixture change

The shared test still requires RecoveryRequired and verifies retained content.
Its Unix fixture preserves the original foreign-file/parent-sync failure case.
Its Windows fixture creates a private real runtime and retains an owned native
file handle without delete sharing, so actual removal is blocked until cleanup
releases that handle. This uses the documented
[CreateFile sharing contract](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createfilew).
The handle closes before temporary-directory teardown. There is no skipped
Windows assertion, injected production failure, or production cleanup change.
Ownership, policy and wire contracts are unchanged; no AGENTS/index policy
amendment is required for these platform test fixtures.

## Executed verification

- The complete filtered merge run passed:
  `GOMAXPROCS=2 go test -race -p=1 ./cmds/delidev-cli/... -run 'Fork|AccountSwitch|Continuation|ALLGREEN|AllGreen|QueueCI' -count=1 -timeout=20m`.
  Server took 114.550 seconds, store 9.611, Worker 5.127 and workspace 186.506.
  Packages with no matching tests remain explicitly outside broader coverage.
- Fresh Worker fork regressions after the fixture change passed in 2.643 seconds:
  `GOMAXPROCS=2 go test -race -p=1 ./cmds/delidev-cli/internal/worker -run 'Fork' -count=1 -timeout=4m`.
- Final root `GOMAXPROCS=2 go vet -p=1 ./cmds/delidev-cli/...` passed.
- Windows amd64 Worker tests compiled and Windows-target Worker vet passed with
  `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 GOMAXPROCS=2`, using `go test -p=1 -c`
  and `go vet -p=1`. The first compile overlapped generated-file replacement and
  failed on temporarily absent `zz_delidev_compat.go`; the sequential retry after
  generation completed passed. The ignored temporary binary is not committed.
- Full `pnpm proto:check` passed, including generated freshness, along with all
  6 protocol/structure Node contract tests. No generated drift remains.
- The merge's required desktop suite passed 1,279 tests plus bundle, asset,
  desktop-launch, widget, type and production-build checks; all 44 client tests
  passed. Installed Codex fork and account-switch CLI acceptance passed using
  temporary keyless loopback fixtures. Their commands are in the merge record.
- `git diff --check` passed. Generated repository-owned `dist` outputs are removed
  after validation.

## Limits

Cross-compilation and vet do not execute the native Windows assertion. Its actual
Windows result must come from fresh CI after the single final push. No unfiltered
local Go-suite pass, installed Windows/Linux account acceptance or packaged-desktop
acceptance is claimed. Earlier broad local Go limits remain recorded. The frozen
historical evidence ledger and closing reference are preserved; monitoring stays
active and evaluates fresh pushed-head CI/review on the next scheduled heartbeat.
