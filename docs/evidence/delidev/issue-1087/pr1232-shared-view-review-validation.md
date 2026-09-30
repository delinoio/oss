# PR #1232: shared-view review and main reconciliation validation

## Revision and scope

This one-shot repair merges main `6c749670727b30679e722821846bc8dc00f5ac32`
and addresses the three current Codex findings collected at PR head
`3febf4e43cddd38bb12f5816a31d3548362dbef2`:

- `PRRT_kwDORRAKg86no65Z`: attempt every shared child recreation and retain each exact failure (`294798f87`).
- `PRRT_kwDORRAKg86no65f`: retain/retry exact Hide ownership through unmount and late completion (`0128370d0`).
- `PRRT_kwDORRAKg86no65p`: preserve the selected child when closing a background tab (`a7c84084b`).

Follow-up `fbd68c5a1` removes browsing data from idempotent Hide responses.
The merge commit is `b54b71a73`. Browser/Fork domain rules are composed rather
than discarded; generated outputs reproduce from the merged schemas. Historical
issue evidence and the ledger remain intact. No new wire allocation, migration,
CEF pin, issue, branch, PR or automation was introduced by this repair.

## Passing checks

- Git LFS integrity and DeliDev asset preparation; generated DeliDev client,
  DevHud administrator embed and async-commit-hook embed builds.
- `pnpm proto:check`: complete lint/breaking/generation freshness check passed.
- `node --test scripts/ci/delidev-proto.test.mjs scripts/ci/delidev-structure.test.mjs`:
  all six protocol/structure checks passed.
- Merge-focused fork/browser/files/diff component selection: 28 tests passed
  across four files, with one worker; frontend typecheck passed.
- Hide-focused browser/cleanup component selection: 13 tests passed across two
  files, including transient/pending closure and post-unmount retry. Typecheck passed.
- API-client `pnpm test`: 46 tests passed across five files.
- Final native host: `cargo test -p delidev-desktop --features desktop-host,custom-protocol -- --test-threads=1`
  passed 20 library and 24 host tests; four opt-in real-sidecar library fixtures
  remain explicitly ignored. The final run includes the data-free Hide follow-up.
- Final `cargo clippy -p delidev-desktop --all-targets --all-features -- -D warnings`
  passed. These native commands used the existing pinned CEF cache, existing shared
  target directory, two build jobs, no Rust compiler wrapper and temporary state.
- `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...` passed.
- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/store -run Browser -count=1`
  passed: server 31.098 seconds; store compiled with no matching test names.
  Server scenarios exercise browser storage through the server boundary.
- Packaging dry-run (eight tests), desktop launch/asset (16 tests), widget fixtures
  and production frontend build passed independently of the full frontend aggregate.

## Broad validation failures and limits

Required frontend `pnpm test` ran from `apps/delidev` and returned exit 1:
**180 failed, 1,112 passed, 102 files (28 failed/74 passed)**, 377.41 seconds.
Its client build and typecheck passed before Vitest. All three browser/cleanup/fork
files are absent from the failure inventory. The failure inventory includes App,
Settings, configuration/integration, sidebar, schedules, Doctor, subscription,
notification/tray, usage, device, backup, session-tool and PR presentation files;
119 printed failure groups report five-second test deadlines. The aggregate
stopped before its packaging/build suffix, which was run successfully separately.

A subsequent one-worker selection of App/browser/fork/cleanup completed with
**eight failed, 54 passed across four files**, 186.54 seconds. All eight failures
are App's five-second deadlines: New Project/focus, deferred Settings entry,
uncertain New Project abandonment, retained session/draft across Settings,
notification draft close, import draft close, wide-header/Search focus, and
New-session/conversation retention across header destinations. The other three
files passed. Controlled browser tests do not erase these broader failures;
their causes are not established by this repair.

Complete `GOMAXPROCS=2 go test -race -p 4 ./cmds/delidev-cli/...` returned exit 1:
**14 packages passed, eight failed** on the reconciled Go sources. Later changes
in this pass affect frontend/native browser code and documentation only.

| Failing package | Observation |
| --- | --- |
| CLI (222.123 s) | `TestCLISessionAcceptanceQueueAndArchive`: workspace file reader unavailable from its owning Worker. |
| Codex (329.878 s) | Missing/duplicate writable-root authority cases could not complete native handshakes. |
| Grok (602.002 s) | Original file-reply cases could not complete native initialization; package budget expired while text-footprint test was active for 22 s. |
| OpenCode (74.648 s) | Reconciliation byte-budget case received uncertain mutation completion. |
| Server (601.436 s) | Ten-minute package budget expired while approval-authority/heartbeat case was active for 59 s/5 s. |
| Store (602.451 s) | Ten-minute package budget expired while backup-restore exact-receipt case was active for 13 s. |
| Worker (602.806 s) | PR-startup/deletion cases retained unconfirmed descendant cleanup; package budget expired during stream termination/permission-denied (49 s). |
| Workspace (601.162 s) | Ten-minute package budget expired while Local fork/parent-deletion case was active for 1 m 17 s. |

A package-budget expiration does not prove that its last active case hangs.
These failures are retained as failures; no production timeout was enlarged and
no unrelated native authority/cleanup rule was relaxed to obtain a pass.

Root Cargo validation ran after the background-tab source edit and before the
native-only Hide-response follow-up:
`cargo test -- --skip managed_service_does_not_start_after_the_preflight_exhausts_its_deadline`.
The unchanged previously sampled unbounded managed-service fixture remains
explicitly excluded; see the prior publication-review validation. No complete
unfiltered root pass is claimed. The run returned exit 101 in unchanged Clibox
`tests/run.rs`: 32 passed, five failed, one filtered. Failures were managed-service
shutdown/readiness exit-code mismatch, three nested wrapper/npm launcher cases
with missing fixture marker files, and custom-CA override probe exit-code mismatch.
Earlier Binpm/Cargo Mono and Clibox CLI/configuration groups passed; the Clibox
configuration group took 457.16 seconds. Cargo stopped at the failing group, so
later workspace packages were not executed. The final focused native host and
Clippy commands cover the follow-up source.

## GitHub and native acceptance

The pre-push CI inventory contains one successful external Cloudflare Pages check
and no reported failing checks. It describes the old remote head and is not fresh
CI proof for the repair. Codex acceptance and merge readiness are not asserted.
Pending/new checks or review are deferred to the next scheduled maintenance pass;
this pass never merges or enables auto-merge.

Actual provider login/history/password behavior, real CEF shutdown/flush,
Windows/X11 rendering, installed packages and release acceptance remain
unperformed. Controlled fixtures use temporary state, not real credentials.
Generated repository-owned `dist` output was removed only within the owned
apps/packages/cmds/servers/crates roots, after rejecting symlinks/tracked files and
pruning dependency/target directories. The Go module cache was not traversed.
