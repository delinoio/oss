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
- Initial broad parallel validation exposed a native cleanup timeout and
  desktop fixture/readiness timeouts under simultaneous native/Go/frontend
  test load. These initial runs are not passing evidence. Full bounded reruns
  and protocol freshness checks are recorded below after completion.

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
