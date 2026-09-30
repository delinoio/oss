# PR #1193 Agent Workers merge at 08:52 UTC

Recorded on 2026-09-30 on macOS arm64 after explicitly invoking the repair-pr workflow for [PR #1193](https://github.com/delinoio/oss/pull/1193). The starting head was `ceacea9701e3ef126d2cb1a5683e6dfba389f50e`. Preserve the prior [implementation](repository-folder-registration.md), [allocation repair](pr-1193-first-repair.md), [review repair](pr-1193-review-repair.md) and [main-merge](pr-1193-main-merge-0837.md) records as historical observations.

## Conflict and resolution

The fetched base was `c49b08027e5b6679cd0fd01066544ac1a72d4a0e`, which introduces Agent Workers Settings presentation. Its single conflict in `settings.tsx` joins the newly inserted `AgentWorkerRow` component with this PR's extended Settings props declaration. Merge commit `2ada9bb49e3a1783d5ebf0d8f11a0f3ba2e96d08` retains that complete component and its bounded inert unsupported-schema name projection, together with the folder-picker and fresh Worker-proof props, scoped native wrappers and repository-registration route. The incoming Agent presentation contracts, tests and scoped styles remain intact.

The merge target was confirmed from MERGE_HEAD before committing. Git's shared origin/main ref advanced again while this validation ran; that newer revision is not part of this merge and belongs to a subsequent scheduled pass. The repair is one-shot, with one normal push after all local commits; no rebase, force push, merge or auto-merge of the PR is used.

Initial inventory contained no unresolved non-outdated Codex threads and no failing checks. GitHub exposed only a passing Cloudflare Pages check; complete Actions and approval evidence remained unavailable. Earlier repaired review threads stay resolved. No separate review or CI repair was required.

## Executed verification

- `pnpm --filter @delinoio/delidev-api-client build` passed before component validation.
- From `apps/delidev`, `pnpm exec vitest run src/repository-registration.test.tsx src/settings.test.tsx src/settings-lifetime.test.tsx --maxWorkers=1 --testTimeout=15000` passed 3 files / 89 tests, and `pnpm typecheck` passed before the merge commit. This verifies registration adapters alongside Agent list actions, schema gates, scope and opening disposal.
- Required full frontend against `2ada9bb49e3a1783d5ebf0d8f11a0f3ba2e96d08`: `GOMODCACHE=/private/tmp/issue-1142-go-mod GOCACHE=/private/tmp/issue-1142-go-build GOMAXPROCS=2 GOFLAGS=-p=2 VITEST_MAX_WORKERS=2 pnpm test` passed from `apps/delidev`: 90 files / 1,169 tests, 8 package dry-run tests, 16 launch/asset tests, widget fixture checks and production build. The normal unit-test timeout was retained. Go-spawning frontend fixtures use disposable caches rather than the shared-cache state that failed previously.
- Normal commit hooks and `git diff --check` passed. Repository-owned generated dist output was removed after validation. This merge changes no Go, Rust, protocol allocation, schema or generated source, so it does not claim new native compilation, root Rust success or rerun protocol freshness. The prior Go, contract and binding checks remain qualified in their owning records.

## Remaining limits

Native picker/trusted-main/saved/foreign-window authorization, keyboard/focus and 960×640/200% zoom acceptance remain independently unverified on macOS, Windows and Linux. Component, compile, package and widget fixtures do not establish native desktop or real-account acceptance.

Earlier root Rust failures on unchanged binpm temporary-path and clibox DNS-classification assertions remain recorded. Earlier broad frontend failures remain historical failed runs even though subsequent review and main-merge runs pass completely. The PR body preserves these distinctions and Closes #1142. The push invalidates prior CI/review evidence; fresh mergeability, checks and Codex activity are left to the next five-minute heartbeat.
