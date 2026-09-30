# Issue #1137 desktop launch evidence

## Source and scope

Recorded on 2026-09-30 from base
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7`. This record covers the
fresh-main-host launch default, Go-owned service/pairing arbitration, product
startup/sidebar/tray wording and retained advanced connection controls. The
original issue #964 requirements snapshot and historical evidence remain intact.
The owning contracts are the [desktop contract](../../../apps-delidev-desktop-contract.md),
[CLI contract](../../../cmds-delidev-contract.md) and
[native-service contract](../../../cmds-delidev-user-services-contract.md).

The implementation has no public RPC, dependency, migration, persisted startup
preference, automatic recovery or Worker/account/harness execution addition.
Temporary fixtures use only their original private scopes and identities.
Credentials, raw child output, user configuration and generated artifacts are
excluded from this record.

## Passing regression evidence

| Command / check | Result and boundary |
| --- | --- |
| `pnpm install --frozen-lockfile` at root; `git lfs pull` | Dependencies/hooks installed and required LFS source assets hydrated in the isolated checkout. |
| `pnpm --filter @delinoio/delidev-api-client build`; `pnpm typecheck` in `apps/delidev` | Generated client and final UI types pass. |
| `VITEST_MAX_WORKERS=1 pnpm exec vitest run src/App.test.tsx src/desktop.test.tsx src/local-registration.test.tsx src/local-server.test.tsx src/settings.test.tsx src/settings-devices.integration.test.tsx src/settings-pricing.integration.test.tsx --testTimeout=15000` | 83 tests pass across seven files, including all four cases that failed in the earlier full run. The explicit timeout is a test-run option for the busy host, not a production deadline change. |
| Final `src/desktop.test.tsx` rerun with the same worker/timeout options | Nine tests pass, including the added diagnostics hiding/reopening regression. The original confirmation survives Settings disposal and the identical uncertain Stop request is retried. |
| `go test -race ./cmds/delidev-cli/internal/cli ./cmds/delidev-cli/internal/userservice -run 'TestDesktopLaunch\|TestDesktopPairing\|TestLaunchAdmission\|TestCLILocalClientPairing\|TestDesktopRecovery\|TestAutomaticStartup\|TestStartupCannot\|TestDetachedStartupReadiness\|TestManagedIntent' -count=1` | Both packages pass. Covers concurrent startup, same-process Stop suppression, fresh launch after joined cleanup, malformed registration, incomplete database cleanup, service install/start/remove exclusion, original pairing/recovery evidence and prior startup behavior. |
| `go vet -p 2 ./cmds/delidev-cli/...` | Passes after the final Go changes. |
| `cargo test -j 2 -p delidev-desktop --lib -- --test-threads=1` | 16 pass, five explicit real-sidecar fixtures remain opt-in. Synthetic host tests prove one initial bootstrap/pairing, concurrent observation, Stop invalidation and retained missing-sidecar failure. |
| `pnpm test:bundle-dry-run`; `pnpm test:desktop-launch`; `pnpm test:widget`; `pnpm build` in `apps/delidev` | Eight packaging tests, 16 launcher/asset tests, widget fixtures and frontend build pass. |
| `pnpm prepare:assets`; `pnpm prepare:sidecar`; `pnpm prepare:widget` | Hydrated icon verified, bundled Go sidecar generated and macOS arm64 widget extension builds successfully with signing credentials excluded. |

## Actual temporary-scope macOS arm64 host checks

A Go sidecar was built from this change with
`go build -o <temporary-binary> ./cmds/delidev-cli`. With that executable selected
through `DELIDEV_TEST_SIDECAR`, the explicit command

```sh
cargo test -j 2 -p delidev-desktop \
  real_fresh_hosts_share_launch_identity_and_preserve_stop_and_detached_lifetime \
  -- --ignored --nocapture
