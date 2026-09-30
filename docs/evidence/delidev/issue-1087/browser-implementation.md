# Protected browser implementation evidence

## Scope and revision

Issue #1087, branch `kdy1/delidev-browser-1087`, based on main
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7`. All fixtures use isolated temporary
state; no user credentials or real provider account was used.

## Observed implementation

Dedicated typed BrowserService RPC/CLI operations retain per-paired-device profile
ownership, exact request receipts and account cleanup counts. Device JSON is
extended additively without consuming main's enum or migration reservations.
The React side panel retains the session/composer. Native raw CEF children have
separate exact request-context paths, no Tauri IPC/scripts and guarded navigation.
Removal intents deny reopen; full directory deletion and exact acknowledgments
follow raw-child closure, poller join and the runtime's CEF shutdown return.

## Validation

Validation results are being recorded after final runs. Early browser-focused Go
race tests passed before the reservation-safe storage adaptation; subsequent
focused runs verify the final model. The native host compiled against the pinned
CEF before the last cleanup refinements. An initial broad Go run hit existing
harness fixture timeouts under concurrent machine load. An initial UI run had
existing five-second timeouts; final validation distinguishes those from browser
regressions.

## Remaining acceptance limits

The UI's native adapter is controlled fixture code. Policy and persistence tests
are ordinary Rust fixtures. These do not prove a real renderer's shutdown/flush
race, actual web history/password behavior, provider login, Windows/X11 rendering,
platform installation or signed release acceptance. Real native/account/platform
acceptance remains separate from implemented source and automated evidence.
