# Codex forks main integration

Merged main `6c749670727b30679e722821846bc8dc00f5ac32` into the terminal
branch based on `6327a38675dc732a329dc2c5400c5e112594273e`.

The session header retains both the capability-gated Fork action and the
terminal panel toggle. Server ownership rules retain both feature contracts.
System status advertises existing capabilities plus Codex forks (13) and
session terminals (14), preserving main's number reservations. Generated Go and
TypeScript bindings were regenerated together from reconciled canonical schemas.
All other fork implementation, documentation and validation changes from main
remain in this merge.

Executed verification:

- `pnpm proto:generate`, `pnpm proto:lint`, and
  `pnpm --filter @delinoio/delidev-api-client build`: passed.
- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/server -run
  'Test(StatusPreservesForwardingAndRestoreCapabilities|StoppedAccountSwitchStatusPreservesExistingCapabilities|SessionForkPublishesOneIndependentChildAndDoesNotCopyQueuedInput|SessionForkDeletionRequiresUnpublishedForkToSettle|SessionDeletionJoinsTerminalBeforeWorkspaceRemoval)$'
  -count=1`: passed (44.259 seconds).
- From `apps/delidev`, `pnpm typecheck` and
  `pnpm exec vitest run src/session-terminals.test.tsx src/session-fork.test.tsx`:
  passed, two files and nine tests.
- `git diff --check`: passed; no unresolved merge entries remained.

These are controlled implementation checks. They do not establish installed
native desktop, remote account or other-platform acceptance. Full repository
validation follows after the independent close-intent review repair.
