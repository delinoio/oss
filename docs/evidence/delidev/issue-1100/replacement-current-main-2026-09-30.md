# Issue #1100 replacement against current main

## Source and scope

Base inspected: `ad0e3e9a29cb3d8375ab5d168bb160c35a023250` (freshly fetched
`origin/main` on 2026-09-30). Issue #1100 remains open. PRs #1108 and #1184
were closed without merging; this replacement reuses the preserved implementation
and review fixes from #1184 at `4a71d21a9ffa07b00a85e0f18a504c01b83a16a9`.
The other files in this evidence directory retain those historical runs and their
original qualifications; they are not validation performed by this replacement.

Reconciliation preserves main's permanent-session-deletion capability and existing
source erasure, regenerates bindings from the current split schemas, and activates
only the already reserved Grok migration 25 and protocol allocations. Claude 26 and
diagnostics 27 remain unimplemented. The project index and owning instructions
retain the distinct-unit, cleanup, immutable attribution and no-pricing invariants.

A new regression uses a real retained verified Grok input to test its original
session foreign-key deletion boundary. Capability coverage now also checks main's
permanent-session-deletion capability alongside native accounting.

## Executed verification before the implementation commit

Host: macOS arm64, Go 1.26.8. All fixtures use isolated temporary state and controlled
providers. The exact DeliDev icon LFS object was hydrated before frontend checks.

- `pnpm install --frozen-lockfile`: passed, including linked-worktree hooks.
- `pnpm proto:generate`, `pnpm proto:lint`, `pnpm proto:breaking`: passed.
- `go vet ./cmds/delidev-cli/...`: passed.
- `go test -race -p 2 -timeout 5m ./cmds/delidev-cli/internal/server -run 'GrokAccounting|MixedCodexGrokAccounting|NativeAccountingCapability'`:
  passed (20.566 seconds). Includes original cleanup/history/source checks, replay,
  zero/maximum uint64, overlapping response exclusion, Stop/Archive races,
  mixed Codex/Grok kinds, transaction rollback, restart and session-source erasure.
- `pnpm --filter @delinoio/delidev-api-client test`: passed 4 files / 44 tests.
- `pnpm ci:contracts`: passed all 113 tests, including allocation reservations and
  migration ownership.
- `git diff --check`: passed.

The complete Go race suite, focused historical-schema/CLI accounting checks and
required full frontend `VITEST_MAX_WORKERS=1 pnpm test` were started and remain
pending at this record's initial commit. Final results will be recorded separately.
No hosted Grok account, installed native inference, native desktop visual,
other-platform or release acceptance is claimed.
