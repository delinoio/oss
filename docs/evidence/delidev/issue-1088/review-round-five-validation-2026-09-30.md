# Review round five validation

Runtime revision: `bcca17d36de5fa25030d3dcd235bba8b47d8f33`.
Main integration parent: `aa10ea86a` incorporates main
`6c749670727b30679e722821846bc8dc00f5ac32`.

This pass preserves both Codex forks and session terminals, regenerates their
shared protocol bindings, and repairs the uncertain-close deletion finding.
The independent merge and review records contain the focused regression results.
The revoked-Worker cleanup-authority decision remains unresolved; no new pairing,
cross-device handoff or revoked credential exception was implemented.

## Passed checks

- `pnpm proto:check`: schema formatting/lint, main breaking comparison and
  generated freshness passed.
- `pnpm ci:contracts`: all 113 tests passed.
- `GOMAXPROCS=2 go vet -p 2 ./cmds/delidev-cli/...`: passed.
- API-client build and tests: five files, all 46 tests passed.
- Desktop TypeScript checking passed. Fork and terminal focused UI checks
  passed (two files, nine tests).
- Repeated server uncertain-close regression passed three runs; related Worker
  ownership, replacement, acknowledgement and retirement checks passed.
- Desktop packaging dry-run checks passed (eight tests); launcher/asset checks
  passed (16 tests); widget fixtures and production frontend build passed.
- Required desktop icon LFS object was hydrated; asset preparation reported ready.
- Windows amd64 Worker tests cross-compiled using `GOMAXPROCS=2 GOOS=windows
  GOARCH=amd64 go test -p 2 -c -o /tmp/issue-1088-1824-worker-windows.test.exe
  ./cmds/delidev-cli/internal/worker`. Native Windows execution was not performed.

## Broad validation failures

Root `GOMAXPROCS=2 go test -race -p 2 -timeout=20m
./cmds/delidev-cli/...` did not pass. The command completed with exit status 1. Failed packages are CLI, harness,
Claude, Codex, Grok, server and workspace. CLI session creation/wait and harness discovery
or native handshake/delivery fixtures failed. Grok reached the unchanged
20-minute package watchdog while the original Plan uncertainty cases ran;
server reached it while invalid/oversized question-answer cases ran. A package
watchdog prevents completion of the entire package's test coverage.

The full storage (1001.549 seconds), terminal (1.930 seconds), user-service
(12.042 seconds) and Worker (421.551 seconds) packages passed. Other successful
packages include API proxy, connections, credentials, domain, forwarding,
native wire, OpenCode, GitHub integrations, presentation, process, providers
and security (cached). Workspace reached the unchanged 20-minute package
watchdog and failed (1200.900 seconds); full workspace coverage remains incomplete.

Required desktop `pnpm test` ran and failed in its unit phase: 20 failed files,
81 passed files; 61 failed and 1223 passed tests (1284 total). Failures included
fixed-deadline timeouts and assertions. A complete unit rerun using
`GOMAXPROCS=2 pnpm exec vitest run --maxWorkers=2` retained every test and its
original deadline, but still failed: 12 failed and 89 passed files, 24 failed
and 1260 passed tests. Its failures span App, Settings, subscriptions, devices,
desktop, backups, notifications, tray, sidebar, schedules and Activity.
Packaging, launcher/assets, widgets and build were then run independently
because the failing unit step prevents their execution in the standard script.

These failures remain visible. No controlled current-main comparison establishes
that they are baseline failures or caused only by concurrent load; neither the
focused passes nor earlier passing revisions establish complete current-head
validation. No test deadline, fixture assertion or source policy was weakened.

## Scope and retained limits

Generated app/API-client `dist` directories were removed after their consumers
finished. No Rust code changed. Historical validation and the frozen ledger
remain untouched. No native Windows/Linux, real remote Worker/account, native
desktop visual or release acceptance is established by this pass. Fresh pushed
head-specific CI and Codex review evidence remains necessary. The unresolved
revocation-authority review thread stays open pending the human decision.
