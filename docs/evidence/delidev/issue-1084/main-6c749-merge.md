# Issue #1084: compose native forks from main

On 2026-09-30 the repair pass merged main `6c749670727b30679e722821846bc8dc00f5ac32` into proxy head `49feceaa855c3fc2b10c453356a2c54d06c02ec7`. Main adds native Codex forks from #1224. This is a local merge, without rebasing or merging the GitHub PR.

Conflicting owner rules and project invariants retain both features. System status advertises proxy capability 6, fork capability 13 and every existing capability together; the existing status test now verifies both numbers and advertisement. CLI dispatch retains the independent 145-second fork and 35-second network/restore bounds. Buf regenerated shared Go/TypeScript bindings from the reconciled source schema; no generated merge side was selected.

Validation on the merged tree:

- `pnpm proto:generate`: passed after formatting `system.proto`. The first lint attempt caught an extra conflict-resolution blank line; Buf formatting corrected it, without suppressing a check.
- `pnpm proto:lint` and `pnpm proto:breaking`: passed.
- `GOMAXPROCS=2 go test -race -p 4 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/cli ./cmds/delidev-cli/internal/domain -run 'Network|Fork|StatusPreserves|StoppedAccountSwitchStatus' -count=1 -timeout=8m`: all four packages passed (57.804s, 9.740s, 19.854s and 5.817s respectively).
- Client typecheck and `pnpm --filter @delinoio/delidev-api-client test`: passed, six files / 47 tests.

These temporary database and generated-client fixtures establish composition, not installed native fork, real account, enterprise proxy or cross-platform acceptance. The preceding combined validation's full-suite CLI/Grok failures remain recorded independently; this focused pass does not resolve them.
