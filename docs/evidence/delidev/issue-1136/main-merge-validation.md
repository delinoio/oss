# Main merge and naming composition

## Source and revision

- Date: 2026-09-30.
- Issue: https://github.com/delinoio/oss/issues/1136.
- PR: https://github.com/delinoio/oss/pull/1172.
- First-parent naming clarification: `d67669d6822be6be478222e46c10588f6d8c443b`.
- Merged main: `7f356266fc195b1880ffac66a93dadab5c5a2df7`.

Main advanced through the AI API Keys presentation change in PR #1178 and Diagnostics hierarchy/disclosures in PR #1179 while this PR was being repaired. The merge retains both implementations. Conflicts in the Diagnostics component, grouped Settings labels and desktop contract combine the existing Runner Device terminology with main's presentation changes. Diagnostics keeps its identity-bound disclosure lifecycle, single heading, original query and report bounds; only its execution-device heading/help nouns change. Grouped navigation retains **AI API Keys** and **Runner Devices**, with unchanged `api-accounts` and `execution-workers` category values.

The Diagnostics fixture now asserts the Runner Devices heading and final help alongside its existing hierarchy/field assertions. The new Diagnostics presentation contract uses the composed heading/help and retains its backend/category boundary. No schema, migration, dependency or execution behavior changes are introduced by the conflict resolution.

## Verification

- `pnpm exec vitest run src/doctor.test.tsx src/settings.test.tsx src/account-settings.test.tsx src/session-tools.test.tsx --maxWorkers=1 --testTimeout=30000`: all 84 focused tests in four files passed.
- `node --test scripts/ci/delidev-structure.test.mjs`: all three documentation ownership checks passed.
- `git lfs fsck`: passed.
- A private temporary Go cache warm-up, `go build -p 2 -o <temporary validation binary> ./cmds/delidev-cli` with `GOMAXPROCS=2` and the temporary `GOCACHE`, passed.

## Initial full-gate failure

The first complete `pnpm test` after merging main passed 998 tests in 83 files, failed one test and skipped one test across two other files. The paired-device fixture's one-second wait did not find **Revoke DeliDev desktop**. The preferences fixture failed during linking because a file in the shared Go build cache disappeared. This run did not reach the later packaging/build gates and is not a complete passing result.

The rerun uses a warmed private temporary Go cache, temporary Vitest `maxWorkers: 1`, `testTimeout: 30000` and `hookTimeout: 300000`, and a temporary Testing Library `asyncUtilTimeout: 10000`. These are local validation settings; the wrapper restores both tracked files after the run. Product timing and fixture assertions are unchanged.

## Completed rerun and evidence limits

The complete `pnpm test` rerun passed: all 1,000 tests in 85 files, eight packaging fixtures, sixteen asset/desktop-launch fixtures, widget checks and the production frontend build. Both original tracked test files were restored, confirmed by an empty diff. No private cache, test timeout or Testing Library configuration is committed.

`git diff --check` passed after restoration and evidence authoring. Required repository-owned generated `dist` directories were removed after validation; dependency-owned outputs remain installed.

The [implementation record](runner-device-terminology.md) still owns the initial incomplete/not-passing broad local Go run. This frontend merge validation and private-cache CLI compilation do not establish a complete broad Go gate or real harness/account/platform acceptance. Current-head CI and automatic review remain separate publication evidence.
