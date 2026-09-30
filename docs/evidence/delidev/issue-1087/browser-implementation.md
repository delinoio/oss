# Protected browser implementation evidence

## Scope and revision

Issue #1087, branch `kdy1/delidev-browser-1087`, based on main
`8a626cc8a6252f47c5d66f2e7b857cddf7354e89`. The verified implementation source is
`9bec03005ec823835c1d2f90d00f1a032f12cde2`. All fixtures use isolated temporary
state; no user credentials or real provider account was used.

## Observed implementation

Dedicated typed BrowserService RPC/CLI operations retain per-paired-device profile
ownership, exact request receipts and account cleanup counts. Device JSON is
extended additively without consuming main's enum or migration reservations;
schema remains 24. Session/Archive/panel closure preserve the shared profile.
The React side panel retains the session/composer. Native raw CEF children have
separate exact request-context paths, no Tauri IPC/scripts and guarded navigation.
Removal intents deny reopen; full directory deletion and exact acknowledgments
follow raw-child closure, poller join and the runtime's CEF shutdown return.

## Passed verification

Commands ran from the repository root unless their app directory is stated.
The final Go runs used task-owned Go 1.26.8 module/build caches, `GOMAXPROCS=2`,
and `TMPDIR=/private/tmp`, after concurrent work removed shared toolchain/cache
files during earlier attempts. Rust used the unchanged pinned CEF cache and
`RUSTC_WRAPPER=` after the shared wrapper disappeared.

- `go test -race -p 1 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli -run TestBrowser -count=1`: passed all five server scenarios and the CLI scenario. These exercise same-account sharing, account/server/device isolation, restart/current-state receipt replay, session/Archive retention, offline cleanup, exact and foreign acknowledgments, revoked/Worker/owner refusal, real Connect authentication/origin checks and concurrent registration.
- `go vet ./cmds/delidev-cli/...`: passed after the final input/ownership guards.
- `pnpm proto:check`: passed formatting, lint, baseline breaking comparison and fresh generation without drift.
- `node --test scripts/ci/delidev-proto.test.mjs scripts/ci/delidev-structure.test.mjs`: six passed, including immutable wire allocations and database reservation checks.
- `cargo test -p delidev-desktop --features desktop-host,custom-protocol browser`: four library policy/path/persistence tests and one controlled native cleanup test passed on macOS arm64 against CEF `150.0.0+150.0.10` and the unchanged Tauri revision.
- In `apps/delidev`, the affected Browser, account cleanup, Settings terminology and App/composer files passed 69 tests with `--maxWorkers=1 --testTimeout=20000`.
- In `apps/delidev`, the complete Vitest suite passed 1,038 tests in 90 files with one worker, a 20-second test timeout and a 10-second Testing Library observation timeout. The temporary verification config/setup files were removed afterward; production and committed test configuration were unchanged.
- TypeScript/client compilation, eight bundle fixtures, sixteen desktop launch/asset fixtures, the widget fixture and the production frontend build passed. Required LFS assets were hydrated before consumption. Both repository-owned generated `dist` directories were removed after verification.

The controlled native cleanup fixture retains an original removal intent, races a
fixture writer's final flush with shutdown, rejects deletion before poller-join
and raw-child-close proof, and then verifies complete directory/intent removal.
The production host compiles with the same lifetime guards; this fixture does not
execute a live CEF renderer.

## Broad-run failures

The required default `pnpm test` was executed in `apps/delidev`. The final default
run passed 1,037 of 1,038 tests; the unchanged Server preferences integration test
hit its one-second observation wait. An isolated default-wait retry also failed;
the complete serial run with the explicitly longer observation wait passed.
A merge regression in API entry deletion wording was corrected and its original
Settings test passed; no failing assertion was removed or weakened.

The full required `go test -race -p 1 -timeout 30m ./cmds/delidev-cli/...` run
produced failures outside the browser scenarios: `TestCLISessionAcceptanceQueueAndArchive`
received an unavailable workspace reader, Grok/OpenCode discovery fixtures could
not verify protocol/cleanup, and the Codex single-use approval `no-send` fixture
reported native acceptance requiring reconciliation. This was not a passing
whole-service race run. Browser-focused race tests and vet passed independently.

Root `cargo test` with canonical temporary paths and the pinned CEF cache failed
in the unchanged Clibox wait tests
`proxy_and_log_environment_cannot_expose_or_redirect_requests` and
`tls_dependency_errors_and_ca_overrides_are_isolated_and_redacted`: both observed
`dns_configuration` while expecting `tls_certificate`. The browser native checks
passed independently. These unrelated failures remain visible and were not
worked around by changing their assertions.

## Remaining acceptance limits

The UI's native adapter is controlled fixture code. Policy and persistence tests
are ordinary Rust fixtures. These do not prove a real renderer's shutdown/flush
race, actual web history/password behavior, provider login, Windows/X11 rendering,
platform installation or signed release acceptance. Real native/account/platform
acceptance remains separate from implemented source and automated evidence.

## First PR maintenance pass

PR #1201 merged main `2b658e05353a858e328f82a636d8bc49dbccb709` after publication. The only conflict
was appended CSS: both the Browser panel and GitHub profile scopes were retained.
The browser Go/native implementation remained unchanged. Default `pnpm test` in
`apps/delidev` passed all 1,055 tests in 90 files, type/client compilation, bundle
and launch/asset/widget fixtures and the production build. The six affected
Browser/cleanup/Settings/App/GitHub files also passed 95 tests with one worker.
The earlier default observation failures above are retained as historical
validation at the pinned pre-merge source, not as the result of this merged run.
Generated `dist` output was removed again. Whole-service Go and root Rust failures
remain distinct from the passing browser and merged frontend evidence.
