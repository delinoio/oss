# Scheduled main merge repair

On 2026-09-30, maintenance of https://github.com/delinoio/oss/pull/1186 found a new `CONFLICTING`/`DIRTY` base at head `22bac349934bedd7530a2b111a6ba34c4c0fb008`. This repair merges `origin/main` revision `126641a6dcf274420c8c5800a6e88079f0e19990` without rebasing.

Conflicts were limited to `apps/delidev/AGENTS.md`, `apps/delidev/src/AGENTS.md` and `docs/apps-delidev-desktop-contract.md`. Both sides' complete Agent-list, Agent-editor and GitHub Integrations instructions/contracts were retained as separate paragraphs. The source merge preserves the incoming core/optional Agent editor, Integrations and PR activity/sidebar implementations. The Agent list controller, action targets, request identity and opening lifetime remain unchanged.

## Validation

- `pnpm --dir apps/delidev prepare:assets` passed with the hydrated icon ready.
- `GOCACHE=/private/tmp/delidev-1162-go-cache pnpm --dir apps/delidev test` completed with exit 0: generated API-client build, typecheck, 88 Vitest files/1,065 tests, eight package/bundle fixtures, sixteen asset/launch fixtures, Swift widget checks and production build. Vitest took 210.94 seconds.
- That invocation used the previously documented temporary one-worker execution, 30-second test/hook budgets and 30-second Testing Library wait budget. Assertions were unchanged. Configuration and setup were restored byte-for-byte before staging; this is bounded serial evidence, not a passing default-budget invocation.
- `GOCACHE=/private/tmp/delidev-1162-go-cache go test -race -p 1 ./cmds/delidev-cli/internal/cli ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/store -run 'Test(PRActivity|CLIActivityPR)' -count=1` passed the selected CLI/server/store PR activity tests; the domain package compiled with no matching tests. This is focused coverage, not the complete Go suite. The incoming protocol and generated-binding sources match the merged base without repair edits.
- The Agent column, row and responsive CSS block matches the prior validated head byte-for-byte. New unrelated styles appear before/after it. The complete merged Settings/CSS SHA-256 values are respectively `c73fbbcc90cbdad35c27bd4b2687cd457201b698a455862a36620b07e7555c4e` and `0e63f6e6f3cbe9f50e369398c7a261e572c5604a7d6f33375d8d055e53e446cb`.
- `git diff --check` and `git lfs fsck` passed. Generated `apps/delidev/dist` and `packages/delidev-api-client/dist` were removed before committing.

Browser measurements retain their original source provenance in [the presentation evidence](agent-workers-presentation.md); they were not rerun for this merge. Prepared native CEF acceptance remains unperformed, and no missing-platform or real-account acceptance is inferred from these fixtures.
