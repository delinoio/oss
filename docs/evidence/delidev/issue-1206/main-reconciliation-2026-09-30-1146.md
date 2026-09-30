# PR #1212: repository-inspection reservation reconciliation

## Inspected state

The 11:46 UTC maintenance pass on 2026-09-30 inspected PR #1212 at
`74512cdd9edcdd4c14136f6ce6032776e6ef46e3`. It was open and conflicting, with no
unresolved Codex review threads or failing reported checks. The current-head code
review was complete; its security review was running. This was not complete CI
or merge approval evidence.

Fetched main at `d1f83cecee4e0c50ea094335392cf68845f85739` includes PR #1214's
main-established repository-inspection metadata reservation at Worker capability
6, plus the Activity, workflow evaluation, Runner Device and OpenCode changes
merged since the previous repair. The sole textual merge conflict was in the
allocation ledger; discovery's pending Worker capability also used 6.

## Resolution

Merge that main revision without rebasing. Keep the established repository
inspection and compaction entries unchanged. Discovery's server capability
remains 16; move only its pending Worker capability from 6 to 7. Retain the ledger's
original branch number 5 and PR #1212 provenance. The prior 15/5 and 16/6
reservation evidence remains historical in the independent evidence files.

Update the current discovery contract, protocol contract and scoped protocol
instructions together. Discovery remains unimplemented: no active schema member,
RPC, generated discovery binding, runtime capability or migration is activated.
The PR retains `Refs #1206`; these reservations still need to reach main before
dependent implementation.

## Validation

- All six allocation/structure checks passed after resolving the ledger.
- `pnpm ci:contracts`: all 113 checks passed.
- `pnpm proto:check`: formatting, lint, breaking compatibility and forced
  generated-source freshness passed without drift.
- An independent ledger comparison confirmed main baseline/entries unchanged
  and discovery absent from active schemas, generated bindings and runtime code.
- `go test -race -timeout=3m ./cmds/delidev-cli/internal/worker -run
  '^TestStreamTerminationCancelsRunningOwnedWork$' -count=1 -v`: all four cases
  passed (13.718 seconds including race-process overhead).
- `go vet ./cmds/delidev-cli/...`: passed.
- `pnpm test` from `apps/delidev`: passed client build/type checking, all 98
  unit-test files / 1,264 tests, eight package fixtures, sixteen launcher/asset
  fixtures, native Swift widget checks and production build. The consumed LFS
  icon remained hydrated.
- Required administrator and async-commit-hook embedded asset builds passed
  before the repository Go-format hook. Generated `dist` directories are removed
  after committing.
- Relative documentation links, conflict-marker absence and `git diff --check`
  passed.

## Limits

The earlier complete local Go race failures remain visible in
[the stream fixture evidence](ci-stream-termination-fixture.md). This pass does
not repeat those blocked broad runs or claim that focused checks replace them.
Inherited main features retain their own native acceptance evidence and limits;
this reconciliation does not provide new native Codex discovery, hosted-account,
subscription or Windows/Linux product acceptance. CI and review must evaluate
the new pushed head.
