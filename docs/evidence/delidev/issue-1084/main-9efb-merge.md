# Main backup-restore merge evidence

Issue #1084 / PR #1216. Merge base before this pass: `98df29c41ddf3c8b1274c51fe8f406b6dae6ca74`. Repair parent: `c054a882d9476c465bf97cbd01ed72aa081f0ffa`. Reconciled main: `9efb1917e0127a9223cee0969877238ab37c0e1d` (managed backup restore, PR #1222).

## Reason and reconciliation

The first `pnpm proto:check` passed formatting/lint but failed breaking compatibility against the advanced main baseline because this branch lacked the newly merged backup-restore messages, RPCs, field and capability 7. No check or protocol rule was weakened.

Merged main without rebasing. Ten conflicts covered additive AGENTS/command documentation, CLI response timing, server status and generated System bindings. Retained both sets of ownership rules and distinct command sections. Network and backup inspection/restore both retain their 35-second client waits around 30-second server operations. System status advertises proxy 6, restore 7 and switch 5 alongside every prior capability; composition tests cover their independent presence. `system.proto` retains all additive declarations. Go/TypeScript/Connect bindings were regenerated with `pnpm proto:generate`, preserving legacy reflection compatibility.

## Focused validation

- `pnpm proto:generate`, `pnpm proto:lint` and `pnpm proto:breaking` passed after source reconciliation.
- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/cli ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/outbound -run 'Network|CLINetwork|BackupRestore|Restore|StatusPreserves|StoppedAccountSwitchStatus' -count=1 -timeout=10m` passed: server 39.971s, store 146.645s, CLI 25.326s, domain 2.001s, outbound 2.498s.
- `pnpm --dir packages/delidev-api-client typecheck` passed.
- `pnpm --dir packages/delidev-api-client test --maxWorkers=2` passed: 6 files, 47 tests.
- `git diff --check` passed. No source conflict markers remain.

Canonical protobuf freshness and the complete DeliDev Go validation run follow the merge commit. Prior native/account/proxy/Worker acceptance limits remain unchanged; this merge records composition fixtures, not real-environment acceptance. Incoming main's separately justified Windows Grok probe test deadline is preserved as merged main evidence, not presented as a proxy-repair change.
