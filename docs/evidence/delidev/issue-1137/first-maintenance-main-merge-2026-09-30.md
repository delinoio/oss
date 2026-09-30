# Issue #1137 first PR maintenance — 2026-09-30

PR #1228 initially publishes head `1d12c33c60ce5d3678c96975cf4eeaa64185a51c`.
Its first maintenance pass finds a conflict with main, no unresolved Codex threads
and no failing checks (one successful Cloudflare preview). Code/security reviews
are still running; this observation is not approval or complete CI acceptance.

Main `65eca3341e2180f676fc81c24ebccbc82b67f344` is merged, preserving Runner Device
terminology (#1220), pinned required-workflow semantics (#1219), Activity filters
(#1217) and repository-inspection wire reservations (#1214). Only the Settings
navigation test and desktop contract conflict. Both retain main's Runner Devices
and issue #1137's Connection & diagnostics, with unchanged category IDs and all
16 categories. Native startup sources are unchanged by this repair.

The API-client build, frontend type check and focused App, desktop, sidebar,
Settings and Doctor tests pass: 158 tests in five files, 41.89 seconds. The merged
Go CLI builds and Go vet passes. Required embedded bundles are regenerated for
validation and commit hooks. Full frontend pipeline results are appended below;
prior broad-suite and native-platform limits remain in the current-main record.

The first complete merged frontend run passes 1,267 tests but fails one newly
merged Activity assertion that still requires its fixture lifecycle control in
the ordinary footer. Issue #1137 explicitly relocates that control. The assertion
now verifies its absence and the generic `This computer · Connected` presentation,
retaining every Activity filter, request ID, paging and mutation assertion. The
focused Activity suite passes all nine cases. A complete pipeline rerun follows.

The final required `pnpm test` pipeline passes: 1,268 tests in 98 files (208.16
seconds), bundle/package dry-run tests 8/8, launcher/assets tests 16/16, widget
fixtures, type checking and production build. The run uses one worker, a temporary
60-second default test budget and `GOMAXPROCS=2` for fixture children; the original
runner configuration is restored. Protocol checks and all eight structure,
allocation and LFS policy tests pass again without generated-source drift.

Generated repository-owned `dist` output is removed before the final repair push.
This success does not erase prior failures or extend the recorded native/platform
acceptance boundary. The repair does not change Rust startup sources; the earlier
root Cargo/binpm failure and macOS arm64 native rerun remain separately recorded.