```

passes against two actual `Supervision`/`Connector` host owners and an actual
detached Go server. The isolated scope starts with no database/client/listener;
both fresh owners authenticate without a renderer click, share the same server,
client and token, and repeated observations retain that identity. Joining one
host leaves the server available. Owner Stop makes the remaining host's launch
observation and Retry return Stopped. After host shutdown and original server
cleanup, a fresh host reopens the same server identity; joining that host again
leaves the detached server available. The fixture finally stops its owned server.

This is native infrastructure/sidecar evidence on macOS arm64, not a rendered
CEF-window or packaged installer acceptance claim. Component tests separately
prove automatic product entry, Strict Mode/remount observation, generic failure,
explicit Retry, stopped-state suppression and connect-only saved-window behavior.

The first real two-host attempts failed because the second host retained a
pairing-lock Conflict as Busy; extending the aggregate fixture deadline did not
resolve it. The final implementation adds bounded joining of the original fixed
desktop-client recovery lock for native pairing and inspection. It revalidates
the original evidence after acquisition and does not retry an uncertain pairing
as a new request. The final actual fixture passes in 3.91 seconds.

## Required broad checks and unresolved results

- Required `VITEST_MAX_WORKERS=1 pnpm test` in `apps/delidev` was run. The first full
  run passed typecheck and 959 of 963 tests, failing three unchanged Settings
  lifetime cases at their five-second test deadline and the pricing integration
  confirmation wait. All four pass in the seven-file bounded rerun above. The
  required full pipeline was rerun after adding the final retention test; its
  outcome will be recorded separately before delivery.
- Root `cargo test -j 2 -- --test-threads=1` was run with the existing native CEF
  cache and target directory. The first run failed in unchanged clibox
  `timeout_terminates_owned_descendants` with an empty PID parse. The second run
  progressed past that area and failed in unchanged clibox wait tests
  `proxy_and_log_environment_cannot_expose_or_redirect_requests` and
  `tls_dependency_errors_and_ca_overrides_are_isolated_and_redacted`. Neither run
  establishes a passing whole-workspace Rust suite; focused DeliDev tests pass.
- `go test -race -p 2 -timeout 20m ./cmds/delidev-cli/...` was run. It encountered
  timeouts in unchanged `TestCLISessionAcceptanceQueueAndArchive` and
  `TestDiscoveryVerifiesOpenCodeWithoutExecution`, then missing shared Go-cache
  import artifacts in later packages. It does not establish a passing broad Go
  suite. Focused changed-area race tests and vet pass.
- `DELIDEV_NATIVE_USER_SERVICE_TEST=1 go test -race
  ./cmds/delidev-cli/internal/cli -run '^TestNativeUserServiceLifecycle$' -count=1 -v`
  verified an actual uniquely named installed/stopped macOS registration remained
  byte-identical with unchanged lifecycle intent after desktop launch returned
  service-managed. Its subsequent explicit native service Start failed existing
  ownership/cleanup verification with RecoveryRequired; cleanup through that
  manager was also unconfirmed. The fixture's ordinary owner Stop succeeded,
  preserving stopped intent rather than forcing removal of uncertain native
  state. Live-service reuse and complete native service lifecycle acceptance
  remain unproved by this run. Fake-backend admission tests separately prove
  install/start/remove cannot cross the held original service-control lock.
- An initial CEF host compile check failed before application compilation because
  the sidecar resource had not been generated and the shared CEF extraction was
  missing wrapper/header files. Required resources were then prepared and the
  compile check rerun; its final result will be recorded before delivery.

The busy shared machine and shared-cache failures are observed constraints, not
proof that every broad failure is environmental. No unrelated tests or native
ownership rules were weakened to obtain a passing result.

## Acceptance limits

Actual CEF UI startup/Stop/remount/show/reopen/Quit, keyboard Escape/focus
containment/restoration and packaged first-launch acceptance remain unperformed.
The fixed product listener was already occupied by an existing user process;
this work does not stop or replace it. macOS x64, Windows x64/arm64 and Ubuntu
X11 x64/arm64 actual checks are unperformed. No account, provider, harness,
Worker execution or session-resume acceptance is inferred from these fixtures.
Generated repository-owned `dist` output must be removed after validation and
before the final worktree is delivered.
