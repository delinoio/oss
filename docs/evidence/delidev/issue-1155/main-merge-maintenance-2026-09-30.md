# PR #1181 main-merge maintenance (2026-09-30)

## Revisions and conflict resolution

The maintenance run started from PR head
`5d1d67be96bc4a68980aecbdac6fc53e02fb2953` and merged the fetched main revision
`7f356266fc195b1880ffac66a93dadab5c5a2df7`. The merge commit containing this
record retains both histories without rebasing.

GitHub reported a merge conflict. The sole textual conflict was in
`apps/delidev/src/AGENTS.md`: the PR inserted its sidebar presentation rule next
to the account-settings rule, while main added the AI API Keys terminology
contract to that account rule. Resolution preserves the complete PR sidebar rule
and main's complete updated account rule. The desktop contract merged cleanly,
retaining the PR presentation and Settings-disposal corrections alongside main's
AI API Keys and Diagnostics contracts. No sidebar/controller/product code needed
an additional repair. The PR delta against the new main retains its original
presentation and regression tests.

The initial repair inventory had no unresolved, non-outdated Codex review threads
and no failing CI checks. These observations apply to the prior pushed head;
the repaired merge head requires its own checks and review.

## Verification

Commands ran from `apps/delidev` unless noted otherwise.

- `pnpm test`: generated API-client build and frontend type checking passed.
  The unit stage passed 1,007 of 1,009 tests in 84 of 86 files, including all
  11 PR sidebar regression cases. The unchanged
  `settings-preferences.integration.test.tsx` case failed an asynchronous lookup
  for Edit Server preferences, and the unchanged tray-presentation case exceeded
  its existing five-second deadline. The full pipeline stopped at this unit stage;
  it is not a green full-suite result.
- `pnpm exec vitest run src/pull-requests.test.tsx --maxWorkers=1`: 10 of 11
  passed. The missing-github-owner case failed an asynchronous repository-button
  lookup. An isolated rerun with
  `pnpm exec vitest run src/pull-requests.test.tsx --maxWorkers=1 -t 'keeps unconfigured github_owner'`
  passed that case (10 other cases skipped). The local results vary; their exact
  cause is not established. No product code, deadlines or test scheduler config
  were changed to accommodate the failures.
- Separate later pipeline stages passed: `pnpm test:bundle-dry-run` (8 cases),
  `pnpm test:desktop-launch` (16 cases), `pnpm test:widget` (native Swift widget
  fixtures), and `pnpm build` (production frontend bundle).
- Root `git diff --check` passed after reconciliation. The PR delta against main
  contains no Rust source changes. Generated app/client `dist` directories were
  removed after verification.

## Limits

This maintenance run performed no new rendered-browser or native desktop
acceptance. The original sidebar presentation record retains its browser evidence
and macOS CEF, Windows, Ubuntu X11 and native-zoom limits. Package fixtures and
frontend builds do not establish native runtime acceptance. Local test failures
remain visible; passing CI on the prior head does not prove the merge head.
