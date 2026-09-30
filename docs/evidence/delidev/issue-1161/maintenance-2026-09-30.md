# Home navigation maintenance: 2026-09-30

PR: https://github.com/delinoio/oss/pull/1200. Starting head: `e519249c0f012a8609c9497eb51cded2c7aaf68c`. Fetched main at `36736923e` after the Windows CI shards, Home-only header actions, direct API-provider actions and schedule frequency presets landed.

The previously clean, published worktree was absent from disk and Git's active worktree inventory; the existing issue branch and remote-tracking branch still matched the published head. Recreated the original attached checkout at its recorded path and checked out the existing PR. The primary checkout's modified app instructions and deleted icon were preserved.

Resolved App, Sidebar, their tests and the desktop contract together. Preserve main's Home-only Inbox/Search action rendering and synchronous wide/compact header focus handoff, while retaining Home accumulation, enum-based local/saved footer presentation and Settings-active read suspension. The merged fixtures retain both prop-controlled and stateful navigation coverage. Header scope/scroll tests now reach automatic continuations and verify retained catalog/global/named boundaries instead of manual paging controls; other destinations keep their existing paging.

Hydrated the canonical icon from the local LFS cache, regenerated the API client and passed `pnpm typecheck`. Focused validation passed 5 files / 76 tests:

```sh
GOMAXPROCS=2 GOFLAGS=-p=2 GOCACHE=/private/tmp/delidev-home-1161-go-cache pnpm exec vitest run src/sidebar.test.tsx src/App.test.tsx src/home-navigation.test.ts src/desktop.test.tsx src/cache.test.ts --maxWorkers=1 --fileParallelism=false --testTimeout=30000
```

This is component/merge evidence, not packaged CEF or native-platform acceptance. The prior browser and 5,050-row evidence remains qualified in `home-navigation.md`.
