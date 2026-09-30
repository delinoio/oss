# PR #1232 native browser review validation

Source revision: `c4e4ae0de4737d5ccca51f4784c20f0e6e893d32`, branch
`kdy1/delidev-browser-1087-replacement`, 2026-09-30. Separate repairs:

- `0295230e6`: release superseded native presentations before retry authorization;
  see [reservation evidence](pr1232-reservation-repair.md).
- `362fa0e83`: persist fair removal progress across exits;
  see [cursor evidence](pr1232-cleanup-cursor-repair.md).
- `47ff29307`: retain native exit through final worker discovery;
  see [discovery evidence](pr1232-final-discovery-repair.md).
- `c4e4ae0de`: use enumeration for the cursor quota after strict Clippy rejected
  the explicit loop counter. This preserves the quota and rotation behavior.

Merge `a0413ba9e` incorporates main
`98df29c41ddf3c8b1274c51fe8f406b6dae6ca74`. Before that merge, protocol breaking
validation rejected missing `SwitchSessionAccount` request/response/RPC and
System capability 5 from the newly advanced main baseline. The merge was clean,
preserves main's allocations and generated bindings, and leaves the native Rust
sources unchanged. Protocol lint, breaking and generated freshness then passed.

## Passing checks

Native commands use `CEF_PATH=/Users/kdy1/Library/Caches/tauri-cef`,
`CARGO_TARGET_DIR=/Users/kdy1/projects/oss/target`, an empty `RUSTC_WRAPPER`,
`CARGO_BUILD_JOBS=2`, and `TMPDIR=/private/tmp`. CEF and Tauri remain at the
existing immutable versions.

- `cargo test -p delidev-desktop --features desktop-host,custom-protocol`:
  20 library tests and 16 host tests passed at the discovery repair. Four
  explicitly qualified real-sidecar tests remain ignored. Main changed no Rust
  source. After the final counter correction, the 65-profile restart/rotation
  regression passed again in 13.41 seconds.
- `cargo clippy -p delidev-desktop --all-targets --all-features -- -D warnings`:
  passed after the counter correction.
- `pnpm proto:check`: complete lint, breaking and generated freshness passed
  after main reconciliation.
- `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...`: passed before and after merge.
- API-client `pnpm build`, `pnpm lint`, and
  `GOMAXPROCS=2 GOFLAGS=-p=1 pnpm test`: passed; all 44 tests in four files.
- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/store -run Browser -count=1`:
  five server browser tests passed in 7.562 seconds. The store package compiled
  and had no matching tests; its browser storage is exercised through the server.
- `pnpm exec vitest run src/session-browser.test.tsx src/configuration-browser-cleanup.test.tsx --maxWorkers=1`:
  all nine tests in two files passed.
- Frontend `pnpm typecheck`, `pnpm test:bundle-dry-run`,
  `pnpm test:desktop-launch`, `pnpm test:widget`, and `pnpm build`: passed.
- `node --test scripts/ci/delidev-proto.test.mjs scripts/ci/delidev-structure.test.mjs`:
  all six tests passed.
- Git LFS integrity, explicit asset preparation, API-client/frontend builds,
  and the administrator/async-commit-hook embedded builds passed. Generated
  repository-owned `dist` directories were removed after validation.

## Broad local suite limits

No passing full repository suite is claimed. Preserve the preceding qualified
records; the following checks were also executed during this repair.

Frontend `pnpm test` before merge failed: 63 failures and 1,210 passes across
100 files, with many five-second deadlines. After merge, the same unit inventory
with `--maxWorkers=2` had 1,283 passes and one five-second deadline in
`App.test.tsx` (abandoning an uncertain New Project save). A further complete run
with two workers and a 15-second default deadline had 1,269 passes and 15 failures
across 101 files. Its failing files were `agent-configuration.test.tsx`,
`settings-lifetime.test.tsx`, and the Claude, configuration, GitHub, preferences
and subscriptions Settings integration files. These files were not changed by
the browser repairs. Longer deadlines did not produce a clean full run; the
cause of the varying failures remains unconfirmed. Bundle, launch, widget and
build stages were executed separately because failed unit runs stop `pnpm test`
before those stages.

Both complete `GOMAXPROCS=2 go test -race -p 4 ./cmds/delidev-cli/...` runs
finished with exit 1, before and after merge. The first failed CLI, Codex, Grok,
server and workspace packages; the store and Worker packages passed from cache.
The reconciled run passed API proxy, connections, credentials, domain,
forwarding, harness core, Claude, native wire, OpenCode, GitHub integrations,
presentation, process, providers, security, user services and Worker. Failures:

- CLI `TestCLISessionAcceptanceQueueAndArchive` (package 209.562 seconds).
- Codex native approval uncertainty and definite steer rejection assertions,
  followed by the ten-minute package deadline while changed-settings/reviewer
  recovery was active (602.358 seconds).
- Grok original file-reply assertions, followed by the ten-minute deadline while
  native lifetime-loss handling was active (602.195 seconds).
- Server deadline while Claude interruption preservation was active
  (601.726 seconds).
- Store deadline while `TestPRProblemRetainedQuotaRollsBackWholeCollection` was
  active (601.212 seconds).
- Workspace creation/live/literal/binary/unborn comparison assertions, followed
  by the deadline while first-execution remote rechecking was active
  (601.063 seconds).

Deadline snapshots identify active cases, not independently proved individual
hangs or their root causes. These broader failures are not browser acceptance.

Root `cargo test` was executed from the repository root. An initial launch
without the CEF cache selector was stopped and restarted with the existing cache.
The corrected run reached unchanged Clibox `tests/run.rs`: 31 tests passed, six
process-cleanup/timeout tests failed, and one fixture did not finish. The failed
tests cover caller-marker cleanup, nested npm launcher descendants (single and
chain), nested shell ownership, forwarded shutdown output and owned descendants.
After roughly six minutes, a native process sample showed
`managed_service_does_not_start_after_the_preflight_exhausts_its_deadline` waiting
in `pthread_join` for its fixture thread blocked in `TcpListener::accept` at
`crates/clibox/tests/run.rs:2340`. Only this owned test process was terminated;
root Cargo returned exit 101 with SIGTERM, so later workspace tests did not run.
No Clibox source was changed and no completed root suite is claimed.

These controlled fixtures, compilation and build checks do not establish real
renderer shutdown/flush, provider login, supported-platform, native package or
release acceptance. Those outstanding limits remain in the prior browser
records. The preceding pushed head's CI success does not verify this repaired
head; fresh CI and Codex review must be assessed on later maintenance runs.

## Final review snapshot

The final one-shot repair inventory had no failing checks on the preceding
pushed head, but included five new findings posted during this repair. They
remain unresolved for the next scheduled maintenance pass: [tab-control atomic
publication](https://github.com/delinoio/oss/pull/1232#discussion_r4145686254),
[address callback publication](https://github.com/delinoio/oss/pull/1232#discussion_r4145686265),
[completed connection tombstone restaging](https://github.com/delinoio/oss/pull/1232#discussion_r4145686279),
[failed native geometry retries](https://github.com/delinoio/oss/pull/1232#discussion_r4145686295),
and [asynchronous child-creation failure presentation](https://github.com/delinoio/oss/pull/1232#discussion_r4145686316).
Only the three original handled threads are eligible for resolution after this
pass's single push. No accepted review or merge-ready outcome is claimed.

The initial generated-output cleanup scan also reached the ignored Go cache's
source `cmd/dist` directory; permissions rejected deletion. Comparing its complete
contents against the same installed Go toolchain confirmed no difference.
Cleanup was narrowed to repository-owned project roots and completed there.
