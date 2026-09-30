# AI Subscription UI validation

## Revision and scope

Issue #1143 implementation starts from main revision `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` on 2026-09-30. This record travels with the implementation commit. It covers the frontend UI stage: compact independent account/quota rows, ordered provider catalog, collapsed Advanced metadata workflows, generated-contract gating and Settings-opening disposal. Native Codex lifecycle and quota collection remain #1095/#1096 work; no Claude/Grok native support is asserted.

The inspected current generated account contract has no server-owned native brand, masked identity or friendly quota label fields. Production retains generic account marks, window IDs and absent masked identities; branded/masked examples occur only in presentation fixtures. Imported catalog marks retain their pinned MIT license/source notices.

## Preparation and executed checks

- `pnpm install --frozen-lockfile` from the isolated checkout: passed, including linked-worktree hooks.
- `git lfs pull --include=apps/delidev/src-tauri/icons/icon-source@2x.png --exclude=''`: passed before asset-consuming tests.
- Generated the API client with `pnpm --filter @delinoio/delidev-api-client build`; frontend type checks passed.
- Focused presentation/account/Settings lifetime regression run: 59 tests passed. The final presentation run includes active-only freshness expiry: 13 tests passed.
- Generated Connect router/controller fixtures and the feature-specific temporary Go-server integration: six tests passed. They verify exact subscription account-type filtering, disconnected rows, no lifecycle RPC from disabled controls, capability-read retries, retained reads, metadata editing/deletion, category-local filter retention and close/reopen disposal of accepted-but-delayed work.
- The first cold temporary-server run exceeded its setup timeout. An explicit `go build -o <temporary-path> ./cmds/delidev-cli` warmed the build cache; the subsequent real-server run passed. No Go or Rust source was changed.
- Unrestricted `pnpm test` from `apps/delidev`: failed with 14 timing-related failures (964 passed, 978 total), including existing 5-second test and 1-second asynchronous query deadlines. A two-worker rerun with unchanged timeouts passed 977 of 978 tests, leaving the existing Server preferences integration's asynchronous lookup deadline. That test also missed its one-second initial lookup in isolation. The host load average was above 200 during diagnosis; this observation does not establish a product regression or a clean default-run result.

- A two-worker run with temporary 5-second async query waits and 15-second test budgets passed 971 tests; seven real-server suites failed setup (eight skipped cases). Go reported missing shared toolchain sources and build-cache files. An issue-owned module/build cache and bounded Go parallelism isolate the subsequent run; assertions and committed runner configuration remain unchanged.

- Remaining pipeline stages executed independently: bundle dry-run tests 8/8 passed; desktop launch/asset-preparation tests 16/16 passed; widget fixture checks passed; `pnpm build` passed. The production output contains byte-identical original mark license/source notices. These checks exercise fixtures/builds and do not launch the packaged native application.

## Browser presentation evidence

Rendered the real `SubscriptionSettingsView` and repository stylesheet within a static Settings-shell fixture using React DOM server rendering, then inspected it with installed Chrome through Playwright. Accounts, masked example identities and 68/82/41/76% quotas are fabricated presentation data. This is a browser layout check, with no provider login or native lifecycle operation.

| CSS viewport | Main pane | Catalog columns | Result |
| --- | --- | --- | --- |
| 1440×900 | 1120px | 3 | No horizontal overflow or clipped controls |
| 960×640 | 672px | 2 | Rows wrap; no horizontal overflow or clipped controls |
| 720×640 | 688px | 2 | Existing compact navigation; no horizontal overflow or clipped controls |
| 560×640 | 528px | 1 | One-column cards and quotas; no horizontal overflow or clipped controls |
| 720×450 (1440×900 at a 200% CSS viewport equivalent) | 688px | 2 | No horizontal overflow or clipped controls |

Screenshots were temporary local validation output and are not committed. The zoom-equivalent check changes CSS viewport dimensions; it does not establish actual browser-zoom or native CEF-zoom acceptance. Component fixtures additionally check labeled controls, ellipsis Escape containment/opener restoration and exact-account disconnection confirmation.

## Remaining evidence limits

No packaged macOS, Windows or Ubuntu X11 CEF process was launched for this change. Native viewports, actual 200% zoom and keyboard traversal remain unverified platform evidence. Bundle/launch/widget fixture results, when listed above, are script/test evidence rather than native acceptance. No real-provider login, credential removal, native quota read or server-wide refresh operation was executed. Future production lifecycle callbacks require generated capabilities, authorization and an exact native profile.
