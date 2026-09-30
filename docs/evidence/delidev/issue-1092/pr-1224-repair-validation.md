# PR #1224: repair validation

## Revisions and repair scope

Main conflict resolution merged `65eca3341e2180f676fc81c24ebccbc82b67f344`
without rebasing. Review repairs preserve source-history cleanup, independent
Local/worktree lifetime and the ordinary Worker clock gate. Final implementation
revision: `944241713bb2f0240b3003738bf72c28cfe1469c`.

The three handled Codex threads and focused evidence are recorded separately:

- `PRRT_kwDORRAKg86ng0Y1`: rejected source inspection and all later pre-native
  runtime rollback, with possible native state retained.
- `PRRT_kwDORRAKg86ng0Y9`: Local sharing only from user-owned Local checkouts.
- `PRRT_kwDORRAKg86ng0ZE`: future-dated Worker observations cannot claim input.

The frozen historical ledger is untouched. No protocol number, migration version,
generated binding or Rust source changes are introduced by these repairs.

## Passing validation

- Required desktop `pnpm test` after the Local presentation change passed:
  99 files / 1,268 tests, 8 bundle tests, 16 asset/desktop-launch tests, widget
  fixtures, TypeScript checking, explicit client build and the
  production Rsbuild bundle. The preceding merge-only invocation had 1,265
  passes and a workspace-fixture Go-build timeout; that fixture passed on retry.
- Root `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/... -run Fork
  -count=1 -timeout=4m` passed on `c84e8283ed4b0cacc4c054fbdc80b4d3d51815f7`:
  server 24.411 seconds, Worker 2.288 seconds, workspace 228.244 seconds, with
  Codex/CLI/domain/GitHub tests also passing. Packages without matching tests are
  not presented as having exercised their broader suite.
- The final runtime-rollback implementation passed all Worker fork regressions
  in 1.938 seconds, including definite workspace rejection and preservation of
  unjoined inspection / possible native-child state.
- Final root `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...` passed after the
  runtime-rollback extension. Protocol lint/check/reproduction and 6
  protocol/structure CI-contract tests passed against the reconciled schemas.
- The opted-in installed native acceptance passed on the final implementation:
  `DELIDEV_NATIVE_THREAD_EXECUTABLE=/private/tmp/delidev-1092-codex/codex
  GOMAXPROCS=2 go test -p 1 ./cmds/delidev-cli/internal/cli -run
  '^TestManualNativeCLISessionFork$' -count=1 -timeout=8m` (25.385 seconds).
  This uses pinned Codex 0.151.0, temporary server/Worker/native homes and a
  scripted keyless loopback provider. General Chat and two dirty repositories
  publish independent children, retain exact request replay and source-only
  queued input, and continue the child across process replacement. Fork itself
  performs no inference. No user credentials or hosted provider are used.

## Broad Go limitations

A fresh unfiltered `GOMAXPROCS=4 go test -race ./cmds/delidev-cli/...` completed
with exit 1 at `c84e8283ed4b0cacc4c054fbdc80b4d3d51815f7`, before the final
pre-native workspace rollback extension. CLI failed its existing
`TestCLISessionAcceptanceQueueAndArchive` workspace-diff read with Unavailable.
The same assertion failed in an isolated retry; main's independent issue #1136
evidence also reproduces availability failure in this fixture on untouched base
`ad0e3e9a29cb3d8375ab5d168bb160c35a023250`, without establishing that every
broad failure has the same cause.

The following packages reached their unchanged ten-minute package deadlines:

- Grok: `TestInitialPlanRequiresOriginalClaimAckAndMode/mode-wrong`.
- Server: `TestClaudeInterruptionRejectsMissingChangedAndUnownedEvidenceAtomically/arrival`.
- Worker: `TestOpenCodeWorkerCheckpointPinsOriginalAssignmentAndCompletion/native-input`.
- Workspace: `TestLocalPreparationRecoveryPreservesReadyCheckout/committed`.

These are package deadlines, not proof that each named currently running case
ran for ten minutes. No whole-suite Go pass is claimed. An earlier broad run
overlapped source edits and produced mixed-revision compilation evidence; it was
superseded and is not used to validate the final implementation. The final
rollback extension has its fresh focused race and installed-native evidence above.
Do not infer Windows/Linux native, hosted-account or packaged-desktop acceptance.

Generated repository-owned `dist` directories are validation inputs only and
are removed after their consumers finish. A push invalidates the previous PR
head's CI/review evidence; new checks and review are assessed on the next heartbeat.
