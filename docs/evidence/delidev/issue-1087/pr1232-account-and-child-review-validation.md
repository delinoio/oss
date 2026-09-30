# PR #1232 account selection and child replacement validation

This 2026-10-01 repair follows Codex's review of
`e463342d457c49c1a3891e9ebfe759fff5cee3d4`. Commit `744cb074e` fixes pending
continuation-account selection at the desktop and registration transaction;
`ebbc60fd0` fixes superseded native-child invisibility and closing-handle retention.
Each finding has its own focused tests, scoped instructions and evidence:

- [Pending account selection](pr1232-pending-account-selection.md)
- [Superseded child invisibility](pr1232-superseded-child-invisibility.md)

## Executed checks

Commands run from the root unless the directory is stated. Native Rust commands
use the unchanged CEF pin, `CEF_PATH=/Users/kdy1/Library/Caches/tauri-cef`,
`CARGO_TARGET_DIR=/Users/kdy1/projects/oss/target`, `RUSTC_WRAPPER=`,
`CARGO_BUILD_JOBS=2` and `TMPDIR=/private/tmp`.

| Check | Result |
| --- | --- |
| `git lfs fsck`; DeliDev asset preparation; generated API-client and embedded DevHud administrator/ACH builds | Passed |
| Native `cargo test -p delidev-desktop --features desktop-host,custom-protocol -- --test-threads=1` | Passed: 20 library + 33 host tests; four explicitly ignored real-sidecar fixtures |
| `cargo clippy -p delidev-desktop --all-targets --all-features -- -D warnings` | Passed |
| `pnpm test` in `apps/delidev` | Passed: 1,294 tests in 103 files, typecheck, eight packaging tests, 16 launch/assets tests, widget checks and production build |
| Focused browser/selection Vitest run with one worker | Passed: 13 tests in two files |
| `pnpm --filter @delinoio/delidev-api-client test` | Passed: 46 tests in five files |
| `pnpm proto:check` | Passed: lint, breaking comparison and fresh generated bindings without drift |
| DeliDev protocol/structure Node checks | Passed: six tests |
| `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...` | Passed |
| Focused browser server race tests | Passed, 4.106 seconds; includes the real stopped-account switch before Resume |
| `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/...` | Exit 1: 18 packages passed (14 cached), four failed at their default ten-minute package limits |
| Root `cargo test -- --test-threads=1` after refreshing the Clibox test build | Exit 101: Clibox `tests/run.rs` had 32 passes and six failures; later workspace groups were not reached. No command-level test filter was used |

## Broad Go limitations

Package expiration is not proof that the currently running individual test hung.
The complete command settled without manually terminating its processes:

| Package | Result and observations at package expiration |
| --- | --- |
| Grok | 600.950 seconds; `TestOriginalPlanControllerPreservesUncertainty` (71 seconds), `planning-missing-resolution` (15 seconds) |
| Server | 601.222 seconds; `TestOpenCodeProposalRejectsChangedOwnersAndDuplicateNativeEvents` (15 seconds), `namespace` (two seconds) |
| Store | 601.530 seconds; `TestBackupRestoreRejectsChangedInspectionRevisionAndOwnership` (three seconds), `active` (one second) |
| Workspace | 600.726 seconds; `TestForkPreparationRejectsAllRepositoriesBeforeCreatingChildScope` (63 seconds), `local/duplicate-repository` (three seconds). Eight earlier continuation/ownership/diff/closed-inspection/fork-deletion tests also failed |

The CLI (163.164 seconds), connections (9.836 seconds), Codex (79.971 seconds)
and OpenCode (29.292 seconds) passed uncached. The remaining 14 successful packages,
including Worker, reused valid Go test results. The causes of these broader
failures remain unresolved; deadlines and acceptance coverage were preserved.
No test executable remained from this command's unique Go build directory after
completion. Unrelated processes were preserved.

## Root Rust limitations

The first unfiltered root run reported two Clibox npm-launcher failures at source
path canonicalization. The shared cached integration-test binary embedded
`/Users/kdy1/.codex/worktrees/22a4/oss/crates/clibox`, while the launcher existed in
this checkout. Its compiled strings confirmed that stale fixture root. Refreshing
the unchanged `crates/clibox/tests/run.rs` timestamp rebuilt the fixture for this
worktree without changing its source. Both npm-launcher cases then passed.

The fresh unfiltered run still failed these six unchanged Clibox cases:

- `completed_workload_cleans_up_its_background_descendants`: unexpected timeout
  status with retained workload output.
- `nested_shell_wrapper_owns_its_descendants`: missing fixture PID file.
- `service_probe_ignores_custom_ca_override_variables`: exit 124 instead of 1.
- `timeout_forwards_shutdown_output_before_returning`: missing shutdown output.
- `timeout_terminates_owned_descendants`: missing fixture PID file.
- `wrappers_preserve_a_leading_literal_workload_separator`: unexpected timeout
  status with empty output.

That group completed in 38.53 seconds. The previously unbounded managed-service
preflight fixture passed in both runs. No complete root Cargo pass is claimed.

## CI, cleanup and acceptance

Before this repair's push, the reviewed predecessor `e463342d4` had 35 successful
checks and 12 skips, including the previously failing Windows harness and CI
aggregate in [run 36777779128](https://github.com/delinoio/oss/actions/runs/36777779128).
This does not establish the cause of its earlier Windows failure or validation of
the new repair head; fresh CI and Codex review remain required after pushing.

Generated repository-owned `dist` output was removed after checks, and the
historical evidence and ledger remain intact. Real provider login/history/password
behavior, actual native renderer shutdown/flush, Windows/X11 controls, installed
packages and release acceptance remain unperformed. Controlled native adapters and
component fixtures do not substitute for those observations.
