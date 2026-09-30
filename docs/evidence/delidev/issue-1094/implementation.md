# Issue #1094 native subagent observations

## Revision and scope

Verified on 2026-09-30 in branch `kdy1/issue-1094-subagent-observations`, based on freshly fetched `origin/main` revision `74701b8948694e2bf8f8ba6d07e596c2d2f358a7`. The initial implementation commit is `0662ed96cfd27805b1957b6ee9ca193919e5b052`; commit `1dd956960` retains the final read-only inventory and shutdown regressions. This record is committed separately.

The earlier [PR #1122](https://github.com/delinoio/oss/pull/1122) was closed without merging during the ownership refactor. This implementation adapts that work to the current split Go/CLI/protocol ownership and main's reserved identifiers rather than restoring its monolithic files or frozen evidence ledger. Entity kind 30 and capability 12 preserve the existing allocation registry.

Go publishes original Codex `0.151.0` API and Claude `2.1.236` API child observations through authenticated existing resource reads, with CLI session pagination and capability-gated desktop rendering. Whole batches validate before atomic state/resource/event publication. Original native/product IDs, nested parents, source coverage, partial output, requested versus observed models and nullable usage remain distinct. Children retain independent cleanup obligations after root completion, and their counters never enter additive root billing. No public child-control operation or unproved continuation is exposed.

Codex canonical activity kinds and state-DB-only descendant inventory were checked against the pinned official source at `d8673cb68e349c208659b986697773d3145dbb14`. The adapter explicitly avoids native metadata repair and preserves shutdown after older turn completion/history. The Claude runner ignores forwarded child usage in its root adapter after the child adapter publishes it.

## Executed checks

All native/provider fixtures used isolated temporary state without user credentials. Required LFS assets were hydrated and `git lfs fsck` passed. Root `pnpm install --frozen-lockfile` and both root-hook embed prerequisites succeeded without dependency changes. No Rust source changed.

| Command | Result |
| --- | --- |
| `go test -race ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/harness/codex ./cmds/delidev-cli/internal/worker ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli -run Subagent -count=1` | Passed. Covers bounded ownership, exact replay, late completion, nested Claude content, root-only usage isolation and read-only CLI/native operations. |
| `go test -race ./cmds/delidev-cli/internal/server -run Subagent -count=1` | Passed after adding transactional historical-ID-reuse rollback and overlapping parent/child ledger assertions. |
| `GOMAXPROCS=4 go test -race -p 2 ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/harness/codex ./cmds/delidev-cli/internal/server -run Subagent -count=1` | Passed on the final source, including completed-to-shutdown refinement and late notification/history preservation. |
| `go vet ./cmds/delidev-cli/...` | Passed again after the final lifecycle correction. |
| `pnpm proto:check` | Passed after implementation commit: lint, breaking baseline and generated-source reproducibility. |
| `node --test scripts/ci/delidev-proto.test.mjs scripts/ci/delidev-structure.test.mjs` | Passed, 6 tests. |
| `pnpm exec tsc --noEmit` in `apps/delidev` | Passed. |
| `pnpm --filter @delinoio/delidev-api-client lint` and `pnpm --filter @delinoio/delidev-api-client test` | Passed on final source, 4 files / 44 client tests. |
| `pnpm exec vitest run --maxWorkers=1 src/subagents.test.tsx src/session.test.tsx` in `apps/delidev` | Passed: the matching subagent file ran 3 tests; no session test file matched. Capability gating, exact session/page reads, revision refresh and nullable rendering covered. |
| `pnpm test` in `apps/delidev` | Failed: 14 files / 25 tests failed; 71 files / 938 tests passed. Failures included App/Settings timing and Go-fixture readiness. This is not a green full frontend result. |
| `pnpm exec vitest run --maxWorkers=1` in `apps/delidev` | Failed on repeat: 6 files / 9 tests failed; 79 files / 954 tests passed. Remaining failures were in App, Settings and Claude/preferences/usage/workspace integration tests; the new subagent tests passed. |
| `pnpm test:bundle-dry-run`, `pnpm test:desktop-launch`, `pnpm test:widget`, `pnpm build` in `apps/delidev` | Passed independently after the failed aggregate unit stage. Bundle/launcher fixture results and production build do not prove native platform acceptance. |
| `GOMAXPROCS=4 go test -race -p 2 ./cmds/delidev-cli/... -timeout=30m` | Full required backend run pending completion at evidence preparation; already has failures in CLI workspace acceptance, harness discovery bounds and Claude API stream recovery. It is not green. |
| Unchanged-main diagnostic: `GOMAXPROCS=4 go test -race -p 1 ./cmds/delidev-cli/internal/harness -run 'TestDiscoveryUsesIsolatedEnvironmentAndOwnedProcesses\|TestExplicitPathFailuresNeverFallBack\|TestDiscoveryBoundsAndGrokUpdateSuppression' -count=1` | Passed in an isolated Go-source archive of the base revision. This does not prove all full-suite failures predate the change. |
| `git diff --check` and normal commit hooks | Passed for the implementation commit. |

## Regression evidence and limitations

The server tests exercise actual SQLite rollback when a later batch element reuses a historical native identity after an earlier valid element, and verify that a child total overlapping an existing root total does not add a billing row. Live/unavailable children block cleanup; root terminal state and child terminal state do not close descendants. Receipt replay retains one resource/event. Tests reject foreign, cyclic, duplicate and reparented identities; shutdown may refine a terminal observation but cannot reopen it.

Multiple independent full Go/frontend suites were active on the host during these runs. Process contention may contribute to bounded discovery, process and UI timing failures; that is a suspected contributor, not proof that every failure is environmental or pre-existing. Product/test deadlines were not relaxed, and failures remain visible.

No real-account/native provider session, supported desktop/mobile platform acceptance, package installation or release acceptance was performed. Partial output/history fixture coverage is not a full child transcript, resumed child execution or provider-account proof. The complete issue #964 product scope remains independently outstanding. See [the subagent contract](../../../cmds-delidev-subagents-contract.md) for ownership, bounds and removal/change conditions.
