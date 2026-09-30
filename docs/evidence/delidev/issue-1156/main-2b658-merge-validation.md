# PR #1182 Home-header integration

Date: 2026-09-30. Previous PR head: `8073c090f7b48687bdc3b7a8c59b53eb1d383ce3`. Merged main: `2b658e05353a858e328f82a636d8bc49dbccb709`. Host: macOS arm64. The recorded managed checkout was clean and matched the existing PR branch/head before repair.

The desktop instructions conflict consists of independent Activity and Home-only Inbox/Search rules at the same location. Retain both, alongside GitHub Integrations and Diagnostics ownership. Preserve main's consumed wide/compact focus handoff and the Activity-only filter styling. Compose main's Windows Go CI shard rules without changing their ownership or implementation.

Generated DeliDev client build and desktop typecheck passed. `pnpm exec vitest run src/activity-sidebar.test.tsx src/sidebar.test.tsx src/App.test.tsx --maxWorkers=1` passed all 69 tests across three files. `node --test scripts/ci/ci-contract.test.mjs scripts/ci/go-test.test.mjs scripts/ci/plan.test.mjs` passed all 56 tests. These fixtures do not establish native UI/platform acceptance or measured Windows CI improvement.

Before this merge, GitHub Actions run `36688081402` and Cloudflare Pages succeeded for `8073c090f7b48687bdc3b7a8c59b53eb1d383ce3`. This evidence does not apply to the next pushed head. One new Codex CSS review thread was recorded for a separate repair after the merge commit; it remains unresolved until that fix is pushed.
