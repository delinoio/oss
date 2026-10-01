# PR #1232 callback and resource review validation

This 2026-10-01 pass evaluates Codex's review of `81ab67188c0d` without
overlapping another repair. Independent commits retain initial native geometry
failure, remove device browser inventory from generic responses, and add a
regression proving existing managed restore retains current browser obligations.
The restore finding's historical-Device premise is disproved; production restore
behavior remains unchanged. Original native evidence and historical ledgers remain
intact.

## Focused results

- Pinned CEF browser-host tests: 27 passed in 13.63 seconds, including failure
  retention before asynchronous closure, visibility after its exact close
  callback, control rejection and stale callback isolation after Retry.
- Go server race tests matching `TestBrowser` plus the Worker pairing/ownership
  fixture: passed in 11.988 seconds. New binary/JSON Connect checks cover all
  generic device read paths, owner and paired clients, own/foreign identities,
  pending/removed inventory, unchanged stored bytes and revocation projection.
- The managed-restore browser race regression passed in 6.066 seconds. An older
  active-profile backup cannot erase offline/revoked pending removal or completed
  cleanup. Original profile revisions/deletion identities survive replacement and
  another restart; revoked reads and deleted-account registration remain denied.
  The selected backup stays byte-for-byte unchanged.

## Complete required checks and related checks

- Git LFS integrity passed. Required generated API client, frontend/assets,
  DevHud administrator and async-commit-hook embedded outputs were built before
  consumers ran.
- Complete pinned CEF native tests passed: 20 library and 34 host tests, 54 total;
  four real-sidecar fixtures remain explicitly ignored. Strict all-target,
  all-feature Clippy with `-D warnings` passed. The Tauri/CEF pin is unchanged.
- Complete frontend `pnpm test` passed typecheck but returned exit 1 at Vitest:
  1,185 passed and 109 failed across 103 files (23 failed, 80 passed). Many
  failures were existing test-deadline errors.
- One complete Vitest recheck with `--maxWorkers=1` and unchanged deadlines
  returned exit 1: 1,289 passed, five failed across 103 files (two failed,
  101 passed), in 579.58 seconds. All browser tests passed. Four unchanged App
  tests concerning Settings-opening disposal/drafts/notifications and one
  unchanged Settings picker test reached their five-second limits. No tests,
  deadlines or production behavior were weakened to turn this into a pass.
- The remaining frontend stages were run independently after that failure:
  eight packaging checks, 16 launch/assets checks, widget fixtures and production
  build all passed. These results do not make the original `pnpm test` successful.
- All 46 generated API-client tests passed. Protocol lint/breaking/freshness,
  six protocol/structure fixtures and complete DeliDev Go vet passed.
- Unfiltered root `cargo test -- --test-threads=1` returned exit 101 in the
  unchanged Clibox `run` group: 37 passed and one failed in 36.06 seconds.
  `managed_service_forwards_shutdown_output_before_readiness_timeout` returned
  status 1 (`IoFailed`) instead of expected 124 while retaining its shutdown
  output. Later workspace groups were not reached. No root test filter or
  deadline change was used.
- Complete `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/...`
  returned exit 1: 17 packages passed (16 cached) and five failed. CLI failed
  `TestCLISessionAcceptanceQueueAndArchive` at its creation diff because the
  fixture Worker file reader was unavailable (package 245.787 seconds).
  Grok (601.348 seconds), server (603.564), store (601.488) and workspace
  (600.859) reached the unchanged default ten-minute package limits. Grok also
  reported initialization/uncertain-delivery fixture failures before that limit.
  Connections passed freshly in 25.208 seconds.
- At those package limits, the running tests had elapsed 89 seconds for Grok's
  owned-input case, one second for server native-loss handling, less than one
  second for store title-summary handling, and 76 seconds for workspace PR
  preparation. A package limit does not prove an individual test deadlock.
  No executable from this run's unique Go build directory remained after the
  command settled; unrelated older test processes were preserved.

During the full checks, observed one-minute host load was 273.88 on 16 logical
CPUs. Contention may contribute to deadline-sensitive failures; that observation
does not establish their complete root cause. Unrelated processes were preserved.

## GitHub and evidence limits

The reviewed `81ab67188c0d` head had 35 passing checks and 12 skips, including
the CI aggregate and Windows harness. The final push invalidates that evidence
for the replacement head; fresh CI and Codex review are required.

Actual native host geometry failure, real provider login/history/password behavior,
real CEF shutdown/flush, Windows/X11 native controls, installed distributions and
release acceptance remain unperformed. Controlled callback tests and temporary
SQLite fixtures establish their recorded invariants only. No real credentials,
native cache contents or external browsing data were used.

Generated repository-owned `dist` outputs were removed after all consuming
checks settled. Source/generated protocol files, credentials, environment
material, shared target caches and historical evidence were preserved.
