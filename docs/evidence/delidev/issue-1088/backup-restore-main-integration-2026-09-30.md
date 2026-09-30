# Managed restore main integration

PR #1226 merged main `9efb1917e0127a9223cee0969877238ab37c0e1d` into terminal head `dbfbd8f5094f82afcf94b7a09f579bcb91401a91`.

Both managed restore capability 7 and terminal capability 14 remain advertised alongside the existing capabilities. The server ownership instructions retain both sets of rules. The automatically combined canonical system schema owns both features; Go and TypeScript bindings were regenerated with `pnpm proto:generate`.

Executed checks:

- `pnpm proto:lint` and `pnpm proto:breaking`: passed.
- API client build and test: passed, 5 files / 46 tests.
- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/server -run 'TestStatusPreservesForwardingAndRestoreCapabilities|TestStoppedAccountSwitchStatusPreservesExistingCapabilities|TestTerminalReceiptsOutputReattachAndArchiveBarrier|TestSessionDeletionJoinsTerminalBeforeWorkspaceRemoval'`: passed, 6.292 seconds.

These are merge-focused checks, not complete native or release acceptance. Existing qualified validation records are preserved.
