# New schedule creation validation

Source implementation: `a7537fccc`; final CSS viewport bound is included in that commit.
Merge validation base: `9d110ced7` (merged by `eb4a6d2cb`).

The creation-only frontend now separates Task, Execution and Repeat schedule,
adds bounded Daily/Weekdays/Weekly/Custom authoring, keeps mounted reference
overrides behind a disclosure, and reserves the persistent main-content footer's
actual wrapped height. Definition, Local provenance and retained-mutation
ownership remain in the existing editor. Existing schedule editing, controls
and history retain their original rendering and service contract.

On macOS arm64, the generated-client build and desktop type check pass. All
31 focused schedule checks pass, including canonical cron transitions, invalid
time refusal, strict schema-v1 bytes, UTF-8 prompt bounds, bounded catalog
statuses/paging, fresh Local proof and original uncertain retry identity. The
real-server workspace fixture retains its subsequent resume/pause/Run now/
delete/history assertions. A serial run of App, workspace and preferences
fixtures passes all 39 checks; the unchanged-base App/workspace comparison
passes all 38 checks. The final production frontend build, eight bundle
verifier checks, 16 asset/desktop launcher checks, native Swift widget fixtures,
102 repository contract checks and workflow validation also pass.

The required package `pnpm test` was executed at default concurrency and then
with a temporary one-worker Vitest setting, restored after each run. The first
run reports 932 passed, 50 failed and two skipped assertions. The first serial
run reports 980 passed and four failed assertions, including original App
deadlines and real-server setup observations. Their focused changed-code rerun
passes. The next serial package run reports 974 passed, two failed and eight
skipped assertions, with eight additional suite setup failures caused by Go
build-cache files disappearing during compilation. The remaining failures are
the workspace repository observation and the tray fixture's five-second
deadline. These results do not establish a complete package pass or prove every
failed check is a baseline failure. A private Go-cache rerun is pending; no
product or committed test timeout/worker setting has been changed.

A disposable in-memory Connect fixture using the actual App, creation component
and shared stylesheet was inspected separately in the browser. It grants no
real-server access, runs no inference and sends no real schedule write. At
1600×1000 CSS pixels the grid measures 1080px, Repeat measures 320px, and the
whole collapsed Execution card clears the footer by 16px. At 1280×800 the
right column remains 320px; below 1280 the sections stack. At 960×640 and
720×640, expanded remote-reference controls and a safe server rejection remain
scrollable above the footer with no horizontal overflow. At 420×640 the footer
wraps to 113.5px and the scroll region ends exactly at its top. The 720px drawer
closes through Escape and Close, restores the opener's focus, and preserves the
name, prompt and override draft.

Actual Chrome 200% zoom was verified through the browser's native zoom control,
with an effective 864×453 CSS viewport. This exposed the shared shell's 480px
CSS minimum extending creation actions below view. A creation-scoped 100dvh
main-content bound fixes that case while preserving the native minimum and
other surfaces. After the fix, the footer ends at 453.5px, its scroll region ends
at its top, document/content widths do not overflow, ArrowRight changes the
native Execute/Plan radio, and Tab from the final checkbox reaches a fully
visible Cancel button. Expanded disclosure, Custom input and a pagination
connection failure were exercised at this zoom. The temporary zoom and viewport
overrides were reset. This is browser evidence, not native desktop acceptance.

The supported `pnpm dev:desktop` path hydrated the exact LFS source icon, built
the sidecar/widget/native host and produced an ad-hoc signed macOS CEF bundle
in an isolated target. Launch exits with `SidecarFailed` and “Opening in
existing browser session” while another unrelated DeliDev CEF fixture owns the
shared profile; that process was preserved. No successful native creation
viewport, keyboard or 200% acceptance is claimed. A subsequent disposable
client/server pairing attempt was rejected by automatic approval review because
creating the grant adds security-sensitive client access without specific
authorization. The grant was not created. Native paired-server smoke remains
blocked on that authorization and isolated runtime ownership; Windows and
Ubuntu native runs are unavailable on this host. The disposable unpaired server
and issue-only native target were removed. No native config, dependency, RPC
or persisted schema change is part of this increment.


Browser fixture captures (no native acceptance implied):

- [Default 1600×1000](creation-1600.png)
- [960×640 with safe rejection](creation-960-error.png)
- [720×640 with safe rejection](creation-720-error.png)
- [420×640 wrapped footer](creation-420-error.png)
- [Actual Chrome 200% zoom](creation-200-percent.png)
