# Issue #1094 merged-source validation, 2026-09-30

## Revision and composition

The inspected implementation revision is `dc46ec717`, which merges main
`7090de04621ece95b3c2cce8d88fcfcdeadad7cc` into the replacement branch.
It preserves main's Grok accounting schema, generated usage model and capability
alongside native subagent observations. Generated protocol conflicts were resolved
by regenerating reconciled source schemas. Shared entity/capability reservations
remain unchanged; this feature introduces no database migration.

Historical issue evidence is preserved. This file records later execution without
rewriting the historical ledger or treating closed unmerged PRs as implementation
on main.

## Completed checks

- `GOMAXPROCS=4 go vet ./cmds/delidev-cli/...`: passed.
- `pnpm proto:check`: passed, including lint, breaking compatibility and generated
  freshness. Its first merged-source attempt failed because the Go proxy could
  not resolve; the retry completed without generated drift.
- `pnpm ci:contracts`: all 113 tests passed.
- Focused race command: all six packages passed on this revision:

  ```sh
  GOMAXPROCS=4 go test -race -p 2 \
    ./cmds/delidev-cli/internal/domain \
    ./cmds/delidev-cli/internal/harness/codex \
    ./cmds/delidev-cli/internal/harness/claude \
    ./cmds/delidev-cli/internal/server \
    ./cmds/delidev-cli/internal/worker \
    ./cmds/delidev-cli/internal/cli \
    -run 'Subagent|ChildHistory|ChildTask|ClaudeTask|ClaudeChild|CanonicalCollaboration|GrokAccounting' \
    -count=1 -timeout=8m
  ```

  The checks cover native ownership and read-only inventory, missing spawn
  notifications after parent completion, selected Claude leaf ancestry and exact
  description/depth binding, late task patches, omission-preserving receipts,
  requested/observed model separation, non-additive child usage, atomic ownership
  rejection, replay, late completion and independent cleanup. The Grok accounting
  selection also checks composition with the merged main behavior.
- After explicitly rebuilding `@delinoio/delidev-api-client`, frontend typechecking
  passed. The complete Vitest suite passed with two workers and a 15-second test
  deadline: 98 files / 1,247 tests. Packaging dry-run fixtures (8), desktop-launch
  and asset fixtures (16), widget fixtures and production Rsbuild completed.
  These runner overrides are explicit; this is not a claim that the unrestricted
  default test invocation passed.

## Broad test limitations and earlier repairs

The required default `pnpm test` was executed in `apps/delidev` and failed in
Vitest: 20 files / 64 tests failed, 77 files / 1,176 tests passed. The following
bounded-worker attempt passed 1,239 of 1,240 tests, with one Settings test exceeding
its default five-second deadline. A later run overlapped the main merge and
observed mixed generated/source models; it is discarded as revision-pinned
validation. The stable merged-source frontend sequence above completed all checks.

The initial required full backend race run reported failures in
`TestCLIConfigurationTransferThroughRealWorker` (import remained queued) and
`TestCLISessionAcceptanceQueueAndArchive` (workspace preparation remained pending).
The run also overlapped source repairs and the main merge; it was stopped rather
than presented as verification of one revision. A stable merged-source full race
run is being completed separately and its result will be recorded below.

Initial new fixture failures were repaired before the successful focused run:
the Codex multi-read fixture has a bounded one-minute process lifetime, its
resolved-activity fixture initializes the control semaphore and uses a bounded
context, and the Worker child-model fixture assigns its model after the shared
helper replaces content. Earlier interrupted or mixed-source runs are not counted
as green verification. Concurrent native, Go and frontend suites were observed on
this host; contention alone does not prove every failure is environmental or
pre-existing.

## Acceptance limits

Required source assets were hydrated with `git lfs pull`. Workspace installation
and explicit client/ach/administrator builds prepared ignored generated output
before Go checks. No Rust source changed. Fixtures use isolated temporary state
and no user credentials. Real-provider accounts, native-platform installation and
release acceptance were not performed. Generated repository-owned dist directories
will be removed after checks and commit hooks finish.
