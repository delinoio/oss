# PR #1232 publication and presentation review validation

Source: `1487766a870cad480bbf4783fd369e9f8725a3e0`, branch
`kdy1/delidev-browser-1087-replacement`, 2026-10-01.

Independent current-review repairs:

- `a3996bec7`: staged tab-control publication under the reservation fence;
  [evidence](pr1232-tab-publication-repair.md).
- `bd4da541b`: reject superseded observed-address publication;
  [evidence](pr1232-address-publication-repair.md).
- `a1e86983a`: cache only successful native geometry and retry identical bounds;
  [evidence](pr1232-geometry-retry-repair.md).
- `cc3b4071f`: surface asynchronous child-creation failures for the exact view;
  [evidence](pr1232-child-failure-repair.md).
- `0ac6f23c4`: apply staged publication to first-tab initialization and close
  staging handles before error cleanup;
  [evidence](pr1232-initial-tab-publication-repair.md).
- `1487766a8`: bound worker reservation waits and cancel undelivered callbacks;
  [evidence](pr1232-reservation-wait-repair.md).

Merge `fb90e49d0` retains main
`9efb1917e0127a9223cee0969877238ab37c0e1d`. Initial protocol breaking validation
rejected missing managed-backup restore RPCs/messages, System capability 7 and
InspectBackup field 5 from that advanced baseline. The merge was clean, preserved
the shared allocations and generated sources, and changed no native Rust or
frontend source. Full protocol regeneration reproduced the reconciled bindings.

## Passing validation

Native commands use the unchanged pinned CEF cache at
`/Users/kdy1/Library/Caches/tauri-cef`, shared target
`/Users/kdy1/projects/oss/target`, empty `RUSTC_WRAPPER`,
`CARGO_BUILD_JOBS=2` and `TMPDIR=/private/tmp`.

- Final `cargo test -p delidev-desktop --features desktop-host,custom-protocol -- --test-threads=1`:
  20 library and 21 host tests passed. Four explicitly qualified real-sidecar
  tests remain ignored. The new publication, geometry, child-failure and
  reservation-cancellation regressions also passed independently before their
  respective commits.
- Final `cargo clippy -p delidev-desktop --all-targets --all-features -- -D warnings`:
  passed. The pinned desktop host compiled without a dependency change.
- `pnpm proto:check`: lint, breaking compatibility and exact fresh regeneration
  passed after main reconciliation.
- API-client `pnpm build`, `pnpm lint` and
  `pnpm exec vitest run --maxWorkers=1`: all 46 tests in five files passed.
- `GOMAXPROCS=2 go vet -p 1 ./cmds/delidev-cli/...`: passed on the merged Go tree.
- `GOMAXPROCS=2 go test -race -p 1 ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/store -run Browser -count=1`:
  server browser scenarios passed in 16.354 seconds. Store compiled with no
  matching tests; its browser persistence is exercised through the server.
- Frontend `pnpm typecheck` and
  `pnpm exec vitest run src/session-browser.test.tsx src/configuration-browser-cleanup.test.tsx --maxWorkers=1`:
  all 11 tests passed again after main reconciliation.
- Packaging dry-run: eight tests passed; desktop-launch/asset preparation:
  16 tests passed; widget fixtures and production frontend build passed.
- Six protocol-allocation/structure fixtures passed. Git LFS integrity, explicit
  required asset preparation, API-client/frontend builds and administrator/
  async-commit-hook embedded builds passed. Generated repository-owned `dist`
  directories were removed after validation.

## Broad local suite limits

No passing complete repository suite is claimed. Keep the historical
[preceding validation record](pr1232-native-review-validation.md) intact.

Default-concurrency frontend `pnpm test` at `cc3b4071f` returned exit 1:
81 failures and 1,205 passes across 101 files (21 failed, 80 passed), in 312.96
seconds. The failure inventory includes Settings, configuration/integration,
subscription, Doctor and tray cases with deadline/setup/assertion failures.
Neither browser test file was in that failure inventory. Later script stages
were run separately because failed units stop the composite command. This
run preceded main's additive API/Go merge; the unchanged frontend source plus
fresh merged client and focused browser tests were verified afterward. Causes
of the broader variable failures remain unconfirmed.

At `0ac6f23c4`, the default-thread native run before the reservation-wait follow-up
passed 20 library tests, but one of 20 host tests failed:
`forgotten_connection_purges_whole_original_scope_only_after_native_shutdown`
retained its staged forget marker unexpectedly. The unchanged fixture passed
immediately in isolation (0.56 seconds), and the complete native inventory
passed with one test thread before and after the follow-up. Concurrent broad
Rust/Go validation was active during the failed attempt; its precise cause is
unconfirmed. No production deadline was enlarged to obtain these passes.

Complete `GOMAXPROCS=2 go test -race -p 4 ./cmds/delidev-cli/...` returned exit 1:
17 packages passed, including Codex (185.471 seconds), store (435.151), Worker
(309.416) and workspace (465.248). Five packages failed:

- CLI (270.425 seconds): `TestCLISessionAcceptanceQueueAndArchive` reported an
  unavailable owning-Worker workspace reader.
- Harness discovery (78.607 seconds): Claude and Grok discovery deadlines.
- Claude (196.817 seconds): the valid private-runtime API-stream case reported
  uncertain request delivery.
- Grok (601.399 seconds): ten-minute package deadline while original-plan
  uncertainty/planning-unclaimed-resolution was active.
- Server (601.794 seconds): ten-minute package deadline while changed or
  contradictory startup-report account evidence was active.

Deadline snapshots show cases active when the whole package budget expired;
they do not prove that those individual cases hung. This run used the merged Go
source at `fb90e49d0`, unchanged by the subsequent Rust-only follow-ups.

Root Cargo was run from the repository root after each source follow-up using
`cargo test -- --skip managed_service_does_not_start_after_the_preflight_exhausts_its_deadline`.
That one unchanged fixture was excluded because the preceding maintenance pass
independently sampled its unbounded `TcpListener::accept`/join; the exclusion
does not establish a full unfiltered pass. The first run failed unchanged
Clibox `blocked_stdin_can_be_interrupted` (eight passes, one failure). The next
two runs reached Clibox `tests/run.rs`: 35 passed, two nested-npm-launcher
descendant tests failed with missing fixture-marker files, and the one known
fixture was filtered. Root Cargo returned exit 101, so later workspace tests
did not run. No Clibox source or test was changed.

## Maintenance boundary

The final one-shot inventory found the same four current actionable Codex
threads, no reported failing checks among 47 entries on the preceding pushed
head `03ba34c18`, and an open, non-draft PR with merge status `BLOCKED` and no
accepted review decision. Only the four handled threads are eligible for
resolution after the single push. Outdated findings were excluded by the repair
workflow. A new push invalidates earlier CI/review evidence; fresh results are
left to the next scheduled heartbeat. No merge or auto-merge was requested.

Controlled temporary-state tests, mocked frontend invocations and compilation
do not establish live provider login/history/password behavior, real CEF
shutdown/flush, Windows/X11 rendering, installed packages or release acceptance.
Those outstanding requirements remain visible in the prior issue evidence.
