# Outbound proxy PR merge of main 82020a7e

## Source and conflict resolution

PR #1170 head `53d587adb5cf7f22810833dcbbc9f92087b3b8aa` merges main `82020a7ef2916534f3342aa5208f43d2a051b052` without rebasing. The domain and server scoped AGENTS each conflicted between the explicit network rule and main's independently owned Claude continuation rule. Both rules are retained with their owning contract links. No runtime code required manual conflict resolution. Main's settled-failure Resume, Worker/settings/subscription presentation, original imported-mark notices and CI precompilation changes remain intact.

## Executed validation on macOS arm64, 2026-09-30

All Go-dependent checks below use repository-selected Go 1.26.8, `GOMAXPROCS=4`, `GOMODCACHE=/private/tmp/delidev-1084-review-modcache` and `GOCACHE=/private/tmp/delidev-1084-review-go-cache`.

- `go test -race -p 1 ./cmds/delidev-cli/... -run '^(TestNetwork|TestCLINetwork|TestProxyCredential|TestClaude|TestFailedClaude|TestVerifiedFailed)' -count=1` passed. It compiles every DeliDev package and executes selected network, credential and Claude domain/server/Worker regressions. Unrelated suites have no selected tests; this is not a complete-suite claim.
- `go test -race -p 1 ./cmds/delidev-cli/internal/harness/claude -run '^(TestClosedContinuation|TestOriginalFailedEOFCheckpoint|TestFailedCheckpoint)' -count=1` passed the changed native EOF/history/failed-settings fixture boundary.
- `go vet -p 1 ./cmds/delidev-cli/...` and `go build -p 1 -o <temporary-output> ./cmds/delidev-cli` passed.
- `GOMAXPROCS=4 pnpm proto:check` passed lint/formatting, breaking compatibility and forced generated-source freshness without drift.
- All 45 API client tests passed, including real-Go-server compatibility.
- The complete frontend `pnpm test` passed 1,172 tests in 92 files, eight bundle fixtures, 16 desktop-launch/asset fixtures, widget fixtures and the production build. It used `GOFLAGS=-p=1` and `VITEST_MAX_WORKERS=2` in addition to the isolated Go environment.
- All nine `node --test scripts/ci/go-test.test.mjs` fixtures passed, including failure-before-test-execution behavior for the incoming Windows shard precompilation change. Local fixtures do not establish native Windows execution or measured CI duration.

Administrator, ach and DeliDev client outputs were explicitly regenerated before checks and the required root Go-format hook. Previously hydrated consumed LFS assets remained available. Repository-owned `dist` output is removed after checks/hooks. No Rust source changed in this merge.

## Complete-suite and remote evidence limits

The existing isolated full race retry in session 92171 has completed with exit 1, with CLI, Grok, server and workspace failures. It began before this main merge and is not a passing combined-tree full suite. Its actual test names/timings and the limits of the unchanged-main comparison are recorded separately in [short-credential review evidence](short-credential-review.md). No assertion or timeout was relaxed.

The initial GitHub inventory had no unresolved non-outdated Codex threads and no failing checks, but only the Cloudflare check was reported. Current-head review approval and complete CI remain unverified; earlier evidence does not apply to this new merge. Repository code-review usage was reported exhausted, while security-review activity remains separate. These local fixtures do not establish real accounts, enterprise proxy behavior, native proxy credential lifecycle, Worker bootstrap, release or Windows/Linux runtime acceptance.
