# Pinned required workflows: replacement implementation

Validation date: 2026-09-30. Freshly fetched target `origin/main` was
`ad0e3e9a29cb3d8375ab5d168bb160c35a023250`.
Implementation commit: `910d33b73d6d02552fe63061a94768ff4fabd090`.

Issue #1106 remains open. Closed, unmerged PRs #1111 and #1167 do not establish
completion on main. This replacement reuses the issue-scoped implementation from
`35313e1bcd8fda33c557e03260e041ba81a7a759`, composes it with the current source
owners and adds fixes for the remaining #1167 review findings. None of those
earlier PRs' validation results are counted as newly executed checks here.

The existing authenticated CI query now retains immutable numeric source/path/SHA,
complete current-test-merge suites and independently attributed current-attempt
jobs. Ordinary status-check proof and versions remain compatible. Missing,
inaccessible, changed, competing or unsupported evidence cannot authorize failure.
Optional proof has independent time/size limits and cannot erase ordinary CI.

New regression coverage checks that unsupported/unpinned rules and unusable PR
states cause no enrichment reads, unusable sources in mixed rules cannot erase
independent pinned proof, and a stale headline state/reason cannot override pending
or Unknown workflow rows or unsupported rule authority. Retained proof independently
selects the current merge failure and refuses reuse by a newer running attempt.

Executed checks:

- `go test -race ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/integrations/github`
  passes, including the final retained-proof case (domain 9.414s; adapter cached
  after its successful 10.482s run).
- `go vet ./cmds/delidev-cli/...` passes.
- Desktop `pnpm exec vitest run src/github-workflows.test.tsx src/github-ci.test.tsx src/github-rules.test.tsx`
  passes all three files / 16 tests. `pnpm typecheck` also passes.
- `pnpm ci:contracts` passes all 113 tests.
- Root `pnpm install --frozen-lockfile` succeeds and installs the linked-worktree
  hooks. The implementation commit passes the ordinary Go formatting hook.
- The required DeliDev icon is hydrated with `git lfs pull --include='apps/delidev/**' --exclude=''`;
  `git lfs fsck` passes.
- Read-only `gh api graphql` introspection confirms the current original
  `WorkflowRun.file` and `WorkflowRunFile` fields. The entire fixed suite document
  executes without GraphQL errors against the original closed PR #1167 node;
  that PR has no potential merge commit, so this validates the document schema,
  not real-account required-workflow outcomes.

The completed full root Go race suite and desktop `VITEST_MAX_WORKERS=2 pnpm test`
are recorded in [full validation](replacement-full-validation.md). The full desktop run encountered failures
in unchanged Settings/App fixtures; the two Agent opaque-page cases pass in an
isolated one-worker retry (2 passed / 47 skipped, 19.24s). This does not attribute
every full-suite failure or establish a full-suite pass.

No real-account pinned-required-workflow end-to-end, native desktop/platform or
release acceptance was performed. No protocol numbers, storage migrations, native
side effects, user credentials or generated binaries are introduced by this change.
