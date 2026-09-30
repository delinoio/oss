# Issue #1157 Projects settings presentation

Date: 2026-09-30. Base inspected: `74701b8948694e2bf8f8ba6d07e596c2d2f358a7` (`origin/main`). The implementation and regression tests are in the commit containing this record.

## Implemented boundary

Projects alone has the centered 820px column, approved empty panel, ordered divided project list, four create/edit groups and scoped deletion panel. Existing field explanations, selectors, primary/restriction semantics, full documents, captured revisions, exact uncertain requests and creation gates remain. Issue #1138 opening disposal, targeted entry, sibling state and the shared shell remain authoritative. The desktop contract and the parent/source AGENTS files define the presentation exception; no public API, server behavior, storage, polling, dependency, product logs or native window changes are added.

## Component and server verification

The focused Settings suites passed all 50 tests, including 16 new Projects cases. They cover successful versus failed emptiness, continuation/later pages, server order and complete IDs, unsupported schemas, read errors/correlation, retained refresh results, all field groups, ordering/primary clearing, both restriction states, full-document preservation, stale/error Save guards, exact save/delete retries, discard-on-close, targeted Name focus and category isolation. Existing lifetime tests preserve late-result, sibling-cache/native completion and reconnect coverage.

A full package-local run initially reported 972 passes and four real-server UI-read failures (Workspace, configuration, paired devices and Claude settings). An isolated Workspace run passed, including real checkout inspection, project creation and deletion. A temporary comparison using unchanged base versions of the presentation sources reproduced a provider-save acknowledgment timeout in the configuration integration test; the base Claude test passed. The shared real-server fixture now uses a bounded five-second Testing Library UI wait, restored after fixture cleanup, instead of treating its one-second default as a Go RPC SLA. Assertions and production behavior are unchanged.

The final `pnpm test` run from `apps/delidev` passed: generated-client preparation and TypeScript checking, all 976 tests in 85 files, eight bundle dry-run verifier tests, 16 asset/desktop-launch tests, macOS widget fixture checks and the production Rsbuild bundle. The test-fixture repair preserves the caller's React Strict Mode setting. A final `pnpm typecheck` also passed. Generated repository-owned `dist` directories were removed before the final commit; temporary source/browser fixtures and their server were removed/stopped. No validation-only Vitest configuration change is committed.

Temporary validation settings use one Vitest worker, a 30-second test timeout and a 90-second default hook timeout, with `GOMAXPROCS=2`, to limit contention with concurrent local work. Tests with explicit timeout overrides retain them. The ordinary committed Vitest configuration is restored afterward. These settings are validation context, not a product response-time guarantee.

## Browser visual verification

The macOS in-app Chromium browser rendered the actual Settings components against a generated-resource in-memory Connect fixture. Its temporary production bundle used `style-src 'self'` and no inline styles within the application root. Browser console inspection showed no application CSP errors. The fixture and server are temporary and are excluded from the final worktree.

Empty, loaded, create, edit and deletion states were inspected at 1440×900, 1280×820, 960×640 and 640×410 CSS pixels. The first two widths measured an 820px column; the latter widths measured 672px and 608px. Every state had no horizontal content overflow. Complete row/repository IDs and field groups remained present. The 640px view used the grouped category select, stacked actions and vertical scrolling to Save/Cancel. The empty panel measured 300px and its primary button measured 36px. Keyboard focus measured a visible 3px #0067D9 outline; browser Escape closed the dialog and restored its opener. Browser top-layer modal behavior was observed. These observations do not establish native CEF keyboard containment.

Calculated sRGB contrast: primary text/white 16.24:1, secondary text/white 6.04:1, secondary text/gray 5.55:1, primary-action white/accent 5.33:1, control border/white 3.66:1 and focus accent/gray 4.89:1. Decorative panel borders are not control boundaries. Disabled controls retain their existing semantics.

## Remaining acceptance limits

No source-bound native DeliDev CEF smoke run was performed. Native macOS, Windows and Ubuntu X11 dialog/background inertness, keyboard containment and host opener restoration remain unverified. Actual native 200% zoom is also unverified: 640×410 tests the corresponding reduced CSS viewport for a 1280×820 surface but is reflow evidence, not actual zoom acceptance. Browser/component/build-verifier/widget fixture success cannot establish supported native platform acceptance or publication. No native geometry, signing or release claim is made.
