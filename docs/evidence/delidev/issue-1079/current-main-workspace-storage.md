# Issue #1079: workspace storage on current main

## Source and ownership

The issue remained open and current main did not implement workspace storage.
This branch started at main `ad0e3e9a29cb3d8375ab5d168bb160c35a023250`,
reconciled the complete implementation and historical evidence from closed,
unmerged PR #1165 (`ea0efdc9196343e8222155eea16fffb0d75bf20a`), and then
merged main `7090de04621ece95b3c2cce8d88fcfcdeadad7cc` without rebasing. The combined implementation revision
is `46452512fcf4cfa832086419dfda45fba7b9f98f`.

Service-specific schema ownership, reserved workspace-storage capability 11,
permanent-deletion capability 9, and all merged capabilities are preserved.
Main's schema-25 native-accounting migration and independent layout marker are
retained; workspace snapshots add no database migration. Generated Go,
TypeScript, Connect and historical compatibility views are regenerated from the
reconciled canonical schemas. Existing issue-1079 evidence files retain their
original revisions, results and limitations.

## Additional changes

- Reject promisor/partial-clone Git configuration and `.promisor` pack markers
  before capture or inspection can establish a self-contained snapshot. Offline
  fsck alone cannot establish complete promised history.
- Synchronize the acknowledgment-bound retirement receipt before promoting the
  original finished Worker journal to reported. The receipt binds the exact
  output/problem digest; startup completes the interrupted transition and
  retirement without native replay.
- Turn unconfirmed independent scratch cleanup into recovery-required. Explicit
  recovery removes the complete original staging tree and settles the failed
  capture only after cleanup succeeds.
- Capture Windows file/directory symlink type from source reparse metadata and
  recreate that explicit type, retaining forward and dangling directory links.
  Unix inventories retain their original representation.
- Include original accepted storage jobs and reserved snapshot UUIDs in the
  immutable permanent-deletion work plan, including jobs without native output.
  Worker deletion joins original owners, removes private staging/removal/restore
  evidence and published copies, handles independent restored Git, and preserves
  the user's source checkout. Reappearing managed copies invalidate a completed
  cleanup proof.

A subsequent cross-domain repair (`ea577dcebcff6e52268ea930bdbabee36c944bf2`)
closes the session-forwarding gap found during reconciliation: every original
forward must have both cleanup confirmations before storage acceptance, and new
forwarding/live socket authority requires present storage. Cleanup authority
remains usable independently. Tests first prove the original forwarding
identity, Worker lease and primary lane are eligible so unrelated unavailable
authority cannot satisfy the exclusion assertion.

Scoped AGENTS and owning workspace, forwarding, storage and project contracts record these
ownership changes. Repository hooks ran for every commit.

## Executed checks before the accounting merge

These checks ran against the corresponding repair commits, not the later merged
revision. Their actual results are retained separately from final validation:

- Promisor capture/inspection race regressions passed (171.679s).
- Storage retirement race regressions, including cancellation after receipt
  persistence but before the reported transition, passed (13.450s).
- Failed scratch cleanup and explicit recovery race regressions passed (514.665s).
- The original storage reservation in the permanent-deletion plan passed under
  the race detector (3.916s).
- Permanent deletion of live, stored and restored snapshots passed for both
  General Chat and Worktree (six cases, 379.916s). The first run rejected the
  fixture's noncanonical macOS source alias; the fixture was corrected through
  realpath, then rerun. Production ownership checks were retained.
- Go vet, all 113 contract tests, and all 44 client tests passed.
- The required desktop `pnpm test` run failed four unchanged UI tests at their
  five-second bounds (92 files and 1,232 tests passed). The three affected files
  passed an isolated rerun (80 tests); no timeout or implementation change was
  made for those failures.

A broader race run overlapped subsequent source changes and was stopped. Its
partial CLI/native-harness failures and merge-time compiler errors do not
validate one fixed source revision. No complete suite success is claimed for it.
The initial protocol check also failed against main's newly added accounting
fields; the subsequent merge preserved those fields and regenerated bindings.

## Final validation at 46452512

- Root `pnpm proto:check` passed formatting, lint, compatibility and regenerated
  freshness. Its resolved main baseline was
  `574c1a92c957fc741a723ff8123888dad32a2194`.
