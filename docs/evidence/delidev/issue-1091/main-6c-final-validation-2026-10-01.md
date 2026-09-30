# Final Codex Fork base validation, 2026-10-01

Issue: [#1091](https://github.com/delinoio/oss/issues/1091). PR:
[#1230](https://github.com/delinoio/oss/pull/1230). Executable source is merge
`95c652da12e08bf7180951ceb34c65ce19f42bc4`, incorporating main
`6c749670727b30679e722821846bc8dc00f5ac32` after the earlier restore base.
[The merge record](main-6c-reconciliation-2026-10-01.md) retains its conflict
composition, distinct Grok review commits and focused race results. This final
record preserves failures without replacing earlier evidence.

## Passed checks

All tests use private temporary resources without user credentials or inference.
Go uses `GOCACHE=/tmp/oss-1091-go-cache`.

- `pnpm proto:check` passed the complete lint/breaking/forced-generation/freshness
  command with no tracked or untracked generated-source drift.
- `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...` and
  `GOMAXPROCS=2 go build -p 1 -o /tmp/oss-1091-review-1455-main6c-delidev ./cmds/delidev-cli` passed.
- The exact eight-package focused race selection in the merge record exited 0;
  seven packages ran matching tests, and store had no matching selected test.
  Server fixtures exercise the original fork/restore storage boundary.
- API-client tests passed all five files and 46 tests.
- Desktop client build and typecheck passed before the default unit phase.
  Separate remaining checks passed eight bundle fixtures, 16 launch/asset
  fixtures, widget fixtures and production build.
- The unchanged `settings.test.tsx` file passed all 49 tests in isolation using
  `pnpm exec vitest run src/settings.test.tsx --maxWorkers=1` and original deadlines.

## Frontend failures

The required default `pnpm test` from `apps/delidev` failed its unit phase:
10 files/54 tests failed, while 91 files/1,242 tests passed. A complete unit rerun
with `pnpm exec vitest run --maxWorkers=2` passed 100 files/1,295 tests but failed
one 5,000 ms timeout at `settings.test.tsx:723`, the unfiltered provider-picker
case. Its file was not edited by this merge. The isolated file subsequently
passed, but this neither proves the failures' cause nor establishes a passing
complete default command. No test or production deadline was weakened.

## Broad Go failures and incomplete run

The final-source root command was:

```sh
GOMAXPROCS=4 go test -race -p 2 -timeout=20m ./cmds/delidev-cli/...
```

Five packages completed successfully, one had no tests and three completed with
failures before the remaining run was stopped (exit 143):

- CLI, 430.413 seconds: `TestCLIPairWorkerAndInspectRealRepository` timed out
  during repository inspection at `devices_test.go:138`; the session acceptance
  fixture timed out waiting for Worktree creation at `sessions_test.go:202`.
- Harness, 199.605 seconds: Grok and OpenCode discovery fixtures reported
  unconfirmed probe cleanup at `discovery_grok_test.go:99` and
  `discovery_opencode_test.go:105`.
- Claude, 988.649 seconds: valid API-stream and explicit-failure checkpoint
  fixtures reported uncertain request delivery; the valid private-init probe
  reported unconfirmed cleanup; blocked-input streaming returned a timeout
  classification instead of the expected retained uncertainty.

The four initially reported CLI/discovery cases were run once in isolation:

```sh
GOMAXPROCS=2 go test -race -p 1 -timeout=20m ./cmds/delidev-cli/internal/cli ./cmds/delidev-cli/internal/harness -run '^(TestCLIPairWorkerAndInspectRealRepository|TestCLISessionAcceptanceQueueAndArchive|TestDiscoveryVerifiesGrokWithoutExecution|TestDiscoveryVerifiesOpenCodeWithoutExecution)$' -count=1
```

Both selected packages failed (CLI 105.601 seconds, harness 55.475). Inspection
retained recovery-required uncertainty, Worktree creation timed out, Grok
verification failed, and OpenCode cleanup remained unconfirmed. These test files
and the discovery implementation have no diff from the earlier restore base.
They are not claimed as cleared or conclusively unrelated to the current source.

Machine load was repeatedly above 300 during the failures and remained severe
after isolation. That is an observed limitation, not proof of causality. After
repeat failures, terminate only this task's broad Go process and its owned test
children rather than repeatedly running the same blocked checks. Its original
native supervisors exited; unrelated worktrees' processes were left untouched.
Codex/Grok and later broad packages did not complete on this final source, so the
command is explicitly incomplete and failed. The earlier complete restore-base
race result and positive full Grok/server/store/Worker/workspace results remain
separate evidence in [the earlier record](review-repair-validation-2026-10-01.md).
No further local retry is claimed without new evidence.

## Publication limits

Normal hooks passed. Required generated embeds preceded compilation/hooks;
repository-owned generated `dist` output is removed afterward and remains
untracked. Preserve the standalone `Closes #1091` reference and existing PR body.
Push all separate repairs and base reconciliations once, then resolve only the
two supported fixed P2 threads. The original automatic outside-workspace Read
review remains unresolved pending the previously requested policy decision.
Current-head CI/reviews await the next registered heartbeat; no merge, auto-merge,
confinement, risk acceptance or real-account/platform approval is claimed.
