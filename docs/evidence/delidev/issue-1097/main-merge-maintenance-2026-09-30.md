# First PR maintenance: compose current main capabilities

## Inspected revisions

PR #1218 initially published head
`fcc21667f0e2fe640e06e3114a6e846bf00f36b2`.
Its first maintenance pass merged freshly fetched main
`574c1a92c957fc741a723ff8123888dad32a2194` on 2026-09-30.
That main includes Grok native accounting (#1211) and shared native-compaction
reservations (#1215). The earlier validation and failed attempts remain in
`current-main-reconciliation-2026-09-30.md`; neither historical record is rewritten.

## Composition

- Retain all current System capabilities: automatic titles, forwarding,
  user services, native accounting (4), permanent deletion (9), and explicit
  stopped Codex API account selection (5). The status regression now checks
  accounting together with the existing capabilities and duplicate refusal.
- Retain both accounting and account-selection rules in the scoped CLI, protocol
  and client instructions and protocol contract. Preserve main's implemented
  schema-25 accounting migration and independent future compaction reservations.
  This account-selection change adds no migration or reservation.
- Reconcile canonical schemas and regenerate Go/TypeScript/Connect Query output
  with `pnpm exec buf format -w` and `pnpm proto:generate`. Do not select a merge
  side for generated descriptors. Existing permanent-deletion and selection
  queries remain present alongside the additive accounting response.

## Executed checks before the merge commit

- `GOMAXPROCS=2 go test -race -p 1 -timeout 5m
  ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/apiproxy
  ./cmds/delidev-cli/internal/cli
  -run 'Test(AccountSwitch|StoppedAccountSwitch|HistoryObservation|SwitchedHistory|FullNativeHistory|CLISwitch|GrokAccounting)'
  -count=1` passed: server 186.556 seconds, apiproxy 1.879 seconds,
  CLI 1.749 seconds. This includes the pending permanent-deletion regression,
  history-mode observation, fresh explicit Resume and original usage attribution.
- `GOMAXPROCS=2 go test -race -p 1 -timeout 5m
  ./cmds/delidev-cli/internal/server
  -run 'Test(MixedCodexGrokAccounting|NativeAccountingCapability)' -count=1`
  passed in 11.698 seconds, retaining distinct accounting kinds and capabilities.
- `GOMAXPROCS=2 go vet ./cmds/delidev-cli/...` passed.
- `pnpm proto:lint` and `pnpm proto:breaking` passed.
- `node --test scripts/ci/delidev-structure.test.mjs
  scripts/ci/delidev-proto.test.mjs scripts/ci/proto-breaking.test.mjs` passed
  all seven allocation, structure and schema-baseline checks.
- `pnpm --filter @delinoio/delidev-api-client test` passed all 44 tests in four
  files. Its build and the desktop typecheck also passed before the frontend run.
- From `apps/delidev`, `pnpm exec vitest run src/usage.test.tsx
  src/grok-accounting.test.tsx --maxWorkers 1` passed all 13 tests in both files.
- `pnpm --filter devhud-admin build:embedded` generated and validated the real
  administrator embed required by the root Go formatting hook. This is build
  preparation, not product runtime acceptance.

## Limits

The required frontend `pnpm test` ran from `apps/delidev` and failed during Vitest:
19 failed and 75 passed files, 23 failed and 1,209 passed tests, seven skipped tests,
and three unhandled errors. Observed errors included test/hook and temporary
server readiness timeouts, fork-worker startup timeouts, and a missing temporary
fixture executable. It completed after 2,322.64 seconds. Later chained packaging,
widget and frontend build steps did not run. The isolated accounting component
pass does not establish a successful full frontend gate.

The prior full Go race failures/interruption and both installed Codex discovery
failures remain recorded and unresolved. The successful merge-focused race gate
supersedes the previous failed focused repeat for this reconciled source, but
cannot establish a successful full suite or native A-to-B acceptance. No product
or test deadline was relaxed and no unrelated test behavior was changed.

Generated repository-owned `dist` directories are removed before delivery.
GitHub check/review evidence on the original head does not apply to the new push.

## Committed-source verification

The conflict repair is `bc9c38c1902605b5dba3391581ef9e0ae32cc2f0`, with
parents equal to the original published head and the inspected main above.
Root Lefthook Go formatting passed for that commit. `pnpm proto:check` then
passed formatting/lint, baseline breaking comparison, forced Turbo regeneration
and generated-source freshness with no tracked or untracked drift.

Before the final repair push, GitHub reported no unresolved Codex review threads
and no failing reported checks; its single successful external check belonged to
the earlier head. No Codex review or approval was present. Those observations do
not establish CI, review or merge readiness for the new head.
