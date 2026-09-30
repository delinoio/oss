# Issue #1161 Home navigation evidence

Date: 2026-09-30. Initial source base: `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` (fresh main, including the #1163 ownership split). Feature commit: `179ec2d87`. Integrated main `8a626cc8a6252f47c5d66f2e7b857cddf7354e89` in merge revision `f94b17a60f5edfb4e47c9dd6d76442f94c7c8f50`, preserving the AI API Keys, Diagnostics, unified PR activity, Pull requests sidebar and Agent Worker settings changes. Issue: https://github.com/delinoio/oss/issues/1161.

## Implemented behavior

Home/Sessions and New Session share independent accumulating catalog, unfiltered global-session and expanded named-project chains. Every generated authenticated Connect Query request is bounded to 50 rows. Visible anchors within 96px continue serially, including empty batches; repeated continuations fail visibly. Accepted rows are typed navigation projections, deduplicated by original IDs with exact bigint revisions. Full Resource documents and completed batch queries do not accumulate. The ordinary eight-extra-inactive-query rule remains, with live reads protected until settlement. There is no application row-count or Search-only cutoff; retained Home metadata grows with reached inventory.

Scope generations protect cancellation, filter changes, transport replacement and Strict Mode reactivation. Retry preserves the failed token, typed cursor expiry offers scope-local Reload list, and refresh only revisits accepted ranges. Previous rows survive read failures and failed reloads. Fallback-to-named project migration preserves the group, selected row and focused DOM node until authoritative reconciliation. Selection, loaded ranges and Home scroll survive Sessions/New Session and same-identity navigation; connection replacement clears them.

The fixed header/New session action, archive options popup, successful-empty project entry, flat 34px rows and pale-blue selection follow the text specification. A 48px local/saved enum-based summary reveals the original mounted management controllers, with a 35%-capped independently scrolling footer. Disclosure and scrolling are read-only. Existing Settings current-opening disposal, deferred entry and exact uncertain-operation behavior remain intact.

## Component and retention checks

The owned `sidebar.test.tsx`, `home-navigation.test.ts`, `App.test.tsx`, `desktop.test.tsx` and `cache.test.ts` cover threshold edges, independent scope tokens, deduplication, empty continuation ranges, repeated-token stopping, delayed ignored-abort cancellation, archive reset, typed cursor expiry, retained refresh data, exact retries, fallback transfer with DOM/focus preservation, popup Escape/opener focus, original management controller identity, targeted Settings entry/disposal, connection identity and the unchanged ordinary cache bound. The pure retention fixture accepts 5,050 rows through 101 batches and inspects every retained row/page to exclude full documents and arbitrary source fields.

## Browser checks

Executed against a temporary Rsbuild static fixture with synthetic UUID-v7 project/session records and createRouterTransport, in isolated Chromium `149.0.7827.55`. The product's fixed development port was occupied and failed as contracted; the diagnostic fixture was built separately and served on its own temporary static-preview port, without changing product configuration.

At 1440×900, 960×640, 1100×900, 1101×900 and 760×640, measured the 52px rail, 288/256px pane, 36px New session action and 48px summary. The list scrolls independently while header/footer bounds stay fixed. Expanded management stayed within 35% (306.59px in a 900px pane) and scrolled internally. Document width never exceeded viewport width. At 759px, the browser's native modal drawer contained interaction, popup Escape preserved the drawer, drawer Escape restored its opener, and wide resize left one management controller. A 720×450 CSS viewport checked the equivalent 200% desktop reflow without horizontal clipping; this is a CSS viewport equivalence, not an OS zoom or native CEF acceptance claim.

A separate actual browser run scrolled through **5,050 unique rows** via **101 global batches**, all requesting exactly 50 records. The original focused conversation remained focused. Only four query-cache entries remained (small refresh/status observers); zero completed batch queries remained. Both browser runs reported zero page errors. Screenshots and generated fixture/assets stayed temporary and untracked; the approved AI preview was not committed or hosted.

## Validation limits and failed attempts

The exploratory broad test invocation encountered host-contention timeouts. The first complete `pnpm test` run passed 968 tests and failed two Settings integration observation waits. A subsequent complete run passed 962 tests but had seven fixture-build suite failures/eight skipped tests; a focused rerun reproduced missing compiler objects in the machine's shared Go cache. No product error was inferred from these build failures. A private temporary Go cache, bounded compiler concurrency and a successful CLI prebuild isolate the final run from that external cache. Serial Vitest execution, a 30-second default test timeout, a 180-second setup-hook timeout and a five-second async observation allowance were temporary local harness settings; original repository test configuration/setup are restored afterward.

No Rust/native interface was changed. Actual packaged CEF interaction, macOS/Windows/Linux native viewport acceptance, OS zoom, production installation, signing and deployment were not performed or claimed. Browser/component results do not establish those targets. The historical `docs/cmds-delidev-evidence.md` remains frozen under the latest source-ownership contract; this issue owns this independent record instead.

## Executed complete validation

Before integrating the newer main commits, `pnpm test` from `apps/delidev` completed successfully with 85 test files and 970 tests passing, API-client generation, TypeScript checking, package/launcher checks, widget fixture checks and the frontend build. The command used `GOMAXPROCS=2 GOFLAGS=-p=2 GOCACHE=/private/tmp/delidev-home-1161-go-cache` with the temporary serial/timeout harness described above. The canonical LFS icon was hydrated before validation; generated repository-owned `dist` output is removed from the final checkout.

At merge revision `f94b17a60f5edfb4e47c9dd6d76442f94c7c8f50`, the same complete command also passed: **89 test files / 1,042 tests**, followed by successful package/launcher checks, widget fixtures and production build. Vitest completed in 276.88 seconds. Repository test setup/configuration were restored and the final source diff has no temporary fixture or harness edits.
