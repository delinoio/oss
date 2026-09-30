# Scheduled Projects overlap repair

On 2026-09-30, maintenance of https://github.com/delinoio/oss/pull/1186 found `CONFLICTING`/`DIRTY` at head `70da717250804fa731496227303262a12ee5d0c8`. This repair merges `origin/main` revision `6441b84813007eb1f3fca9c3ac82d6963cf44bd6` without rebasing.

Conflicts affected the app/frontend instructions, Settings component and fixture, and category CSS. Both sides' complete instructions were retained. The component preserves the incoming Projects wrapper, rows and success-only empty/page states alongside the Agent-specific wrapper, divided rows and empty/page states. Both wrappers remain mounted across category changes. The fixture retains asynchronous resource reads and the incoming provider-request inspection. Incoming API provider selection, Home sidebar focus and schedule creation changes remain intact; no repair changes their business requests or lifetime policy.

## Validation

- `pnpm --dir apps/delidev prepare:assets` passed with the hydrated icon ready, and `git lfs fsck` passed.
- The generated client build and `pnpm --dir apps/delidev typecheck` passed. `pnpm --dir apps/delidev exec vitest run src/settings.test.tsx src/settings-projects.test.tsx src/settings-lifetime.test.tsx --maxWorkers=1 --testTimeout=30000 --hookTimeout=30000` passed all 80 tests in three files, using the original Testing Library wait budget.
- `node --test scripts/ci/*.test.mjs` passed all 111 tests, including the incoming Go-shard runner/workflow contracts. This does not establish native Windows execution or the documented CI duration target.
- `GOCACHE=/private/tmp/delidev-1162-go-cache pnpm --dir apps/delidev test` completed with exit 0: generated client build, typecheck, 89 Vitest files/1,144 tests, eight package/bundle fixtures, sixteen asset/launch fixtures, Swift widget checks and production build. Vitest took 202.32 seconds.
- The full invocation used temporary one-worker execution, 30-second test/hook budgets and a 30-second Testing Library wait budget. Assertions were unchanged. Configuration/setup were restored byte-for-byte before staging; this is bounded serial evidence, not a passing default-budget invocation. Earlier default-budget timeouts remain qualified in the prior evidence records.
- `git diff --check` and the final `git lfs fsck` passed. Generated `apps/delidev/dist` and `packages/delidev-api-client/dist` were removed before committing.
- The complete Agent and Projects category CSS blocks match their respective pre-merge head/base blocks. The merged Settings/CSS SHA-256 values are respectively `8cf604d62e76fe5afd46bad0dda2462209c2548bbe0307f49acc593c74c04508` and `5c273c4354df4c6eff364a786e2fe85390f4f1391fa68eb9dbbc6378863aa6ef`.

Browser measurements retain their original source provenance in [the presentation evidence](agent-workers-presentation.md); they were not rerun for this merge. Prepared native CEF acceptance remains unperformed, and no missing-platform or real-account acceptance is inferred from fixtures. The current-head automated review was unavailable: [Codex's repository-limit notice](https://github.com/delinoio/oss/pull/1186#issuecomment-5907231958) reports exhausted code-review credits. Prior review completion on `22bac34` is not approval of this merge.
