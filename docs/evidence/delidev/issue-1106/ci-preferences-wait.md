# Required-workflow PR: settings integration CI repair

## Scope and observed failure

The 2026-09-30 maintenance pass repairs the settings integration failure in [PR #1167](https://github.com/delinoio/oss/pull/1167), based on head `95daaf97ebd9cf5343917d823d4994156716f2d3`. [CI run 36689199720, job 109802191377](https://github.com/delinoio/oss/actions/runs/36689199720/job/109802191377) checked GitHub's merge revision `78bd3fb4eee66ba988d0b1a1483486458f4f4149`. Its schema, Go binding and generated client checks passed, but the desktop suite failed `settings-preferences.integration.test.tsx` while waiting for the first `Edit Server preferences` button. That suite reported 88 files / 1,087 tests passed and one failed.

The native-backed test used Testing Library's default 1,000 ms asynchronous query deadline even though its scenario permits 15 seconds and a save requires an RPC plus an invalidation refetch. The unchanged test passed in isolation. Injecting a controlled 1,100 ms delay into its configuration-save transport reproduced the same missing-button failure at the first post-save assertion. This establishes a timing defect in the test; it does not establish the original runner's exact latency or a production save failure.

The repair gives both post-save assertions explicit five-second waits within the unchanged scenario deadline. It retains the controlled delay over the real owned Go server, verifies exactly two save requests, and preserves all singleton, defaults, identity, document and monotonic revision assertions. Production RPC deadlines, retries and Settings behavior are unchanged. The test still uses its feature-owned temporary fixture and joins its normal child cleanup; no repository policy or domain contract changes.

## Executed verification

All commands ran on macOS arm64 with Node.js 24.11.0 and Go 1.26.8, using `GOCACHE=/private/tmp/issue-1106-maintenance-go-cache` and `GOMAXPROCS=2` for native-backed tests.

- From `apps/delidev`, `pnpm exec vitest run src/settings-preferences.integration.test.tsx --maxWorkers=1` passed before the repair without injected latency. With the controlled delay and default waits it failed at the first post-save button assertion. After adding explicit waits, the delayed scenario passed and retained its real server assertions.
- From `apps/delidev`, final `pnpm test` passed completely: the generated DeliDev client build, TypeScript check, 85 Vitest files / 966 tests, eight bundle/package checks, 16 asset/launcher checks, macOS widget fixtures and production frontend build.
- `git diff --check` passed. The source icon was verified hydrated through Git LFS. Required async-commit-hook and administrator embeds were generated before native-backed tests; repository-owned generated `dist` output is removed before delivery.

The earlier broad Go-suite failures and review-repair qualifications remain preserved in [review-repairs.md](review-repairs.md). No Go or Rust source changes or new full Go-suite run are included in this repair. Fixture results do not establish real-account required-workflow, native desktop, release or broader issue #964 acceptance. New CI and review evidence after the push remains separate from this local verification; Codex re-review was unavailable at the starting head because the connector reported a review usage limit.
