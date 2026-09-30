# Server preferences CI wait validation

## Source and revision

- Date: 2026-09-30.
- Issue: https://github.com/delinoio/oss/issues/1136.
- PR: https://github.com/delinoio/oss/pull/1172.
- Inspected first-parent revision: `11c0066786727e66ca02ab0e10bbe3d301c66ae4`.
- Failed CI: [DevHud Protocol and Client, run 36688202298](https://github.com/delinoio/oss/actions/runs/36688202298/job/109798967918).

The job passed schema lint, breaking/freshness checks and both API client suites. Its DeliDev frontend run passed 1,050 tests and failed only `settings-preferences.integration.test.tsx`: the first post-save `findByRole` could not observe **Edit Server preferences** within Testing Library's default one-second wait.

Source inspection confirms that the button appears after the real Go `SaveConfiguration` RPC is acknowledged, the editor closes and the active resource query refetches. The test now gives only its two post-save waits a five-second budget; it still fails if the expected button never appears. The existing 15-second test limit, global wait defaults, singleton assertions, exact Go defaults, unchanged resource ID, revision increment and edited-document assertions remain intact. The comment records the scope and replacement condition for this timing allowance. No product behavior, transport, fixture ownership or repository contract changes.

## Verification

Required administrator and async-commit-hook embeds and the DeliDev generated client were built before Go/frontend compilation. A private temporary Go cache was warmed with `GOMAXPROCS=2 go build -p 2 -o <temporary validation binary> ./cmds/delidev-cli`; compilation passed.

- `GOCACHE=<private temporary cache> GOMAXPROCS=2 pnpm exec vitest run src/settings-preferences.integration.test.tsx`: one test passed against its independently owned real Go server.
- `GOCACHE=<private temporary cache> GOMAXPROCS=2 pnpm test` from `apps/delidev`: all 1,051 tests in 88 files passed, followed by eight packaging fixtures, sixteen asset/desktop-launch fixtures, widget checks and the production build.
- The full gate used the original tracked Vitest configuration and Testing Library setup. No global wait increase, worker limit or runner timeout override was used.
- `git diff --check`: passed.

Generated repository-owned `dist` output and this pass's temporary Go cache/binary are removed before the final clean-worktree check. This evidence does not supersede the earlier incomplete broad local Go gate or claim native/account/platform acceptance. Current-head CI and review after the repair push remain separate evidence.
