# Issue #1088 replacement and permanent-deletion integration

## Source and scope

Base inspected and fetched: `ad0e3e9a29cb3d8375ab5d168bb160c35a023250`.
The issue remained open; PRs #1127 and #1173 were closed without merging.
This replacement reconciles the retained feature at
`ef9585fb7b25e7677cf596bb8514efc926346fda` with the base's permanent-session
removal. Historical issue evidence is retained unchanged.

The main-established reservations remain EntityKind 31, SystemCapability 14 and
WorkerCapability 4. Service-specific sources regenerate all Go, TypeScript,
Connect Query and compatibility bindings together. SQLite remains schema 24.

Deletion atomically requests original terminal closes. Exact retries keep the
same close identities. The independent workspace-removal lane withholds its
work until every terminal has confirmed joined process cleanup, and final SQL
purge independently rechecks that barrier. Original terminal cleanup reports
remain admissible during deletion. New shells and input cannot reopen it.
Controlled server and SQLite tests cover this ordering, receipt replay, direct
purge refusal and preservation of a sibling session's terminal.

The desktop Send line control appends carriage return (native Enter) to the
original UTF-8 draft. Unix PTY and Windows ConPTY fixtures use that control;
raw CLI/Connect byte input remains unchanged. Native Windows execution remains
unperformed locally.

## Executed successful checks

- Frozen root `pnpm install`; only required DeliDev LFS assets hydrated.
- `pnpm proto:lint` and `pnpm proto:breaking` through `pnpm proto:check`.
  Initial freshness comparison failed because the implementation was still
  uncommitted; regeneration succeeded. Post-commit freshness is recorded below
  only after execution.
- `pnpm ci:contracts`: 113 passing tests. `pnpm ci:workflows`: passed.
- API-client `pnpm test`: four files / 44 tests; package typecheck passed.
- Root `go vet ./cmds/delidev-cli/...`: passed before the final test additions;
  repeated final validation is recorded below after execution.
- Initial focused terminal/deletion tests passed in terminal, store, server and
  Worker packages. The race-enabled terminal pass later passed Worker, store,
  server and CLI while process/workspace failed as described below.
- `go test -race ./cmds/delidev-cli/internal/server -run
  '^TestSessionDeletionJoinsTerminalBeforeWorkspaceRemoval$' -count=1`: passed.
- `go test -race ./cmds/delidev-cli/internal/store -run
  '^TestPermanentSessionDeletionRequiresOriginalTerminalCleanup$' -count=1`:
  passed.
- Final desktop typecheck and `pnpm exec vitest run
  src/session-terminals.test.tsx --maxWorkers=1`: three tests passed, including
  native Enter bytes, exact creation retry, reattachment and split UTF-8.
- Desktop eight packaging tests, 16 asset/launcher tests, native Swift widget
  fixtures and the production build passed. A final build after the Enter
  change is recorded below after execution.

## Unsuccessful and incomplete checks

The full desktop `pnpm test` and an independently bounded
`pnpm exec vitest run --maxWorkers=2` both encountered many Settings, sidebar,
backup and integration failures/timeouts and were interrupted. Neither is a
passing full frontend suite. The machine's observed load averages exceeded 600;
this explains an investigation constraint, not proof that every failure is
unrelated or a baseline failure.

`GOMAXPROCS=2 go test -race -p 2 -timeout=20m ./cmds/delidev-cli/...`
encountered CLI recovery/pairing/session failures and native harness discovery
failures before interruption. It is not a passing full Go race suite.

The focused terminal race pass failed
`TestTerminalCloseJoinsOwnedDescendants` and the Local/Worktree portions of
`TestTerminalDirectoryUsesOriginalPrimaryAndDoesNotTakeAgentLease`.
Three isolated descendant reruns failed; a later whole terminal-process rerun
passed TTY, resize, native Enter and output drain but still failed descendant
close. These native failures remain unresolved; no production ownership or
cleanup deadline was weakened.

A temporary diagnostic terminal fixture observed original coalition membership
fall from three to one and confirmed joined cleanup in one run. Its source was
removed. That positive variant does not supersede the original failed fixture.
A separate temporary package copied the exact main process sources from the
base above and passed `TestOwnedDaemonizedDescendants` (all five subcases) and
`TestCancellationAndForeignOwnerRecovery`. It reused current domain/security
imports, contained no PTY feature code and was removed after execution. It does
not establish that the failed PTY fixture is a baseline failure.

One intermediate server/store check failed to compile a temporary test edit
that referenced an unavailable fixture field. That edit was removed and replaced
by the separate SQLite regression; the final focused server and store checks
above passed.

Native Windows/Linux, physically remote Worker, native desktop visual and
release acceptance are unperformed. Cross-compilation is not native execution.
The desktop remains a bounded text/control view, without full-screen VT emulation.
Generated repository-owned dist directories are removed before delivery.

## Final checks completed before the integration commit

- Repeated root `go vet ./cmds/delidev-cli/...`: passed with the final server and
  SQLite regression sources.
- Final desktop production build after the Enter correction: passed.
- Linux amd64/arm64 and Windows amd64 CLI builds and process-test compilation
  passed with `GOMAXPROCS=2`, `CGO_ENABLED=0` and package parallelism two.
  Windows arm64 remained in progress at this commit; its result is recorded
  separately after completion. No foreign binary was executed.
