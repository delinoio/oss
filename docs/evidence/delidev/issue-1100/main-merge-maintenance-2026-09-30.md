# PR #1184: main reconciliation and validation on 2026-09-30

## Revisions and composition

Maintenance began with the PR open at
`4d94007de4b1f38a752c63a9aac5397843c145cb`, with merge conflicts and no
unresolved, non-outdated Codex review threads. The clean issue worktree and
existing branch were retained. This pass invoked the installed repair-pr skill.

Merged main `faa7fbee6b6e2a1e77b45e169417c7de5c00530d` in
`b608ef43709a17903522885e54dda43ebce9088d`. The sole textual conflict was in
the desktop contract: retained both the complete Grok accounting paragraph and
the new PR Activity section. Preserved schema 25's Grok layout marker and the
ordered migration reservations; main's PR Activity additions compose with the
accounting ledger. Protocol validation regenerated reconciled split schemas
and confirmed the checked-in generated outputs without drift.

Main advanced during validation. Merged its home-sidebar update at
`2b658e05353a858e328f82a636d8bc49dbccb709` in
`d4ea5a57bce6bd715dfe545f862e21c488e7f032`, preserving its navigation and
focus contracts. Moved the Grok paragraph verbatim into the existing Usage
section to avoid another incidental conflict at the shared document's end.
Its preserved SHA-256 is
`dd3f23b22d78821b7a8d258c8ad59b72a6e1c5c6940e9c12e80c24f914e58c11`.
Normal commit hooks passed. No user changes, branch identities or closing
references were replaced.

## Passed local checks

Executed from the issue worktree on macOS arm64 with Go 1.26.8, Node 24.11.0
and pnpm 10.26.2. Fixtures used private temporary state.

- `go test -race -p 1 -timeout=10m ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli -run 'Accounting|UnmergedVersion25|EveryHistoricalSchema|MigrationDefinitions|PRActivity' -count=1`
  passed all four packages: domain 2.523s, store 96.178s, server 28.441s and
  CLI 3.030s. Covers accounting/cleanup/replay, historical migrations and
  composed PR Activity retention/projection.
- `go vet ./cmds/delidev-cli/...` passed.
- `pnpm proto:check` passed format, lint, FILE breaking compatibility and
  generated-source freshness after the first merge. The later sidebar merge
  changes no protocol or Go source.
- `pnpm --filter @delinoio/delidev-api-client test` passed all 4 files /
  44 tests after the merges.
- `pnpm ci:contracts` passed all 111 tests, including the imported Windows
  package-shard contracts.
- `VITEST_MAX_WORKERS=1 pnpm test` from `apps/delidev` passed after the first
  merge: all 89 files / 1,056 tests, generated-client build, typecheck, 8 bundle
  dry-run fixtures, 16 desktop-launch/asset fixtures, native Swift widget
  fixtures and production web build.
- Repeated the same complete frontend command after the sidebar merge. It
  passed all 89 files / 1,069 tests (140.63s Vitest stage) and all the remaining
  stages listed above. The source icon was already hydrated. Generated
  repository-owned dist directories were removed after validation.

## Unresolved broad Go and Windows evidence

`GOMAXPROCS=2 go test -race -p 1 -failfast -timeout=10m ./cmds/delidev-cli/...`
exited 1. API proxy passed in 1.871s; CLI failed in 106.089s at
`TestCLISessionAcceptanceQueueAndArchive`, whose creation-diff read returned
`Unavailable` with the workspace file reader unavailable (fixture 31.08s).
Failfast stopped the queue before later packages ran. This is not a complete
Go race pass. The fixture and workspace-reader implementation were unchanged
by this PR. Concurrent host test runs were observed; this does not establish
the cause or justify treating the failure as environmental. Earlier attempts
remain in [the original broad-validation record](full-race-validation.md).

Inspected the completed
[Windows job](https://github.com/delinoio/oss/actions/runs/36683553533/job/109784229417)
from the prior PR head `babbabb653dd16f8bca95eac14b8792729e5f747`.
It failed `TestDetachedWorkerLifecyclePreservesRegistrationAcrossStopAndRestart`
after 80.78s: the replacement never connected and its retained controller
state was exited. The job log did not expose the replacement's private
structured-log reason. The dependent CI Result failure has the same upstream
job dependency; neither is current-head evidence after the maintenance push.

Ran the exact lifecycle fixture separately on this Mac with
`GOMAXPROCS=2 go test -race -p 1 -timeout=3m ./cmds/delidev-cli/internal/cli -run '^TestDetachedWorkerLifecyclePreservesRegistrationAcrossStopAndRestart$' -count=1`.
It passed in 53.996s. This does not disprove the Windows failure or establish
its cause. The main merge imports four Windows test shards with serial package
execution; their fresh native results remain required before reporting CI
success. No fixture deadlines or lifecycle authority rules were weakened.

New pushes invalidate earlier CI and review evidence. Pending checks/reviews
are left for the next scheduled pass. Local fixtures/builds do not establish
hosted-account, installed Grok, native desktop visual, Windows/Linux product,
or release/distribution acceptance.
