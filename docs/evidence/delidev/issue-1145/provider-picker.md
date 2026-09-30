# Issue #1145 provider picker evidence

Inspected and based on `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` from freshly fetched `origin/main` on 2026-09-30. That revision includes the issue #1138 Settings opening-disposal behavior and retains API Accounts naming. The issue’s text specification is authoritative; its generated preview does not establish native implementation evidence.

## Implementation

The picker uses native immediate provider actions, independent empty-query/enabled-only bounded inventory pages, all four capability gates and one exact selected provider hint. Provider-row Add account opens the form directly. Existing credential save/connect, fresh provider checks, exact retry and unverified-health behavior are retained. Responsive creation-card styles use the specified 20px padding and 560px internal grid breakpoint.

## Automated validation

Commands ran from `apps/delidev` after root `pnpm install --frozen-lockfile`, building the API client and hydrating the exact source icon through `pnpm prepare:assets`. `git lfs fsck` and `git diff --check` passed.

- `pnpm exec vitest run src/account-settings.test.tsx src/settings.test.tsx src/settings-configuration.integration.test.tsx --maxWorkers 1 --no-file-parallelism --testTimeout 30000`: **53 tests passed**. This includes native button semantics/order, all four capability omissions, exact independent cursors/read retry/selected UUID, first/later/continuation empty states, stale and typed failures, Strict Mode/event-key replay, heading/button focus, provider authentication/disablement changes, transient keys, exact uncertain retries, no automatic validation/discovery and late-continuation guards. The Go integration fixture explicitly connects and validates a temporary owned keyless endpoint; it waits for the capability gate before opening creation.
- `pnpm typecheck`: passed.
- `pnpm test:bundle-dry-run`: all 8 package checks passed.
- `pnpm test:desktop-launch`: 15 checks passed; the unrelated asset-child SIGTERM fixture exceeded its unchanged 20-second timeout. Isolated `node --test --test-name-pattern='SIGTERM joins the active asset child' scripts/prepare-assets.test.mjs` passed (1 test).
- `pnpm test:widget`: passed.
- `pnpm build`: passed.

The first required `pnpm test` attempt completed 970 of 974 tests successfully, with three 5-second timeouts (New Project reopening, independent picker paging, tray presentation) and a Server preferences integration lookup failure. A serial broad rerun also encountered timing/lookup failures and was stopped; no full-suite pass is claimed. The focused rerun above passed after synchronizing the account integration fixture with capability readiness. Other checkouts were concurrently running builds and tests on this host. The final unmodified `pnpm test` rerun completed **958 of 975 tests successfully**, with 17 timeouts or asynchronous UI lookup failures across 11 files (App, backups, desktop, Settings configuration/devices/lifetime/preferences/providers/workspace, Settings and tray presentation). It stopped before the remaining script checks, which were executed separately above. Both exact-command failures remain visible; the host-load explanation is an inference, not a proven diagnosis or a full-suite pass. No repository timeout or concurrency setting was changed.

## Browser layout and native keyboard observations

A temporary, uncommitted React Strict Mode fixture mounted the actual Settings component and stylesheet with six enabled hosted provider entries. It used an in-memory Connect router and performed no real credential or account writes. The fixture and server are removed after inspection; screenshots remain local task artifacts, outside the repository.

| Viewport / measurement | Observed result |
| --- | --- |
| 1280×800 CSS px | Card 760px, padding 20px, internal grid 718px, two 353px columns, provider buttons 88px high. |
| 3680×2392 CSS px | Same 760px card and two-column grid, without horizontal clipping. |
| 640px width | Card 608px, internal grid 566px, two 277px columns. |
| Internal grid 559px / 560px | Exactly one column / two 274px columns. |
| 375px width | Card 343px, internal grid 301px, one column; 20px padding and no horizontal overflow. |
| Chrome at existing 200% page zoom | One-column picker with normal vertical scrolling and unclipped provider actions. |

In-app browser DOM observations verified the six server-ordered actions (OpenAI, Anthropic, OpenRouter, Vercel AI Gateway, xAI, DeepSeek), heading focus, Tab order, a visible 2px accent focus outline, Enter/Space progression and Back/Change return to the original provider button. Native Chrome accessibility and keyboard input at 200% zoom independently verified Space on DeepSeek opens its form, Change restores DeepSeek focus, and Escape closes Settings and restores its opener. Temporary tabs were closed and the in-app viewport override reset; Chrome’s existing zoom was unchanged. Local screenshots are attached to the chat rather than committed; generated app/API-client `dist` directories and temporary preview sources were removed after validation.

These are browser layout/native keyboard observations, not DeliDev CEF or supported-platform native acceptance. Component/router and temporary-server fixtures do not prove real hosted-provider authentication, native secure-storage behavior or macOS/Windows/Linux package acceptance. No such completion claim is made.

## PR #1183 merge repair, 2026-09-30

Reconciled the merge working tree from PR head `f373be4be0f81186804996c57938a24da0efce51` and freshly fetched `origin/main` at `7f356266fc195b1880ffac66a93dadab5c5a2df7`. The accepted issue #1135 AI API Keys terminology and its subscription/alias/diagnostic/keyless-retry tests remain intact. Direct actions replace the old radio/Continue controls, use Add AI API key / Back to AI API Keys / Entry name / Connect your entry, and retain the independent picker cursor and focus behavior. Incoming Diagnostics and release changes are preserved from the base.

- Rebuilt the generated API client and ran `pnpm typecheck`: passed.
- Required `pnpm test`: **1003 of 1013 tests passed**, with 10 failures across seven files. One provider-row assertion still expected Add account; it was corrected to Add AI API key and passed in the focused run. The other failures were 5-second deadlines or asynchronous UI lookups in App, Settings/configuration/devices/preferences/workspace and tray fixtures. No full-suite pass is claimed.
- The same three-file focused command above passed **57 component/router tests**. Its Go integration setup failed because a shared Go build-cache object for `golang.org/x/term` was missing, before that test executed.
- Retried the Go fixture with private temporary `GOCACHE=/tmp/issue-1145-go-cache`. The first cold-cache attempt built successfully but hit the provider-action lookup deadline. The warm-cache diagnostic rerun, `DEBUG_PRINT_LIMIT=30000 GOCACHE=/tmp/issue-1145-go-cache pnpm exec vitest run src/settings-configuration.integration.test.tsx --maxWorkers 1 --no-file-parallelism --testTimeout 30000`, **passed its one real Go integration test**. Thus all 58 focused regressions passed across the component and isolated integration runs; they are not reported as one uninterrupted three-file pass. Repository timing/concurrency settings were unchanged.
- `pnpm test:bundle-dry-run`: all 8 passed; `pnpm test:desktop-launch`: all 16 passed; `pnpm test:widget`, `pnpm build`, `git lfs fsck` and `git diff --check`: passed.

Generated app/API-client dist directories were removed after verification. Earlier browser measurements remain evidence for the original picker implementation; the reconciled naming was verified by tests/build, without another native CEF or provider acceptance claim.
