# Issue #1088: Worker-owned session terminals

## Implementation boundary

The issue branch starts at main revision
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7`. It adapts the terminal implementation
from closed, unmerged PR #1127 (head
`045d15159496c2c9093cb931219dc20d3da02555`) to the current service-specific schema,
Go source ownership and immutable wire allocations. This record describes new
checks on the replacement branch; the older PR's validation is retained history.

Terminal shells live on the session's paired execution Worker, with original
workspace identity, native PTY/ConPTY ownership, receipt-bound operations,
ephemeral ordered bytes and independent cleanup confirmation. The desktop uses
a bounded text output view, line input, control bytes, resize and reattachment;
it does not emulate full-screen VT applications.

## Validation, 2026-09-30

Implementation revision: `15bc792c3` (evidence-only updates follow this revision).

All commands use the isolated issue worktree and temporary fixture state. No
user provider account or paired production Worker is used. The desktop's
consumed icon LFS object was hydrated before validation. Required API-client
`dist` output is generated explicitly and removed after checks.

- Root `pnpm install --frozen-lockfile`: passed, including linked-worktree hooks.
- API client build, typecheck and tests: passed, four files and 44 tests.
- `node --test scripts/ci/delidev-proto.test.mjs`: passed all three checks for
  declaration ownership, immutable numeric assignments and semantic breaking
  comparison after relocation.
- Native descendant cleanup: three consecutive isolated race-enabled runs
  passed using `GOMAXPROCS=2 go test -race -p 1 -timeout=90s
  ./cmds/delidev-cli/internal/process -run TestTerminalCloseJoinsOwnedDescendants
  -count=3 -v`.
- Root `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...`: passed.
- Root `pnpm proto:check`: passed lint, semantic breaking comparison and complete
  regeneration without tracked or untracked generated-source drift.
- Root `pnpm ci:contracts`: passed all 102 checks.
- Desktop focused terminal tests: passed all three checks for split UTF-8, gaps,
  exact creation retry/reattachment, keyboard focus, byte input and resize, using
  one Vitest worker. The added UI fixture carries schema version 1 and compares
  decoded byte values across JavaScript realms.
- Linux and Windows amd64/arm64 CLI builds and process test binaries: cross-compiled
  successfully with `CGO_ENABLED=0`, `go build -p 1` and `go test -p 1 -c`.
  Foreign test binaries were not executed.
- Initial broad parallel validation exposed a native cleanup timeout and
  desktop fixture/readiness timeouts under simultaneous native/Go/frontend
  test load. These initial runs are not passing evidence. The final broad runs
  and independent follow-up outcomes are detailed below.
- Desktop package dry runs: eight checks passed. Desktop launch/asset checks:
  16 passed. Swift widget fixtures and the production frontend build passed.
  Repository-generated desktop/API-client `dist` directories were removed.

## Broad-suite status

`VITEST_MAX_WORKERS=1 GOMAXPROCS=2 pnpm test` in `apps/delidev` finished with
960 passed and three failed tests across 85 files. The failures were the App
notification/import draft-close timeout and Settings preferences/pricing
integration waits. All three new terminal UI checks passed. The later package,
launch, widget and build stages were run independently because the unit failures
short-circuited the combined command. These results do not make the full desktop
suite passing; its three failures have not been independently reproduced on main.

The required root race command was launched as
`GOMAXPROCS=2 go test -race -p 2 -timeout=20m ./cmds/delidev-cli/...`.
The CLI acceptance fixture and three Grok/OpenCode discovery cases failed.
All four reproduced in a disposable unchanged-main checkout at
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7` with race instrumentation,
`-p 1 -timeout=5m -count=1` and the exact test-name selection. That establishes
baseline failures for those four cases only. The temporary baseline checkout was
removed after its checks. The full race run subsequently hit 20-minute timeouts
in the unchanged Claude and Codex harness packages; remaining packages are still
running at this record's publication. The full run is failing, not passing.

A separate exact-prefix terminal run with `-race -p 1 -timeout=5m -count=1`
passed the server, Worker, store and CLI packages, including all-three-capability
negotiation and duplicate rejection. It failed native descendant cleanup and
Local/multi-repository Worktree directory verification. An isolated recheck of
those two cases also failed. Three earlier isolated cleanup runs passed, but that
success does not resolve the later failures. General Chat, native TTY/resize,
receipt/replay, byte-stream/gap, Stop/Archive and sibling-cleanup fixtures remain
retained coverage; their presence must not be substituted for a fully passing
native suite. Native verification is unresolved and needs follow-up on a clean
host or additional diagnosis. No timeout, ownership or workspace validation was
weakened to manufacture passing results.

## Codex review repairs

The Archive lookup finding is reproduced by corruption and history-bound
regressions: both incorrectly returned success before the repair. Archive
completion now defers only the expected live-terminal `RecoveryRequired` state
and propagates other errors. The corruption fixture proves that the terminal
report and receipt roll back, then that its exact request ID completes Archive
after the retained record is repaired. Race-enabled terminal Archive and
terminal/forward cleanup-order checks pass using an isolated temporary Go cache,
`-p 2 -timeout=3m -count=1`. An earlier build did not execute the tests because
shared Go cache files disappeared during linking; it is not test evidence.

## PR maintenance

Non-draft PR [#1173](https://github.com/delinoio/oss/pull/1173) targets main and
retains `Closes #1088`. Its five-minute heartbeat is active as
`maintain-delidev-pr-1173` in the owning chat. The first maintenance pass found
mergeable Git history with pending CI and running Codex review; that is not CI
success or review approval. Evidence-only pushes invalidate earlier head checks.

## Evidence limits

Native macOS fixture execution, generated-client/Connect fixtures and
cross-compilation must be distinguished from actual Windows ConPTY, Linux PTY,
a physically remote Worker, native desktop visual acceptance and signed release
acceptance. Those real environments have not been exercised in this run.
No Rust code is changed by this issue.

## References

- [Terminal contract](../../../cmds-delidev-terminals-contract.md)
- [Issue #1088](https://github.com/delinoio/oss/issues/1088)
- [Retained PR #1127](https://github.com/delinoio/oss/pull/1127)