- Root `pnpm ci:contracts` passed all 113 tests.
- Client build/typecheck and all 44 tests passed. The initial integration attempt
  hit the existing 120-second fixture build limit; after a separate CLI build,
  the complete unchanged suite passed in 12.01s.
- `go vet ./cmds/delidev-cli/...` and Windows amd64 workspace-test cross-compilation
  passed. Cross-compilation does not establish native Windows runtime acceptance.
- The desktop's complete Vitest suite passed all 96 files and 1,243 tests with
  two workers and `GOMAXPROCS=2` (209.01s). The prior constrained attempt had 11
  failed native-fixture suites: bounded builds/readiness and teardown failures,
  with 1,232 tests passed and 11 skipped. The successful rerun skips no tests
  and retains the original fixture/test bounds.

- Desktop bundle dry-run tests (8), launch/asset tests (16), widget fixtures and
  production build passed. The rerun executes the complete `pnpm test` script
  pipeline with an explicit two-worker Vitest argument and `GOMAXPROCS=2`.

The broad storage-focused race run's workspace package exceeded its cumulative
15-minute bound during `TestSnapshotMaximumInventoryRemainsDeletable` (94s into
that test; package 900.863s). It also reported a recovery-required restore result
at `snapshots_test.go:156` in the two-repository faithful-restoration test and
Git checkout inspection failures during setup of both disk-full cases at line
216. These are failed results, not full storage-suite success. The server
package passed (79.117s), as did Worker (215.992s), store (19.998s), domain
(1.651s) and CLI (1.955s). The Worker/store checks include every added retirement,
journal and permanent-deletion storage case. The exact command was
`GOMAXPROCS=2 go test -race -p 2 -timeout=15m
./cmds/delidev-cli/internal/{workspace,server,worker,store,domain,cli}
-run 'Snapshot|WorkspaceStorage|StorageRemoval|StorageRetirement|StorageJournal|StorageCLI|SessionDeletionIncludesStored|SessionDeletionPinsOriginal|DecodeBounded|PrivateJSONBound'
-count=1`.

The failed two-repository restoration and disk-full cases subsequently passed
together with the unchanged implementation (227.989s). The maximum 8,192-entry
inventory and cancellation-before-removal-claim cases passed together (359.243s).
Both isolated commands used `GOMAXPROCS=2 go test -race -timeout=10m`, the exact
two test names as an anchored `-run` expression, and `-count=1`. These passing
reruns do not erase the failed broader run or establish its cause.

After the forwarding repair, `GOMAXPROCS=2 go test -race -p 2 -timeout=10m
./cmds/delidev-cli/internal/server -run 'WorkspaceStorage|Forward' -count=1`
passed (45.922s). The strengthened independently eligible authority test also
passed. Go vet and all 113 contract tests passed again.

The full required Go race command runs against a fixed, source-only archive of
`46452512` containing the root Go module, command sources and generated protocol
bindings, with LFS smudging disabled for that source archive. Its command is
`GOMAXPROCS=2 go test -race -p 2 -timeout=30m ./cmds/delidev-cli/...` from the
archive's module root. It was still in progress when this evidence draft was
prepared; no complete result is claimed. Its CLI package failed
`TestCLISessionAcceptanceQueueAndArchive` at `sessions_test.go:239` with an
unavailable creation-diff reader (502.193s package time). The same exact isolated
race command, `GOMAXPROCS=2 go test -race -timeout=10m
./cmds/delidev-cli/internal/cli -run '^TestCLISessionAcceptanceQueueAndArchive$'
-count=1`, reproduces that line-239 unavailable reader on both the branch
(51.570s) and a source-only archive of main `7090de04621ece95b3c2cce8d88fcfcdeadad7cc`
(44.332s). That specific failure is present on the inspected main baseline on
this host. No complete Go race-suite success is claimed.

## Evidence limits

No repository-owned generated `dist` remains; dependency and toolchain source
directories are excluded from that cleanup.

Fixtures use temporary repositories/state, controlled native/provider children
and injected local faults. Required app LFS assets were hydrated; `git lfs fsck`
passed before the changes. No real account, user credential, remote push,
release publication or native Windows/Linux runtime acceptance is claimed.
Desktop storage management, database restoration and broader issue #964
acceptance remain separate product boundaries.
