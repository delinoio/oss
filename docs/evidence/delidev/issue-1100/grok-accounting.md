# Issue #1100: verified Grok closed-input accounting

## Source and implementation

Implemented from main `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` in
`9993bd318` on 2026-09-30. The closed, unmerged PR #1108 at
`5c75fb674d3de72be6ec590909cb8ddb996e18db` supplied issue-scoped implementation
and review corrections; unrelated merges and the frozen evidence ledger were
not imported. The replacement uses current service-specific schemas, scoped
instructions, the reserved capability/fields and migration 25.

One immutable `GrokClosedInput` unit is retained in the original verified
completion transaction after matching original input/turn, closed user history,
source response, native closure and independently confirmed workspace cleanup.
Response dimensions remain separate. A response reporting input 11/output 5
and a closed-input total of 16 contributes 16, never 32. Maximum uint64 values
remain exact, and sums use arbitrary precision. Original assignment attribution
and receipt/source references survive replay and restart. Pricing, actual cost
and estimated-budget contribution remain unavailable.

Explicit `NATIVE_UNITS_V1` summary reads expose separate Codex/Grok unit kinds
in overall, session/account/provider/model, daily and model views. Legacy
response fields, costs, ranking and coverage retain their original meanings.
CLI and desktop require the profile echo. Desktop preserves project IDs,
exact clipped half-open interval bounds, mounted filters and stale disclosure.

Migration 25 creates future-only native accounting after a synchronized backup,
without backfill. Its independent metadata layout marker rejects unmarked old
version-25 databases before any write, including an otherwise identical table.
Frozen historical fixture conversion excludes that future marker while keeping
retained user records. The new migration shares the existing ordered registry;
reserved migrations 26 and 27 remain unimplemented.

## Executed verification

Host: macOS arm64, Go 1.26.8, Node.js 24.11.0, pnpm 10.26.2. Tests use temporary
SQLite/Git/process state and controlled credentials/providers. The exact DeliDev
LFS icon was hydrated before source-consuming frontend checks.

- `go test -race -p 1 -timeout=10m ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli -run 'Accounting|UnmergedVersion25|EveryHistoricalSchema|MigrationDefinitions' -count=1`:
  passed all four packages (1.539, 101.495, 21.757 and 2.358 seconds). Coverage
  includes cleanup gating, exact receipt replay, legacy/missing/altered history,
  partial/pre-text/raced Stop exclusion, zero/uint64 precision, mixed unit kinds,
  rollback/restart, CLI/capability negotiation, all frozen historical layouts and
  unchanged-byte rejection of unknown version-25 files.
- `go vet ./cmds/delidev-cli/...`: passed.
- `pnpm proto:check`: passed formatting, lint, FILE compatibility and byte-for-byte
  generated Go/TypeScript/compatibility-view reproduction.
- `pnpm --filter @delinoio/delidev-api-client test`: passed 4 files / 44 tests.
- `pnpm ci:contracts`: passed all 102 tests, including reserved wire assignments.
- Client build and focused `vitest run src/grok-accounting.test.tsx src/usage.test.tsx`:
  passed 2 files / 12 tests.
- Required full frontend `pnpm test` was attempted. The first concurrent run was
  interrupted after unchanged device/tray/notification fixture failures. The
  complete rerun with `VITEST_MAX_WORKERS=1` passed 83 files / 956 tests and failed
  10 tests in unchanged `App.test.tsx` and `settings.test.tsx`: eight five-second
  timeouts and two missing-control waits. Those failures remain visible; no
  product behavior or fixture deadline was changed to conceal them.
- The frontend checks after Vitest were run independently: all 8 bundle dry-run
  fixtures, all 16 desktop-launch/asset fixtures, native Swift widget fixtures,
  and the production web build passed. These do not turn the failed full pipeline
  into a complete-suite pass.
- Normal commit hooks passed. The required administrator and ach Go embed outputs
  were generated explicitly for the repository-wide Go formatting hook.

## Remaining limits

The complete `go test -race -p 2 -timeout=20m ./cmds/delidev-cli/...` run is still
running and has already failed unchanged CLI/configuration-transfer/workspace
and harness-discovery fixtures. Its final outcome is recorded in the follow-up
validation record after completion. No complete local Go race pass is claimed.
The full local frontend pipeline has not passed. Concurrent repository test load
was observed; it is not proof that every failure is environmental or pre-existing.
No hosted account, installed Grok inference, native desktop visual inspection,
other-platform execution or release/distribution acceptance was performed. These
fixture/build results do not establish those acceptance boundaries.
