# PR #1169 main merge maintenance, 2026-09-30

## Conflict and composition

The maintenance inventory found PR head `aa72fa0fdd7b774a09f22f25789255876071c8f6` open and conflicting with main. The exact fetched base merged in this pass is `b1b3e9e7c55511086a284021850426d48484b127`, including Projects settings, verified settled failed-Claude Resume and Windows Go shard precompilation. Three conflicts occurred in the domain, server and Worker AGENTS files. Both independent rules were retained in each file; no policy text was discarded. Generated schemas were not chosen from a merge side.

The upstream failed-Claude continuation profile composes with the existing subagent boundary: Worker checkpoint retention excludes every observed child tree, server completion requires child closure independently and rejects version-2 completion for sessions with children, and completed-report recovery rejects child-owned progress. Added live-child and closed-child cases in the failed-checkpoint Worker regression prove both remain version 1 without acquiring a checkpoint. The failed-root Resume profile therefore grants no subagent continuation.

Main also imports bounded five-second UI waits for actual server-backed Settings fixtures and Windows compile-only test prewarming. These are upstream changes from the fetched base; this repair adds no deadline change or product-source workaround.

## Executed validation

Checks ran against this combined source with the new child regressions, isolated temporary fixtures and no user credentials:

- `GOCACHE=/private/tmp/issue-1094-review-go-cache GOMAXPROCS=4 go test -race -p 2 ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/harness/codex ./cmds/delidev-cli/internal/harness/claude ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/worker -run 'Subagent|TestClaudeUsage|TestClaudeContent|TestClaude.*Continuation|TestClaude.*Recovery|TestClaudeFailed|TestClaudeLostFailed|TestOriginalFailedEOF|TestClosedContinuationPreservesFailure|TestFailedCheckpoint' -count=1 -timeout=5m`: domain, Codex, Claude and server passed. The new Worker child case initially panicked because its fixture assigned to a nil map. After initializing that fixture map, the Worker package passed the same race selection independently. No product defect was inferred from that fixture error.
- `GOCACHE=/private/tmp/issue-1094-review-go-cache GOMAXPROCS=4 go vet ./cmds/delidev-cli/...`: passed after the fixture correction.
- `GOCACHE=/private/tmp/issue-1094-review-go-cache GOMAXPROCS=4 VITEST_MAX_WORKERS=1 pnpm test` in `apps/delidev`: passed, including typecheck, 91 files / 1,126 tests, eight bundle fixtures, 16 launcher/asset fixtures, widget checks and production build.
- `pnpm proto:check`: passed, including format/lint, breaking checks and generated-source reproducibility.
- `node --test scripts/ci/go-test.test.mjs`: all nine fixtures passed.
- `git lfs fsck` and `git diff --check`: passed. Required embeds were explicitly built before normal commit hooks. Generated repository-owned `dist` outputs are removed before finishing.

The completed original aggregate Go race run remains an actual exit-1 result and was not duplicated. Its ten failed packages, twelve passed packages, two packages without tests and revision/host constraints remain recorded in [review repairs](review-repairs.md); these focused checks do not replace that outcome.

## Remote observations and limits

At the opening inventory, head `aa72fa0fdd7b774a09f22f25789255876071c8f6` had passed Linux Go, Go Quality, protocol/client, environment and release-contract checks. macOS and Windows Go were running. There were no unresolved eligible Codex threads; code and security reviews were running for that exact head. A repair push invalidates those checks and reviews as evidence for its new head. Fresh pending activity is deferred to the next scheduled pass. No merge readiness, human approval, real provider-account session, native installation or release acceptance is claimed.

The final one-shot inventory, after merge commit `3834611c6e96b260b8e66d79d389b9818a41c5b6`, found no failed or pending CI checks on the still-remote prior head, but contained four new non-outdated Codex threads that arrived during validation. They are unassessed and remain unresolved for the next scheduled explicit repair pass:

- `PRRT_kwDORRAKg86ndQyj`: selected Claude child-history ancestry projection.
- `PRRT_kwDORRAKg86ndQy1`: changed original requested model.
- `PRRT_kwDORRAKg86ndQy_`: Claude-only metadata on Codex observations.
- `PRRT_kwDORRAKg86ndQzF`: later Claude task transcript flags.

The final inventory was not repeated. No source repair or resolution for these new findings is claimed in this pass. The scheduled repair must assess each independently before CI. The old four handled threads stay resolved.
