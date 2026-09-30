# Final first-maintenance reconciliation

The first repair of PR #1222 also merged the subsequent main snapshot
`65eca3341e2180f676fc81c24ebccbc82b67f344`. It includes the already merged
Runner Device terminology, Activity filters, pinned-workflow inspection and
repository-inspection reservations. Preserve them in full; the only additional
conflict was additive protocol-owner guidance, composed with restore guidance.
No schema or generated declaration changed after the schema-25 reconciliation.

## Completed checks

- Final protocol reservation/relocation tests passed all three cases.
- `pnpm proto:check` passed format/lint, breaking compatibility and exact
  regeneration freshness from committed schema-25 outputs.
- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/domain
  ./cmds/delidev-cli/internal/integrations/github ./cmds/delidev-cli/internal/server
  -run 'Workflow|RequiredCI|Rules|BackupRestorePreservesOriginalGrok|StatusPreservesForwardingAndRestore|Schedule'
  -count=1 -timeout 15m` passed domain (1.494 s), GitHub (8.536 s) and server
  (77.566 s). This repeats the original native-accounting restore/deletion and
  combined-capability regressions with the final server source.
- Final `GOMAXPROCS=2 go vet ./cmds/delidev-cli/...` passed. One prior invocation
  used the frontend working directory and failed to find the Go package path;
  it was corrected to the repository root before reporting success.
- `GOMAXPROCS=2 pnpm test:unit --maxWorkers=2` passed all **1,264 tests in
  98 files**, retaining every existing deadline. Final frontend typecheck and
  production build passed. The default-concurrency `pnpm test` failures from
  the first main snapshot remain recorded in `main-merge-574c1a92.md`.
- The first reconciliation's remaining frontend pipeline also passed:
  bundle dry-run (8 tests), desktop-launch/asset fixtures (16 tests), widget
  fixtures and production build. Those scripts did not change in the later
  main merge. These are controlled fixtures/builds, not installed native
  desktop or distribution acceptance.
- Client tests remained 46/46; their generated source did not change in the
  second merge. No source asset or original license/notice was changed.
- Removed the generated, ignored frontend/client `dist` directories after
  validation. No generated `dist` is tracked or retained in this worktree.

## Pending broad run

The original immutable implementation's full race run (session 94932) still
continues through the remaining packages. Its store package also reached the
20-minute timeout, during fixed historical-schema convergence, after the CLI,
Codex/Grok fixture failures and server timeout already recorded. A complete
race pass is not claimed. The final focused schema-25 restore/accounting tests
passed independently (see the preceding evidence file); no fixture deadlines
were loosened, broad native failures suppressed, or unperformed platform/account
acceptance inferred. Record the original command's terminal outcome separately
before considering another full run.

Five-minute maintenance remains active in this chat. Its chosen checkout is the
isolated `restore-1222-repair` worktree; preserve the original detached checkout's
source until its outstanding command exits. Never merge or enable auto-merge.
