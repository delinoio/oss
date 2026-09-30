# PR #1232: browser lifetime review validation

## Scope

This repair starts from `b0e3753e9a337ae38b0fb9a56e82c017b98db946` and addresses four current Codex threads independently:

- `PRRT_kwDORRAKg86nrB6H`: independent forgotten/account cleanup budgets (`006267c44`).
- `PRRT_kwDORRAKg86nrB6N`: native invisibility or exact close proof before Hide acceptance (`debf00787`).
- `PRRT_kwDORRAKg86nrB6V`: drain accepted address updates during graceful exit (`f2c500126`).
- `PRRT_kwDORRAKg86nrB6Z`: native window teardown invalidates and closes its owned presentation (`d576e6037`).

Each repair has separate controlled regression evidence in this directory. Local CEF 150.0.10 headers confirm that forced browser close can complete asynchronously; native invisibility is established separately from that request. Browser contracts and native instructions are updated together; historical evidence and the ledger remain unchanged. No protocol number, migration, CEF pin, issue, branch, PR or automation is introduced.

## Passing checks

- Git LFS integrity, DeliDev asset preparation and required generated client/frontend builds; DevHud administrator and async-commit-hook embedded asset builds.
- Final pinned-CEF `cargo test -p delidev-desktop --features desktop-host,custom-protocol -- --test-threads=1`: 20 library and 30 host tests passed; four opt-in real-sidecar fixtures explicitly ignored.
- `cargo clippy -p delidev-desktop --all-targets --all-features -- -D warnings` passed. Native validation uses the existing CEF cache and shared target, two build jobs and no compiler wrapper.
- `pnpm proto:check` passed full lint, breaking and generated freshness checks; all six DeliDev protocol/structure checks passed.
- Frontend typecheck passed within required `pnpm test`. The focused one-worker browser/cleanup selection passed all 13 tests; API-client `pnpm test` passed all 46 tests across five files.
- Complete DeliDev Go vet passed. Focused Go race browser validation passed on server (13.210 seconds); store compiled with no matching test names.
- Packaging dry-run passed eight checks, desktop launch/assets passed 16, widget fixtures passed, and production frontend build passed independently of the aggregate frontend failure.

## Failures and limits

Required frontend `pnpm test` returned exit 1: **102 failed, 1,190 passed, 24 failed/78 passed files**, 228.68 seconds. The failure inventory includes App, Activity/sidebar, Agent configuration, backup/device/Doctor/desktop, notification/tray, PR/schedule, subscription and Settings/integration presentation. Browser/cleanup/fork files are absent from the printed failure inventory. Sixty-five printed groups report five-second test deadlines. The aggregate stops before its packaging/build suffix, whose stages were executed separately and passed. These failures remain unresolved; no complete frontend pass is claimed.

Root Cargo ran from the repository root with `cargo test -- --skip managed_service_does_not_start_after_the_preflight_exhausts_its_deadline` and returned exit 101 in unchanged Clibox `tests/run.rs`: **33 passed, four failed, one filtered**. The caller-marker, nested npm launcher and nested launcher-chain cases could not read their expected fixture markers; the owned-descendant timeout case parsed an empty PID. The previously sampled unbounded managed-service fixture remains explicitly excluded, as documented in earlier publication evidence. Later workspace groups were not executed after the failure; no complete unfiltered root pass is claimed.

Complete `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/...` returned exit 1: **15 packages passed, seven failed**. Worker passed in 315.343 seconds. No Go source changed in this repair.

| Failed package | Observation |
| --- | --- |
| CLI (347.560 s) | Repository inspection timed out and its Worker did not stop; the session fixture's repository file read returned owning-Worker unavailability. |
| Codex (601.660 s) | Permission/approval/interaction evidence cases failed; package deadline expired during changed-native-scope inspection (21 s, duplicate subcase 4 s). |
| Grok (601.515 s) | API initialization and original closure evidence failed; package deadline expired during owned-input/lost-response claims (1 m 20 s, subcase 10 s). |
| OpenCode (103.214 s) | Aggregate native-read byte-budget case retained uncertain mutation completion. |
| Server (602.481 s) | Package deadline expired during Claude callback-settlement evidence (17 s, subcase 1 s). |
| Store (601.355 s) | Package deadline expired during PR retained-quota collection rollback (1 m 16 s). |
| Workspace (600.880 s) | Package deadline expired during PR-workspace mismatch/unknown-access matching (13 s). |

A package deadline does not establish that its last active case hangs. While workspace remained in progress, a bounded native sample showed ongoing child-process and filesystem activity; elapsed wall time alone was not treated as hang proof. These failures remain unresolved, and no complete Go race pass is claimed.

The inspected old-head Windows harness job and dependent CI Result fail; see `pr1232-windows-harness-blocker.md`. Its workspace-read cause has not been established sufficiently for a safe production repair. Local browser checks do not erase that CI failure. Fresh CI and Codex review are required for the final pushed head.

Fixtures use temporary state and controlled native adapters. Live provider login/history/password behavior, actual CEF shutdown/flush, Windows/X11 rendering and native controls, installed packages and release acceptance remain unperformed.

Repository-owned generated `dist` directories were removed after validation: DeliDev and async-commit-hook frontend outputs, the DeliDev/DevHud/async-commit-hook clients, and both required Go embedded asset outputs. Dependency/build caches and private user configuration were preserved.
