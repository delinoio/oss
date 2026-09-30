# Issue #1137 review: service-aware advanced Start

Codex thread `PRRT_kwDORRAKg86nhG28` on PR #1228 identifies that the advanced
native Start capability called ordinary explicit CLI Start, bypassing the
installed-service admission enforced by automatic desktop launch.

The native Connector now uses Go's desktop-launch admission for advanced Start.
An explicit advanced request may still reopen ordinary stopped intent, but it
cannot publish intent or spawn a detached competitor in a registered scope.
Ordinary explicit CLI Start remains unchanged. The native instructions and
desktop/user-service contracts record this boundary.

A native fixture models independent explicit CLI versus service-aware admission:
it returns `ServiceManaged`, preserves original registration bytes, and creates
neither a competitor marker nor desktop-client pairing state. The complete
non-ignored native library suite passes 17 tests, with five real-sidecar fixtures
explicitly ignored. Root Cargo is rerun for the Rust change; final broad and
rebuilt-sidecar outcomes are recorded separately in the repair evidence.
