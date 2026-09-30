# PR #1232 superseded browser-child invisibility

Codex thread `PRRT_kwDORRAKg86ntyFm` on `e463342d457c49c1a3891e9ebfe759fff5cee3d4`
identified that changing the selected tab or closing its final tab discarded the
old child handle after an asynchronous CEF close request. A subsequent Hide could
therefore find no handle while the superseded child remained visible.

Tab replacement now synchronously unmaps every affected shared child before
discarding any handle or advancing any generation. An unmap failure preserves all
original views for exact Hide cleanup and starts no replacement. After successful
unmapping, close requests run outside native state, followed by the existing
per-view replacement attempts. Background-tab closure/current-tab selection retain
their existing child. Reservation/open also unmap before releasing a superseded
view. Removal and Quit retain closing handles until the original callbacks, and
window teardown retains a closing view on unmap failure while requesting closure.
Native unmap failures log only the stable operation and typed failure.

Focused verification on 2026-10-01 (Asia/Seoul):

`CEF_PATH=/Users/kdy1/Library/Caches/tauri-cef
CARGO_TARGET_DIR=/Users/kdy1/projects/oss/target RUSTC_WRAPPER=
CARGO_BUILD_JOBS=2 TMPDIR=/private/tmp cargo test -p delidev-desktop
--features desktop-host,custom-protocol browser_host::tests -- --test-threads=1`
passed all 26 browser-host tests (10.62 seconds), compiling against the unchanged
Tauri CEF pin. New controlled adapters verify selected/final-tab invisibility
before generation advance and Hide while the original close callback remains
pending, all-original-view retention when the second shared unmap fails, and
preserved reservation/cleanup identity after failed presentation unmapping.
Existing address-drain, late-callback, shared recreation, background-tab and
whole-shutdown cleanup regressions also pass.

These are temporary-state and controlled-adapter results. Actual macOS, Windows
and X11 raw-child presentation, real account/cookie acceptance, renderer flush and
release acceptance remain unperformed. Final broad-suite results are separate.
