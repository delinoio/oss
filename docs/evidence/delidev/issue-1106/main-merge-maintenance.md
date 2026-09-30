# Required-workflow PR: main merge maintenance

## Revisions and reconciliation

The 2026-09-30 repair pass for [PR #1167](https://github.com/delinoio/oss/pull/1167) initially repaired the settings CI wait on head `95daaf97ebd9cf5343917d823d4994156716f2d3`, committing that repair as `b19e0d17`. Its reproduction and pre-merge full frontend pass remain in [ci-preferences-wait.md](ci-preferences-wait.md).

The final one-shot status inventory then reported new merge conflicts after main advanced. This merge integrates the fetched main revision `b1b3e9e7c55511086a284021850426d48484b127` without rebasing. Main already contains the same bounded preference-save waits from PR #1166 and a fixture-scoped asynchronous wait configuration. The resolved preference test preserves those waits and comments alongside this PR's controlled save latency and two-request assertion. The domain AGENTS conflict preserves both the pinned-required-workflow invariant and main's independent Claude failed-continuation rule. Automatically merged desktop/integration contracts retain both feature families; no unrelated base changes are discarded.

The pre-push `CI Result` failure is the aggregate consequence of the same `devhud-protocol` desktop-test failure, confirmed by its job log. It is not a separate repair cause. No unresolved non-outdated Codex threads were present in the final inventory; the latest pushed head had not received a new review because the connector reported a usage limit.

## Executed merged-state verification

Commands ran on macOS arm64 with Node.js 24.11.0 and Go 1.26.8. Native-backed commands used the independent `GOCACHE=/private/tmp/issue-1106-maintenance-go-cache` and `GOMAXPROCS=2`. Required embedded output was regenerated after the merge.

- First `pnpm test` from `apps/delidev` passed 89 files / 1,127 tests and failed one existing `App.test.tsx` uncertain-New-Project case at its five-second test deadline. The same exact case passed in isolation, with one test passed / 43 skipped and no source change to that file. This does not establish the original timeout's exact cause.
- Focused `pnpm exec vitest run src/pull-requests.test.tsx src/settings-preferences.integration.test.tsx --maxWorkers=1` passed both files / 12 tests, including the controlled native save latency and complete singleton assertions.
- Retry `VITEST_MAX_WORKERS=2 pnpm test` passed the complete required frontend command: generated client build, TypeScript check, all 90 files / 1,128 tests, eight bundle/package checks, 16 asset/launcher checks, native macOS widget fixtures and production build. The installed Vitest 4.1.11 resolver was inspected to confirm that this environment variable sets the worker bound; no committed worker or timeout configuration changes were made for this retry.
- `go test -race -p 2 ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/integrations/github` passed both complete packages freshly. `go vet -p 2 ./cmds/delidev-cli/...` passed.
- `node --test scripts/ci/ci-contract.test.mjs scripts/ci/plan.test.mjs scripts/ci/go-test.test.mjs` passed all 58 tests, covering the inherited workflow/shard changes as well as the affected-job contracts.
- `git diff --check` passed. The source icon remains hydrated; all repository-owned generated `dist` directories are removed after validation.

No Rust source conflict was edited; the inherited clibox version/lock changes remain main's release changes. No new full Go race suite, real-account workflow acceptance, native desktop release acceptance or broader issue #964 completion is claimed. Earlier failures and qualifications remain in the separate issue #1106 records. This validates the pinned fetched main revision; subsequent base changes and new CI/review outcomes are checked by the next scheduled maintenance pass.
