# PR #1201 review and CI repairs

Recorded on 2026-09-30. Rust source revision:
`820326064d15684acd3eda0ce00c69ab06506317`.
The published head inspected before repair was
`ed02fb4352e26242efab991fd7867657f0048eff`.

## Repairs

- `61b778d1dde0da1f01a50c4a469c28945a434903` classifies IPv4-mapped IPv6
  loopback addresses under the fixed API/development-port denial and bounded
  explicit-origin policy. Navigation and WebSocket resource fixtures cover both.
- `8a06cb026ed70586531564c2a2f456bf726469d7` separates completed local purge
  from deferred exact server acknowledgment. Offline acknowledgment preserves
  its original intent and successful normal quit. Local storage failures remain
  failures; exhausted acknowledgment budgets remain pending.
- `d49a4ce121c884b6ac88834463de0ad7ea373569` persists original saved-connection
  browser purge scopes before native credential removal and discovers completed
  CLI removals through retained non-secret connection tombstones. Reopening is
  denied and all device browser data waits for independently completed CEF
  shutdown. Local purge never claims a remote account acknowledgment.
- `ac9f467f77376f0fd438a4d0e9b1c2b5db191770` moves profile preparation,
  tab persistence and removal intents to serialized workers without native-state
  locks held during I/O. CEF work remains on the UI loop. Trusted documents and
  exact presentation/control generations are rechecked. Address callbacks use
  bounded coalescing and one tracked worker joined before directory purge.
- `acd7fbb52a82ca1e808a836689e4c61ca706063d` fixes the Ubuntu Clippy compiler
  failure by taking the raw parent handle from `WebviewWindow`, which implements
  `HasWindowHandle` at the pinned Tauri revision, rather than its `Webview`.
- `820326064d15684acd3eda0ce00c69ab06506317` fixes the additional Clippy
  conditionals, redundant assertion and native argument-count findings. Internal
  control inputs are grouped. Tauri-injected command state has narrowly explained
  exceptions; the renderer's typed command fields remain unchanged.

The four handled Codex threads are the connection purge, offline acknowledgment,
mapped-loopback policy and UI-thread persistence findings. Each root cause was
committed separately. The pre-repair CI run completed with Rust Clippy as its only
failing job (and therefore a failed aggregate); other executed checks passed.
A new push requires fresh CI and review evidence.

## Executed validation

All Rust commands ran from the repository root on macOS arm64 with
`RUSTC_WRAPPER=`, `TMPDIR=/private/tmp`, `CARGO_BUILD_JOBS=3` and the existing pinned
CEF cache. No Tauri/CEF version or dependency pin changed.

- `cargo clippy -p delidev-desktop --all-targets --all-features -- -D warnings`
  passed after the repairs, including the optional packaging CLI build.
- `cargo test -p delidev-desktop --features desktop-host,custom-protocol` passed
  31 tests: 19 library tests and 12 host tests. Four OS-specific acceptance tests
  remain ignored. The nine browser checks include blocked storage, superseded
  opens, stale callbacks, whole-scope purge preserving a neighboring device,
  original offline intents and the controlled late-flush/shutdown proof.
- Required root `cargo test` ran and failed in two unchanged Clibox wait tests.
  `proxy_and_log_environment_cannot_expose_or_redirect_requests` observed
  `dns_configuration` and exit 1 where its success fixture expected exit 0.
  `tls_dependency_errors_and_ca_overrides_are_isolated_and_redacted` observed
  `dns_configuration` where it expected `tls_certificate`. This is not a passing
  whole-workspace test run. No Clibox source or assertion was changed.
- The earlier required broad DeliDev Go race run was still processing at this
  record's creation. In addition to its documented session-workspace and harness
  discovery/approval failures, server fixture
  `TestPRRemediationWorkspaceReadBindsOriginalCandidateAndExclusiveProof/matches`
  reported an operation timeout. Store tests passed. This is not a passing
  whole-service race run. Browser-focused race tests and vet from the original
  implementation remain separately recorded in `browser-implementation.md`.
- API client and frontend build inputs were explicitly generated. Consumed LFS
  assets were hydrated, `git lfs fsck` passed and generated repository-owned
  `dist` output was removed after validation. No frontend source changed in this
  repair; the preceding default 1,055-test frontend pass remains recorded at its
  own source revision.

## Limits

These are implementation, lint, compilation and controlled-fixture results.
The repaired Linux trait use was checked against the pinned source API; the local
Clippy run was on macOS and is not an executed Ubuntu/X11 rendering result.
Actual provider login, browser history/password behavior, live CEF shutdown/flush
races, Windows/X11 desktop behavior and signed release acceptance remain
unperformed. Native process shutdown, local purge and server acknowledgment are
independent observations; offline or removed client authority never implies
server-confirmed cleanup.
