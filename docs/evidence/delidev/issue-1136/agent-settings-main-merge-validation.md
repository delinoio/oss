# Agent settings merge and naming validation

## Source and revision

- Date: 2026-09-30.
- Issue: https://github.com/delinoio/oss/issues/1136.
- PR: https://github.com/delinoio/oss/pull/1172.
- First-parent revision: `6cbe98ebdd38eb303c3ea6a26be5225c519ee6b7`.
- Merged main: `126641a6dcf274420c8c5800a6e88079f0e19990`.

Main advanced through unified PR activity (#1175), Pull requests sidebar presentation (#1181), Agent Worker core/optional settings (#1192) and GitHub profile settings (#1188). The new overlap is confined to `ResourceChoice` and the desktop contract. This merge preserves those main features and the existing Runner Device naming boundary.

`ResourceChoice` retains main's required markers, exact accessible label, Agent Worker read-problem context, initial-fetch status and page-row empty-state rules. The optional `resourceLabel` still defaults to `label` and supplies only placeholder/status nouns. New session remains **Runs on**, with Runner Device inventory wording and original machine IDs. The complete new Agent Worker documentation subsection and Settings opening lifetime remain intact; the Settings category stays **Runner Devices** with its original `execution-workers` value.

Inspection compared the new Agent Worker subsection verbatim against main and confirmed that Agent Worker, GitHub Integrations, Pull requests, PR activity, protocol sources and generated API client sources retain main exactly. No new execution, schema, migration, dependency or credential behavior is introduced by the conflict resolution.

## Verification

- `pnpm exec vitest run src/agent-configuration.test.tsx src/session-tools.test.tsx --maxWorkers=1 --testTimeout=30000`: all 28 focused tests in two files passed.
- `GOMAXPROCS=2 go test -p 1 -timeout 5m ./cmds/delidev-cli/internal/server -run 'TestPRRemediationWorkspacePlanUsesCurrentExplicitSelectionWithoutDispatch|TestScheduleRPCReferencedDeletionDisablesAtomically' -count=1`: passed using a private temporary Go cache.
- `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...`: passed with that cache.
- `go build -p 2 -o <temporary validation binary> ./cmds/delidev-cli`: passed with `GOMAXPROCS=2` and the private temporary cache.
- `node --test scripts/ci/delidev-structure.test.mjs`: all three checks passed.
- `git lfs fsck`: passed. Required administrator/ach embeds and the generated client were prepared before compilation; the icon remains hydrated.

The complete frontend run uses the same local validation settings as the [previous main merge](main-merge-validation.md): private temporary Go cache, one Vitest worker, bounded longer runner waits and Testing Library `asyncUtilTimeout: 10000`. Its wrapper restores both original tracked test files. No timing or cache configuration is shipped.

## Completed frontend gate and limits

The complete `pnpm test` passed: all 1,051 tests in 88 files, eight packaging fixtures, sixteen asset/desktop-launch fixtures, widget checks and the production build. The original Vitest configuration and Testing Library setup were restored, confirmed by an empty diff. `git diff --check` passed after restoration and evidence authoring. Generated repository-owned `dist` output and this repair's private validation cache are removed before the final clean-worktree check.

Focused server regressions, vet and CLI compilation do not replace the earlier incomplete/not-passing broad local Go gate recorded in the [implementation evidence](runner-device-terminology.md). No new native/account/platform acceptance is claimed. Fresh current-head CI and review remain separate from local fixture/build evidence.
