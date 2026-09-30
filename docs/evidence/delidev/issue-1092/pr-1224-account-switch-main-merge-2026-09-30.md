# PR #1224: account-switch and ALLGREEN main reconciliation

## Source revisions and conflict resolution

The 2026-09-30 13:38 UTC maintenance pass began on PR head
`c2338205ba5aace86231a06182abc65623d8b0eb`. GitHub then reported conflicts
with newly advanced main `98df29c41ddf3c8b1274c51fe8f406b6dae6ca74`.
This checkout merges that exact main revision without rebasing.

Independent Codex fork, stopped-account-switch and ALLGREEN ownership/contract
additions are retained together. The SessionService schema preserves Fork,
GetSessionFork and SwitchSessionAccount; System capabilities retain fork wire
value 13 and account-switch value 5. Main's native-model-discovery reservations
remain inactive. All Go, TypeScript and Connect Query bindings are regenerated
from the reconciled schemas rather than selected from a conflict side.

Main extracts continuation construction into `continuationAssignment`. The
fork-specific clearing of the creation seed moves into that shared constructor,
so later child turns use their own predecessor completion while account switching
preserves its separate original account/connection binding. Server status
advertises both implemented capabilities. The native CLI fixture retains both
profile selectors, each profile's bounded deadline, account-switch candidates,
and asynchronous source creation only for the fork profile.

## Executed verification before the merge commit

- Protocol generation, format/lint and breaking compatibility passed.
- Root `GOMAXPROCS=2 go vet -p=1 ./cmds/delidev-cli/...` passed.
- All 44 DeliDev client tests passed.
- Required desktop `VITEST_MAX_WORKERS=1 GOMAXPROCS=2 pnpm test` passed:
  100 files / 1,279 tests, 8 bundle tests, 16 asset/desktop-launch tests,
  widget fixtures, explicit client build, TypeScript and production build.
- Opted-in installed Codex 0.151.0 CLI acceptance passed in 28.660 seconds:
  `DELIDEV_NATIVE_THREAD_EXECUTABLE=/private/tmp/delidev-1092-codex/codex GOMAXPROCS=2 go test -p=1 ./cmds/delidev-cli/internal/cli -run '^TestManualNativeCLI(SessionFork|AccountSwitch)$' -count=1 -timeout=15m`.
  Temporary keyless loopback fixtures verify both full-history account switching
  and General Chat/multi-repository forks with child process replacement.
- The race-enabled filtered DeliDev run has passed server (114.550 seconds),
  store (9.611 seconds) and Worker (5.127 seconds), including fork/account-switch
  and continuation composition. Its workspace portion is still running at this
  commit; the subsequent CI-repair record will retain the final result.
- `git diff --check` passed. No Rust code changed.

## Separate CI repair and limits

The Windows Worker failure on the preceding PR head is
`TestSessionForkInspectionCannotClaimFailedRuntimeRemoval`; CI Result reflects
that same failure. Its platform-dependent failure fixture is addressed separately
after this conflict repair, not hidden by the merge. No installed Windows,
hosted-account or packaged-desktop acceptance is inferred from these local runs.
Earlier broad Go limitations remain in their historical records. Fresh pushed-head
CI and Codex reviews must be assessed on the next heartbeat.
