# PR #1182 Home/Models composition validation

Date: 2026-09-30. Validated implementation revision: `51e7374f3cfc7e783a60f275264acf9c7cbbd3be`, composing main `6d7004d2e37c42da5febd9dd3db06fb1fade019e`. Host: macOS arm64, Node.js `v24.11.0`, Chrome `154.0.8037.59`.

## Required frontend pipeline

Ran package-local `pnpm test` from `apps/delidev`. Only a temporary single-worker Vitest resource limit was applied; original configuration was restored in cleanup. No test, hook or asynchronous lookup timeout was temporarily changed. Main's committed fixture-owned UI waits and this PR's real delayed save-acknowledgment regression remain intact.

Generated API client build and desktop typecheck passed. All 1,228 tests in 95 Vitest files passed (169.44 seconds). Native package dry-run verifiers passed all eight tests, asset/launcher checks passed all 16, and widget fixtures plus the production frontend build passed. Assets were hydrated before validation. Generated `apps/delidev/dist` and `packages/delidev-api-client/dist` were removed afterward; no repository-owned distribution output remains.

Focused Activity, Sidebar/App, Home navigation, Models and preferences verification passed all 117 tests in six files before the merge commit, as recorded in `main-6d700-merge-validation.md`. This merge changes no Go/Rust source, Go module, protocol or API-client source; earlier Go and CI-script evidence remains in its original records rather than being reported as rerun here.

## Activity browser layout

Reran `node /private/tmp/oss-1182-layout-check.cjs fixed` with the complete composed stylesheet, bundled Playwright, installed Chrome and a fresh owned headless context. All five existing fixture assertions passed: short desktop/dialog filters fill unused Activity content height; long desktop/dialog filters keep 760px/810px scroll travel and reachable Apply; hidden Activity leaves the outlet as a non-growing block with the shared color. Header/footer colors and list padding remain intact. The browser closed after verification.

This synthetic sidebar/dialog/outlet fixture uses no account or business RPC state. It establishes bounded CSS composition, not the running app's Home accumulation or packaged CEF behavior. Component tests establish the independent Home/Models integration boundary.

## Evidence and limits

Local logs remain at `/private/tmp/oss-1182-0910-full.log`, `/private/tmp/oss-1182-6d700-focused.log` and `/private/tmp/oss-1182-0910-layout-fixed.json`. Earlier results and attempts remain unchanged in their original records.

Native CEF viewport/focus, actual 200% zoom and Windows/X11 visuals retain the original limits in `activity-sidebar-validation.md`. No native/platform acceptance is added. Remote CI and Codex review must evaluate the new pushed head; pending reviews and the sole observed Cloudflare check for the previous head do not validate these local commits.
