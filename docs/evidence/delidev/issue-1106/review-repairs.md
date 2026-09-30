# Pinned required workflows: PR review repairs

## Scope and revisions

The 2026-09-30 maintenance pass addresses the three original Codex review threads on [PR #1167](https://github.com/delinoio/oss/pull/1167), reviewed at `3f0a07c1a054b4083d4af261803c37beb0dc2a9b`. It preserves the existing issue #1106 implementation, authenticated read boundary and original validation qualifications in [the replacement evidence](pinned-required-workflows.md).

- `7021887c9` recomputes desktop workflow requirement state, reason and exact result IDs from the native aggregate and independently attributed current-attempt jobs. A running aggregate cannot validate a retained terminal failure; terminal aggregate/job disagreement remains Unknown.
- `94b3e6e4e` sizes the complete rules/result CI envelope before admitting optional workflow evidence. A large multi-run job inventory is dropped as a whole without erasing ordinary required-check assessments or publishing a partial inventory.
- `5653b37e5` caps each optional enrichment at three seconds and one quarter of the owning query's remaining time. Canceled reads finish before return. If either repeated optional inventory is unavailable, both workflow projections are discarded while ordinary repeated observations still receive their existing drift, rules and PR checks. Actual drift between two complete inventories and explicit parent cancellation retain their prior failure behavior.

The scoped desktop/GitHub instructions and owning contracts are updated with each repair. No RPC schema, storage migration, GitHub write capability, native runtime or credential ownership changes.

## Executed verification

- The generated DeliDev client build and desktop TypeScript check passed. Focused `VITEST_MAX_WORKERS=1 pnpm exec vitest run src/github-workflows.test.tsx src/github-ci.test.tsx src/github-rules.test.tsx` passed all three files and 13 tests.
- Focused `GOMAXPROCS=2 go test -race ./cmds/delidev-cli/internal/integrations/github -run 'TestOversizedOptionalWorkflowProofPreservesOrdinaryCI|TestPinnedWorkflowProviderProvesRenamedNumericSourceAndCurrentOutcomes' -count=1` passed.
- Focused `GOMAXPROCS=2 go test -race ./cmds/delidev-cli/internal/integrations/github -run 'TestOptionalWorkflowDeadlinePreservesRepeatedOrdinaryCIAndJoinsReads|TestRequiredWorkflowSourceReadJoinsCancellation|TestPinnedWorkflowProviderNeverPublishesUnprovedFailure' -count=1` passed. Controlled source, run and job reads reach their child deadline without expiring the parent query; a successful first optional inventory followed by an unavailable second one preserves ordinary CI and publishes workflow Unknown. Parent cancellation remains joined.
- Complete domain/GitHub adapter `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/integrations/github` passed; the domain package reused its valid cached result and the GitHub package ran freshly.
- `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...` passed.
- Two full desktop `GOMAXPROCS=2 VITEST_MAX_WORKERS=1 pnpm test` attempts each reached all 85 Vitest files / 966 tests, with 84 files / 965 tests passed and one failure. The first failed the unchanged `App.test.tsx` notification/import-draft case at its five-second deadline; that exact case passed without code changes in isolation (one passed / 36 skipped). The retry failed the unchanged `settings-devices.integration.test.tsx` because its revoke button was not found. That file had passed in the first full run but failed again in isolation. Neither attempt reached the subsequent package-script stages, and no full frontend-suite pass at the repaired revision or causal attribution of these failures is claimed.
- Separately executed `pnpm test:bundle-dry-run && pnpm test:desktop-launch && pnpm test:widget && pnpm build` passed: eight bundle/package checks, 16 asset/launcher checks, native macOS widget fixtures and the production frontend build.
- Full `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/...` completed with exit 1. The unchanged CLI session acceptance fixture failed at the workspace-reader boundary, which also failed on the original base as recorded in the replacement evidence. The Grok and OpenCode harness packages failed; Grok reached the default ten-minute package limit. Server and workspace packages also reached that limit, and a workspace PR-match fixture failed beforehand. Domain/GitHub, other harness packages, store and Worker passed. No full Go-suite pass or causal attribution of every failure is claimed; these broader failures remain visible outside the scoped review fixes.

Go 1.26.8 and Node.js 24.11.0 run on macOS arm64. The first dependency installation and first focused Go build encountered missing objects in their shared caches. Validation retries use an independent temporary pnpm store and Go build cache, without changing dependency pins. The DeliDev source icon is hydrated through Git LFS and the required administrator/async-commit-hook embeds are generated explicitly. Generated repository-owned `dist` output is removed after validation and committing.

## Acceptance limits

The regression providers use controlled responses and isolated state. No real-account required-workflow end-to-end acceptance, release publication, Windows/Linux native acceptance or completion of the broader issue #964 requirements is claimed. PR CI/review status after pushing remains separate from these local results.
