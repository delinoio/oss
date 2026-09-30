# PR #1232 after-created failure retention

Codex thread `PRRT_kwDORRAKg86nv4sx` identified an initial native host/geometry
failure after the current raw child was accepted. The callback now retains that
typed result under the existing profile/generation/reservation check before
requesting closure. The independent close callback releases the handle without
erasing failure, so polling and ordinary controls remain in the explicit Retry
state. A stale callback cannot poison the replacement presentation.

Validation on 2026-10-01:

- Git LFS integrity passed; generated API client and DeliDev frontend/assets were
  built before the native test.
- Pinned CEF `cargo test -p delidev-desktop --features
  desktop-host,custom-protocol browser_host::tests -- --test-threads=1` passed
  all 27 tests in 13.63 seconds.
- The new controlled callback test checks failure visibility before closure,
  retention after the close callback, control rejection and stale callback
  isolation after explicit replacement. It does not induce a real platform
  geometry failure or attest to real-renderer shutdown.

Full required checks are recorded separately after the remaining independent
review repairs. Historical evidence remains unchanged.
