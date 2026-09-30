# Issue #1095: complete validation after initial maintenance

This independent record completes the validation following
[`initial-maintenance-main-2026-09-30.md`](initial-maintenance-main-2026-09-30.md)
for [PR #1233](https://github.com/delinoio/oss/pull/1233). The tested source is merge
commit `a91ff7f266929eb258c51065df8f5d3ac83a1db5`, which incorporates main
`d1f83cecee4e0c50ea094335392cf68845f85739`. The following evidence-only commit does
not change implementation or generated bindings.

## Executed checks

Runtime: Go 1.26.8, Node.js 24.20.0 and pnpm 10.26.2 on macOS arm64.

- `GOMAXPROCS=4 go test -race -p 2 -timeout=30m ./cmds/delidev-cli/...`:
  completed successfully. Every tested package passed, including the complete
  CLI, Claude, Codex, Grok, OpenCode, server, storage, subscription, Worker and
  workspace packages. Server completed in 720.852 seconds, Worker in 313.047
  seconds and workspace in 537.970 seconds. This is a complete package-suite
  pass, not an isolated retry or an interrupted run.
- `GOMAXPROCS=4 go vet -p 1 ./cmds/delidev-cli/...`: passed.
- `pnpm proto:check`: passed formatting/lint, breaking comparison and forced
  generation freshness without drift.
- `GOMAXPROCS=4 go test -p 1 ./protos/...`: passed the reflection/contract packages;
  generated binding packages compiled and reported no test files.
- `GOMAXPROCS=4 pnpm --filter @delinoio/delidev-api-client typecheck`: passed.
- `GOMAXPROCS=4 pnpm --filter @delinoio/delidev-api-client test`: all 44 tests passed
  across four files, including the existing Go-built Connect integration fixture.
- The six focused race-tested packages and six allocation/structure tests passed
  before the merge commit, as recorded in the preceding maintenance record.
- `git diff --check`: passed. Repository-owned generated `dist` prerequisites
  were removed after their formatting-hook use. No generated distribution is
  included in this change.

## Earlier observations and limits

The failed full run and narrowly established unchanged-main CLI reproduction in
[`replacement-2026-09-30.md`](replacement-2026-09-30.md) remain preserved. This
later complete pass uses package parallelism two and a thirty-minute package
deadline; it does not change fixture assertions or native operation deadlines.
The earlier CLI/workspace source is unchanged by incoming main between
`574c1a92c957fc741a723ff8123888dad32a2194` and
`d1f83cecee4e0c50ea094335392cf68845f85739`, as confirmed by an empty Git diff over
those two directories. The later pass does not retrospectively erase failures or
establish a particular cause for their timing differences.

Managed-authentication evidence continues to use controlled synthetic fixtures.
No real subscription OAuth, hosted inference, installed-native managed execution,
desktop login controls, complete uncertain-lease recovery, Windows/Linux native
acceptance or release readiness is claimed. Complete issue #964 remains separate.
CI and review for the eventual published maintenance head require their own
observations; local validation cannot approve that head on GitHub.
