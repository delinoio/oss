# Manual PR fixes: current local validation

Code revision: `0ce9f8e06` on `kdy1/fix-1081-manual-pr-fixes`, including main
`574c1a92c`. Date: 2026-09-30. Earlier checks below identify their pre-merge
scope explicitly. Historical PR #1190 results are not current evidence.

## Completed checks

- Root `pnpm install`: passed, including linked-worktree Lefthook installation.
  Hydrated `apps/delidev/src-tauri/icons/icon-source@2x.png` with Git LFS before
  asset consumption. Generated required API-client, async-commit-hook embedded
  and DevHud administrator embedded output before compilation.
- `pnpm proto:check`: passed after main reconciliation. Formatting/lint,
  pointer-only breaking baseline and regenerated Go/TypeScript freshness passed.
- Pre-merge `GOMAXPROCS=4 go vet ./cmds/delidev-cli/...`: passed. A current-main
  `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...` rerun is pending below.
- Pre-merge focused race check:
  `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli ./cmds/delidev-cli/internal/domain ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/worker -run 'ManualPRFixRPC|PRFix|SessionDeletionCompletedProof' -count=1 -timeout=15m`:
  passed all five packages. Covers retained review/approved-review/CI/conflict
  acceptance, exact repository binding, paused exclusion, explicit missing
  configuration, concurrent exact receipt ownership, CLI capability membership,
  original push/cleanup handling, clock skew and restored deletion scopes.
- Pre-merge native Git retry:
  `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/workspace -run 'PRGitTool|PRLocalGit' -count=1 -timeout=15m`:
  passed (697.457 seconds). Covers bound fork targeting, missing write access,
  native success without push, ignored build output, moved remote head,
  immutable/forged scope, one-shot publication, merge/rebase and closed operands.
- The same native check after main and the workspace identity guard failed in
  fork tool preparation with an operation timeout (485.200 seconds total).
  Merge/rebase and operand checks completed without reported failures. Its
  isolated retry
  `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/workspace -run '^TestPRGitToolForkPushIsOriginalBoundAndNeverReplayed$' -count=1 -timeout=15m`
  passed (279.478 seconds), including the new replacement-symlink authority and
  proof rejection before restoring the original workspace and verifying push.
- Frontend API-client build and `pnpm typecheck`: passed on current main.
- Pre-merge `pnpm test:unit --maxWorkers=1`: 1,238 passed, three App tests failed
  their five-second deadlines. All five manual-fix UI tests passed, including
  exact revisions, uncertain/malformed acknowledgement retention, unsupported
  capability, the 4,096-byte title bound and additive unknown capabilities.
  Isolated `pnpm test:unit src/App.test.tsx --maxWorkers=1` then passed all 44
  tests with unchanged deadlines.
- Pre-merge frontend `pnpm test:bundle-dry-run && pnpm test:desktop-launch && pnpm test:widget && pnpm build`:
  passed bundle fixtures, desktop launch/assets fixtures, widget checks and
  production frontend build.

## Required broad failures and remaining reruns

The required initial root `go test -race ./cmds/delidev-cli/...` failed with
native fixture deadline/process-cleanup failures and package timeouts. The
initial frontend `pnpm test` failed (17 files, 64 test failures; 1,177 passed).
These failures are not a full-suite pass or proof of a clean baseline.

The current-main frontend `pnpm test` also failed: 83 files passed, 14 failed;
1,229 tests passed, seven failed and 12 skipped. Seven App/Doctor/tray cases
exceeded five seconds, and eleven settings integration suites failed their
120-second Go-build preparation. It therefore did not reach its later packaging
and build steps. A serial current-main unit rerun is still pending and has also
reported settings preparation timeouts; it cannot yet establish a complete pass.

Current-main root retry
`GOMAXPROCS=4 go test -race -p 2 ./cmds/delidev-cli/... -timeout=20m`
and current-main vet are still running at publication. Their results must be
recorded in a new evidence update when complete. No all-package race/vet pass
is claimed for this revision. Required production/native deadlines were not
widened. The host had substantial concurrent native/test/build load, but no
independent baseline comparison proves that load explains every failure.

## Limits

All native/provider fixtures use temporary state, controlled repositories and
scripted observations without user credentials. They do not prove real-account
inference, actual GitHub fix publication, every-platform native sandbox behavior,
packaged desktop or release acceptance. Only the advertised Codex Git profile is
supported. Generated output is untracked and will be removed from the final
worktree after outstanding compilation finishes.
