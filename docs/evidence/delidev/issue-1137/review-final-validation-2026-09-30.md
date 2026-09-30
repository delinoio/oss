# Issue #1137 final review repair validation — 2026-09-30

The first maintenance repair merges main and addresses the two Codex findings in
separate commits: service-aware advanced native Start and read-only live legacy
observation. No new RPC, protocol allocation, migration or preference is added.

- The complete frontend pipeline passes 1,268 tests in 98 files, type checking,
  package/launcher/assets, widget and production-build checks, as recorded in the
  first-maintenance file. The subsequent review fixes change Rust/Go infrastructure
  only; frontend source and tests are unchanged afterward. The production bundle
  is regenerated successfully for the final host compile.
- The rebuilt macOS arm64 sidecar and all native library fixtures pass 22/22 tests
  in 19.42 seconds, including the new advanced-Start service-ownership fixture and
  all five real-sidecar temporary-scope fixtures.
- `GOMAXPROCS=2 go test -race -p 1` with desktop launch, pairing and service-admission
  selections passes CLI in 41.638 seconds and user-service in 1.696 seconds.
  The focused legacy/Stop regressions also pass separately; final Go vet passes.
- The final production CEF host check passes in 17.86 seconds with prepared assets,
  rebuilt sidecar and regenerated frontend output.
- Required root Cargo is rerun for the Rust fix. It still exits 101 in unchanged
  binpm CLI tests, with 133 passing and the same five `/var` versus `/private/var`
  temporary-path failures (4.43 seconds for that suite). This is not a passing
  root workspace test result and does not establish later Cargo suites.
- Protocol/generation and structure/allocation/LFS checks passed after the main
  merge; neither review fix changes their source or tool-owned outputs.

All temporary runner changes are restored, generated sources have no drift and
repository-owned generated `dist` directories are removed before publication.
Earlier failing attempts remain preserved. These results do not extend the
recorded native acceptance boundary to rendered CEF, real service managers,
packaged installation, remote TLS or the remaining supported platforms.
