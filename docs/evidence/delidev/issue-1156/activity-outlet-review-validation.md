# Activity outlet background review repair

Date: 2026-09-30. Repair starts from merge revision `9c13acfc`. Codex thread: `PRRT_kwDORRAKg86ncfAo` ([review](https://github.com/delinoio/oss/pull/1182#discussion_r4142398154)). Host: macOS arm64.

The reported background break is reproducible. The Activity surface's `flex: 1 0 auto` had a block outlet parent, so it stayed at its natural height and left the remainder of the sidebar-list content area showing the shared pane color. Make only an outlet containing a visible Activity surface a growing, nonshrinking flex column. Its existing surface growth then fills available content height, while long filter content remains inside the original scrolling list. Hidden Activity surfaces do not match the selector. Keep the shared pane padding and rail/header/footer styles.

## Browser fixture evidence

Used the bundled Playwright with installed Chrome `154.0.8037.59`, a fresh owned browser context, the complete production stylesheet and synthetic sidebar/dialog/outlet markup. This was a CSS layout fixture without business RPCs, accounts or user state, not the running DeliDev app or packaged CEF. The temporary driver was `node /private/tmp/oss-1182-layout-check.cjs baseline` and then `fixed`; screenshots and metrics remained outside the checkout. The initial direct Chrome `--dump-dom` attempt timed out after 60 seconds and is not passing evidence; the controlled Playwright run completed.

| Case | Before | After |
| --- | --- | --- |
| 1440×900 desktop, short filters | Activity height 370.5px; lower content area `rgb(245, 246, 248)` | Activity height 748px in the 756px list, preserving its 8px bottom padding; lower content area `rgb(248, 249, 251)` |
| 759×900 native browser dialog, short filters | Activity height 370.5px; lower content area uses shared pane color | Activity height 698px in the 706px list; lower content area uses the Activity color |
| 960×640 desktop, long label | 760px scroll travel; Apply reachable | Same scroll travel; Apply reachable |
| 759×640 dialog, long label | 810px scroll travel; Apply reachable | Same scroll travel; Apply reachable |
| 1440×900, Activity hidden and Usage visible | Block outlet, zero flex growth, shared pane color | Same layout and color |

All five fixed-case assertions passed. Header and footer effective backgrounds remained `rgb(245, 246, 248)` throughout. Long content remained 1,248.03125px high and did not shrink into the scroll viewport. `pnpm exec vitest run src/activity-sidebar.test.tsx --maxWorkers=1` also passed all nine component tests.

This evidence establishes the bounded browser-fixture layout repair. Actual CEF visual geometry, keyboard/focus behavior, 200% zoom and Windows/X11 acceptance retain the original limitations in `activity-sidebar-validation.md`. No new native acceptance or full responsive-matrix claim is made. Resolve the review thread only after the repair is pushed.
