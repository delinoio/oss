# Replacement browser native and client checks

Recorded on 2026-09-30 for issue #1087 at implementation revision
`dbd18fdf1c9940c74ed1219435cb7027da9f8246`, incorporating main
`574c1a92c957fc741a723ff8123888dad32a2194`. Earlier issue-1087 records remain
historical evidence at their own revisions. These checks do not establish the
status of a later PR head.

## Reconciled behavior

- Preserve main's implemented native accounting migration 25 and shared native
  compaction reservations. Browser metadata introduces no migration or shared
  enum/field allocation. Regenerate bindings from reconciled canonical sources.
- Subscription deletion integration now observes accepted deletion and separate
  cleanup counts before explicitly returning to accounts. The retained empty-list
  and Settings-lifetime assertions remain; its focused three-test run passed.
- Inspect the unchanged pinned Tauri renderer handler in
  `crates/tauri-runtime-cef/src/cef_impl/ipc.rs` at
  `4af26a3f7f8b692d62cca549bbacd93f5ce90b41`. It installs a shared JavaScript
  message stub. The raw external browser client explicitly rejects every process
  message and has no Tauri browser-side IPC handler or initialization scripts.
  The contract and scoped instructions distinguish the inert renderer stub from
  native/product authority. No dependency patch or pin change is introduced.

## Completed verification

The native commands used the existing CEF 150.0.10 cache, Cargo's locked shared
target cache, two build jobs and no compiler wrapper. Fixtures use temporary
owned state, never user credentials or inference.

- `cargo clippy -p delidev-desktop --all-targets --all-features -- -D warnings`
  passed, including the explicit process-message rejection.
- `cargo test -p delidev-desktop --features desktop-host,custom-protocol` passed:
  20 library tests and 12 desktop-host tests. Four pre-existing sidecar integration
  tests remain explicitly ignored; they are not counted as passing tests.
- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/cli -run 'TestBrowser|TestMigration' -count=1`
  passed all three packages against reconciled schema 25.
- `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...` passed at the initial browser
  boundary. A later broad-suite result must be reported separately.
- `pnpm proto:check` passed lint, compatibility against main's captured revision,
  and generated-binding freshness. The six focused protocol/structure tests also
  passed.
- API client `pnpm test` passed all 44 tests, including its isolated server
  integration, after preparing the fixture's untrimmed Go build cache.
- Asset hydration and preparation, client/frontend typechecking and production
  frontend build passed. Final sidecar preparation passed.
- `pnpm test:bundle-dry-run` passed eight tests; `pnpm test:desktop-launch` passed
  sixteen tests; `pnpm test:widget` passed its exact-value, currency, isolation,
  stale/closure, masking, corruption and private-storage fixtures.
- Required root `cargo test` after the explicit message-rejection change failed
  in unchanged `clibox` test
  `first_cancellation_honors_the_configured_cleanup_grace`: 37 tests passed and
  one failed in that test binary. An earlier root run failed five unchanged
  `clibox-fspy` macOS process-supervision fixtures. Neither is a passing workspace
  Rust result; no unrelated assertions were changed.

## Full-suite and acceptance qualifications

The ordered default frontend rerun completed with 1,235 passing tests, five
five-second test-deadline failures and twelve skipped integration tests across
98 files. Fourteen files failed, including integration setup exceeding its
two-minute Go build deadline. This is not a passing `pnpm test` run. Packaged
sidecar preparation uses `-trimpath`, while integration fixtures use an untrimmed
build; prepare that exact fixture configuration before retrying. Serial retries
use temporary, uncommitted configuration and unchanged assertions. Their final
result and the complete broad Go race result belong in a separate record.

One intermediate frontend retry overlapped generated-source replacement with
imports, so its module-read failures are not implementation verification.
The initial broad Go run was stopped after failures when main reconciliation
invalidated its remaining inputs. A fresh broad run uses one stable source
revision; pending results are not reported as passed.

Compilation and controlled fixtures do not establish live provider login,
history/password behavior, actual CEF renderer shutdown/flush races, Windows/X11
rendering, installed packages or signed release acceptance. Offline or uncertain
cleanup remains pending until original native cleanup and server acknowledgment
are independently established.
