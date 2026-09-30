# Restore reconciliation with schema 25

On 2026-09-30, merged main `574c1a92c` into PR #1222 after its initial
`70b7de3c26e92baee6d716a6fed8139feb1cf6c9` head reported conflicts.
The isolated managed worktree is `restore-1222-repair`; the original checkout
remains detached and unchanged while its broad Go validation continues.

## Composition

- Retain the complete schema-25 Grok accounting implementation and shared native
  compaction reservations from main. Restore introduces no additional migration
  or numeric reservation.
- Advertise automatic titles, forwarding, user services, native accounting,
  managed restore and permanent deletion independently at their existing wire
  values 1, 2, 3, 4, 7 and 9. Compose both owners' instructions and contracts.
- Regenerate Go and TypeScript/Connect Query bindings from the reconciled schema;
  do not select either generated conflict side.
- Replace the restore test's fresh-schema DROP-table approximation with the
  frozen `023-titles` fixture and existing test-only common-column helper.
  Only the private candidate migrates; the selected source remains byte-identical.
- The new server regression creates a verified Grok unit through original
  authenticated publication/completion. Restore preserves its native terminal,
  unit and exact counters with paused/recovery-required execution; a current
  session tombstone removes the unit through the shared foreign-key cascade.
  The separate full permanent-deletion tests remain the evidence for external
  Worker/backup obligations; this composition test does not simulate those claims.

## Completed validation

- `GOMAXPROCS=2 go test -race -p 2 ./cmds/delidev-cli/internal/store
  ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli
  -run 'Restore|BackupRestore|Accounting|HistoricalSchema' -count=1 -timeout 20m`
  passed: store 499.202 s, server 173.472 s and CLI 19.038 s.
- `GOMAXPROCS=2 go vet ./cmds/delidev-cli/...` passed.
- Protocol generation, format/lint and breaking checks passed. Exact generated
  freshness will be checked after the merge commit makes the reconciled outputs
  the committed baseline.
- Client tests passed all 46 tests in five files, including real temporary servers;
  frontend prerequisite client build and TypeScript checking passed.
- `pnpm --filter delidev-desktop prepare:assets` hydrated the required icon from
  the shared LFS cache; `git lfs fsck` passed. Original licenses/notices remain.
- The required `pnpm test` frontend command failed under its default concurrency:
  one New Project timeout and temporary-server readiness failure (94/96 files
  passed). Its GOMAXPROCS=2 rerun failed three App deadlines and one existing
  model pagination wait (94/96 files passed). No frontend source differs from
  the exact merged main `574c1a92c`, and no test deadline was increased.
- `GOMAXPROCS=2 pnpm exec vitest run --maxWorkers=2` then passed every frontend
  test: 96 files, 1,243 tests, no skipped tests, 143.52 s. All test cases and
  existing deadlines were retained. The remaining bundle/launch/widget/build
  steps are running separately; their terminal results receive follow-up evidence.

## Broad-run limits

The initial complete race command at the original implementation remains owned
by this chat (session 94932, `/tmp/delidev-1080-race-v3-final.log`) and is not a
passing result. In addition to the CLI failure independently reproduced on the
original freshly fetched main and Codex Steer deadlines, unchanged Grok native
closure/mode fixtures failed and its package reached the 20-minute timeout.
The server package also reached that timeout after Claude continuation/denial
fixtures reported safe storage errors. An isolated denial case passed both the
original restore checkout (12.663 s) and independent original-main baseline
(18.313 s); the broad failures' precise cause is not established.

Do not overlap another complete run while that command still owns validation.
Record its eventual terminal outcome separately. Native account, installed
harness, Windows/Linux runtime and release/distribution acceptance remain outside
this evidence. No Rust source was changed.
