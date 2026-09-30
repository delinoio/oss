# Outbound proxy PR main merge

## Revisions and resolution

PR #1170 head `204826a41b8f0252e3b134d76157101b8d9e82f0` merges main `6441b84813007eb1f3fca9c3ac82d6963cf44bd6` without rebasing. The only content conflict was at the end of `docs/cmds-delidev-integrations-contract.md`: the explicit outbound-routing section and main's unified PR activity projection are independent contracts. Both are retained. Incoming PR activity schemas, generated bindings, source owners and desktop changes remain intact. Normal protocol regeneration reproduced the combined generated tree without drift.

## Executed verification

Local macOS arm64 checks on the combined tree, 2026-09-30:

- `GOMAXPROCS=4 pnpm proto:check` passed formatting/lint, breaking compatibility and forced generated-source freshness.
- `go test -race -p 1 ./cmds/delidev-cli/... -run '^(TestNetwork|TestCLINetwork|TestProxyCredential|TestActivity|TestPRActivity|TestRetainPRHandling)' -count=1` passed. It compiles every DeliDev package and runs selected network, credential, client, CLI, server and incoming PR activity regressions. Unrelated suites have no selected tests; this is not a complete-suite result.
- `go vet -p 1 ./cmds/delidev-cli/...` passed.
- `go build -p 1 -o <temporary-output> ./cmds/delidev-cli` passed.
- API client typecheck and all 45 tests passed, including generated descriptor and real-Go-server compatibility.
- The complete frontend `pnpm test` passed 1,122 tests in 89 files, eight bundle fixtures, 16 desktop-launch/asset fixtures, widget fixtures and the production build. It used `GOMAXPROCS=4`, `GOFLAGS=-p=1` and `VITEST_MAX_WORKERS=2`.

Go-dependent checks use `GOMODCACHE=/private/tmp/delidev-1084-review-modcache` and `GOCACHE=/private/tmp/delidev-1084-review-go-cache`, with repository-selected Go 1.26.8. Required administrator, ach and DeliDev client generated output was explicitly regenerated before consuming checks/hooks. Repository-owned `dist` output is removed after verification. The previously hydrated DeliDev icon remains available to consuming fixtures.

## Remaining evidence

The existing complete isolated race retry began before this merge and is still finishing. It has reported CLI session acceptance and Grok failures, plus Grok and server 20-minute package timeouts. Its complete outcome belongs in `short-credential-review.md` when it exits; it cannot establish a passing combined-tree full suite. The earlier bounded unchanged-main comparison in `implementation.md` does not reproduce every current failure and does not establish identical causes.

GitHub's pre-push inventory has no unresolved non-outdated Codex threads and no failing checks, but main conflicts prevent complete CI evidence. Codex reports exhausted repository review usage; current-head review approval is unavailable until an administrator restores capacity. Pushing this merge invalidates earlier CI/review evidence. Real accounts, enterprise proxies, native proxy credential lifecycle and Windows/Linux runtime acceptance remain unperformed.
