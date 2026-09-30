# Pinned required workflows: replacement implementation

## Scope and source

Issue [#1106](https://github.com/delinoio/oss/issues/1106) remains open. This replacement starts from freshly fetched main `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` on 2026-09-30 and adapts the scoped implementation from closed, unmerged [PR #1111](https://github.com/delinoio/oss/pull/1111). Implementation commit `16481058c5a8ff741fc37576fd7ae4aafa8a6b23` retains the current ownership structure and frozen historical evidence; unrelated prior changes are not included. The integration and desktop contracts own behavior.

The existing authenticated repository CI query, Go CLI and desktop now carry explicit-SHA workflow rules and complete original source/run/current-attempt evidence. Numeric repository lookup resolves current source names. Exact original file/path/SHA, current ordered-parent test-merge suite, PR association, native requiredness and independent current-attempt jobs must agree. Missing, unsupported, inaccessible, stale or competing evidence remains Unknown. Aggregate running state cannot reuse an old failure. Historical status-check proofs and original CheckRun content versions stay compatible. No RPC schema or database migration changes.

The replacement additionally rejects unbound suites with absent or malformed App identity: only a positive non-Actions App identity proves a suite unrelated. Controlled pagination regressions cover that distinction. A domain regression confirms unavailable workflow proof retains the independent ordinary status-check assessment while the overall result stays Unknown.

## Executed verification

- Final `go test -race ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/integrations/github` passed, including the App-identity guard and status-check compatibility regression. Final focused Go vet also passed.
- `go vet ./cmds/delidev-cli/...` passed.
- `pnpm ci:contracts` passed all 102 tests.
- A read-only authenticated `gh api graphql` request using the complete fixed `DeliDevWorkflowSuites` document succeeded against PR #1038. Its current test merge had the exact ordered base/head parents and zero suites. This verifies current schema compatibility, not real required-workflow execution or product PAT access.
- The full `go test -race ./cmds/delidev-cli/...` completed with exit 1: unchanged CLI, harness and workspace fixtures failed, and several native-harness/server/store/Worker/workspace packages reached the default ten-minute timeout. The run overlapped substantial validation in other worktrees; measured host load average reached 361.54 with about 26 GiB of swap in use. No full-suite Go pass or causal attribution of every failure is claimed.
- On the exact original base `74701b8948694e2bf8f8ba6d07e596c2d2f358a7`, isolated `GOMAXPROCS=2 go test -race ./cmds/delidev-cli/internal/cli -run '^TestCLISessionAcceptanceQueueAndArchive$' -count=1 -timeout=5m` also failed (62.915s), at the workspace-reader boundary. The broad implementation run timed out earlier in workspace preparation for that test; this baseline verifies a failing unchanged fixture without equating those symptoms.
- The first desktop `pnpm test` run used `VITEST_MAX_WORKERS=2 GOMAXPROCS=2` and finished its Vitest stage with 80 files / 941 tests passed and 5 unchanged files / 23 tests failed, mostly deadline failures under the measured host load. The failed Vitest stage prevented the later packaging/build commands. The new workflow tests passed in that run.
- Isolated `VITEST_MAX_WORKERS=1 pnpm exec vitest run src/github-workflows.test.tsx src/github-ci.test.tsx src/github-rules.test.tsx` passed all 3 files / 11 tests. On the exact base revision, the three failed Settings tests passed in isolation (3 passed / 21 skipped), without changing their deadlines or implementation.
- The full frontend retry `VITEST_MAX_WORKERS=1 GOMAXPROCS=2 pnpm test` passed: all 85 files / 964 tests, generated client build, TypeScript checks, 8 bundle/package verifier tests, 16 asset/launcher tests, native macOS widget fixtures and the production frontend build. No test or product code changed between the failed first run and successful retry.

The host uses Go 1.26.8 on macOS arm64 and Node.js 24.11.0. The DeliDev source icon was hydrated through Git LFS before frontend/native-package checks. Required administrator and async-commit-hook embeds were generated explicitly for Go checks/hooks. Generated repository-owned `dist` directories are removed after validation and committing.

## Acceptance limits

Fixtures use isolated temporary state and controlled providers, without user credentials or external writes. The explicit schema-only query above is separate authenticated read-only evidence. No real-account required-workflow end-to-end acceptance, Windows/Linux native acceptance, publication or completion of the full issue #964 scope is claimed. Mutable source refs, target/queue workflows and code-scanning merge protection remain outside #1106.
