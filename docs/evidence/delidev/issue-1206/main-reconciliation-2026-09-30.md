# PR #1212: reconcile main-established reservations

## Source and conflict

The final one-shot repair inventory on 2026-09-30 reported a new merge conflict.
The fetched main revision `574c1a92c` includes Grok accounting from PR #1211 and
the shared compaction reservation prerequisite from PR #1215. Main now owns
`SystemCapability` 15 and `WorkerCapability` 5 for native session compaction.
PR #1212's unpublished discovery reservations had selected those same numbers.

## Reconciliation

Merge main into the existing issue branch without rebasing. Preserve all four
main-established compaction reservations and both planned contracts. Move only
the pending native Codex model discovery entries to server capability 16 and
Worker capability 6, recording their previous branch numbers in the ledger.
Follow main's single-original-owner rule by using PR #1212 as their provenance;
the domain contract and PR retain issue #1206's ownership and `Refs` reference.

Update the discovery contract, protocol contract and scoped protocol instructions
together. The discovery contract now recognizes main's implemented Grok schema
25 and leaves pending versions 26 and 27 unchanged. Initial validation evidence
remains historical, with a link to this reconciliation. No discovery schema,
binding, migration, RPC, runtime capability or native implementation is activated.

The automatic merge retains main's Go/frontend Grok accounting implementation
and its generated protocol bindings. These inherited changes are validated
separately from the stream-cancellation fixture repair and from the still-pending
native model discovery feature.

## Validation

- All six allocation/structure tests pass on the reconciled ledger.
- `pnpm ci:contracts`: all 113 checks passed.
- `pnpm proto:check`: format, lint, breaking comparison against the fetched main
  revision and forced binding regeneration/freshness passed without drift.
- DeliDev client type checking passed.
- Selected accounting/migration/capability/CLI race tests passed in domain, store,
  server and CLI packages (1.535, 9.244, 66.950 and 3.824 seconds). The repaired
  stream-termination race fixture passed again in Worker (15.929 seconds). The
  command used `-race -p=1 -timeout=5m` with explicit test-name selection; this is
  focused merge coverage, not a complete Go suite pass.
- `go vet ./cmds/delidev-cli/...`: passed on the merged sources.
- `pnpm test` from `apps/delidev`: passed client build, desktop type checking, all
  96 unit-test files / 1,243 tests, eight package fixtures, sixteen launcher/asset
  fixtures, the native Swift widget fixture and production build. The consumed
  LFS icon was confirmed hydrated before frontend validation.
- Relative documentation links, conflict-marker absence and `git diff --check`
  passed. Generated `dist` output is removed after the merge commit.

## Limits

The earlier local complete Go race failures remain recorded in
[the fixture evidence](ci-stream-termination-fixture.md). Neither these merge
checks nor a merged compaction prerequisite complete issue #1206. Its discovery
reservations must still reach main before dependent implementation. Fresh hosted
CI and review of the pushed merge remain pending.
