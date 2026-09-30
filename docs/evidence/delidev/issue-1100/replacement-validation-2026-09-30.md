# Issue #1100 current replacement validation

Implementation: `12905b1f1c6ce4c28e0cc7451a239f250f10cc8f`, based on main
`ad0e3e9a29cb3d8375ab5d168bb160c35a023250`. This record follows
[the replacement provenance](replacement-current-main-2026-09-30.md); prior PR
results in this directory remain historical evidence.

## Completed checks

- Required `pnpm proto:check`: passed formatting/lint, FILE breaking checks and
  exact regeneration of current Go/TypeScript/compatibility outputs after commit.
- Focused domain/store/CLI race tests:
  `go test -race -p 2 -timeout 5m ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/cli -run 'Accounting|UnmergedVersion25|EveryHistoricalSchema|MigrationDefinitions'`:
  all three passed (1.317 / 180.917 / 5.206 seconds). Historical layouts converge
  on fresh schema 25; unidentified prior v25 files remain preserved.
- Focused frontend `pnpm exec vitest run src/grok-accounting.test.tsx src/usage.test.tsx --maxWorkers=1`:
  passed 2 files / 13 tests, including exact uint64 display, zero/unavailable,
  mixed unit kinds, native-only model exclusion and original project attribution.
- Required full `VITEST_MAX_WORKERS=1 pnpm test` from `apps/delidev`:
  completed with exit 1. Type checking passed; Vitest reported 92 passed and 4
  failed files, with 1,239 passed / 3 failed / 1 skipped tests. The failures were
  unchanged Settings integration fixtures: Claude model-option lookup, renamed
  GitHub profile lookup, new Server preferences lookup, and temporary-server
  readiness in the Devices suite. No fixture deadline or product behavior was
  modified to hide these failures.
- Separately executed `pnpm test:bundle-dry-run`, `pnpm test:desktop-launch`,
  `pnpm test:widget`, `pnpm build`: all passed. This covers 8 package fixtures,
  16 launcher/asset fixtures, native Swift widget fixtures and production build;
  it does not turn the failed full frontend command into a pass.
- Hook prerequisites `pnpm --filter devhud-admin build:embedded` and
  `pnpm --filter async-commit-hook build:embedded`: passed. The implementation
  committed with the normal Go formatting hook.

- Targeted retry of the four failed frontend files using explicit
  `--maxWorkers=1` and unchanged fixture deadlines: 3 files / 3 tests passed;
  GitHub integration still failed waiting for the initial profile rename control.
  Claude, Devices and Server preferences passed. This is qualified retry evidence,
  not a full-suite pass.
- Isolated main comparison from a `git archive` of
  `ad0e3e9a29cb3d8375ab5d168bb160c35a023250`, containing only root Go module
  files, the DeliDev Go component and generated Go protocols:
  `GOMAXPROCS=4 go test -race -p 1 -timeout 3m ./cmds/delidev-cli/internal/cli ./cmds/delidev-cli/internal/harness -run '^(TestCLISessionAcceptanceQueueAndArchive|TestDiscoveryVerifiesGrokWithoutExecution)$' -count=1`:
  both packages failed. Grok discovery reproduced the same protocol-validation
  failure. The CLI fixture failed earlier, with a Worktree preparation wait
  timeout, so this does not reproduce the replacement's precise file-reader error.
  It demonstrates that the fixture also fails on unmodified main under this host's
  concurrent workload; the original file-reader failure remains unresolved.

Go vet, 44 generated-client tests and 113 CI-contract tests are recorded in the
preceding provenance record. The full Go race run has already reported the CLI
workspace-reader and Grok discovery failures; remaining native harness packages
are still running. No complete Go race pass is claimed.

## Maintenance

Non-draft PR: https://github.com/delinoio/oss/pull/1211. Its body contains
`Closes #1100`; base `main`, head `kdy1/issue-1100-grok-accounting`.
The first maintenance observation found it open and mergeable, with required CI
and Codex review pending and no review threads. Automation
`maintain-grok-usage-pr-1211` is active in this chat every five minutes. No merge or
auto-merge was requested or performed.

All state/provider/native fixtures remain isolated. No real Grok account,
installed native inference, native desktop visual, other-platform or release
acceptance is claimed.
