# Restore PR maintenance after main changes

## Scope and revisions

PR #1180 for issue #1080 retains branch `kdy1/delidev-1080-restore`.
This pass merges main `b1b3e9e7c55511086a284021850426d48484b127` into
restore head `0944293d0cbfd68924d5deac4ffb027d387eda02`, without rebasing.
The sole textual conflict is in the server `AGENTS.md`: preserve both the
managed-restore ownership/barrier requirements and the newly merged verified
failed-Claude-completion requirements. Other main changes remain intact.

Regenerate service-owned protocol outputs from the composed schema. Existing
SystemService restore capability 7, all shared numeric reservations and schema 24
remain unchanged. This pass does not activate a reserved migration or transfer
service ownership.

## Composition regression

`TestBackupRestorePreservesSessionPRActivityDeletion` creates a real remediation
reservation and binding, backs up SQLite, then deletes the bound session. Restore
must preserve current deletion tombstones, including the activity transition made
before its original reservation had a session binding. The restored store retains
shared PR observation evidence while removing the deleted session, queued input
and all original session-owned activity. Shared historical remediation attempts
remain independently retained under the existing PR contract; they are not
session-child rows. The first fixture assertion incorrectly classified that shared
attempt as a deleted child; correcting the assertion required no production change.

## Executed checks

All Go commands use the independent task cache
`GOCACHE=/private/tmp/delidev-1080-go-cache`, preserving the prior shared-cache
interference qualification.

- `pnpm proto:check`: passed formatting, lint, breaking compatibility and forced
  regeneration/freshness. Composed Go/TypeScript generated outputs have no drift.
- `node --test scripts/ci/delidev-structure.test.mjs scripts/ci/delidev-proto.test.mjs`:
  six checks passed, including FILE compatibility, numeric reservations and
  separation of reserved migration versions from executable migrations.
- `go test -race -p 2 -timeout 10m ./cmds/delidev-cli/internal/store
  ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli -run
  'BackupRestore|RestoreBackup|RestoreStopped|PRActivity' -count=1`: passed
  (store 74.145s, server 6.796s, CLI 5.450s).
- `go vet ./cmds/delidev-cli/...`: passed.
- `pnpm --filter @delinoio/delidev-api-client lint` and `test`: passed;
  all 46 tests across five files passed.
- `pnpm test` from `apps/delidev`: passed typecheck, all 1,122 Vitest tests
  across 89 files, package/launcher/widget fixtures and frontend build.
- `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build` and the
  `GOOS=linux GOARCH=arm64` variant for `./cmds/delidev-cli`: passed.
- Required ignored DevHud administrator and ach embedded assets were generated
  before repository commit hooks; generated `dist` output is removed after validation.

## Complete race-suite outcome

`GOCACHE=/private/tmp/delidev-1080-go-cache go test -race -p 4 -timeout 20m
./cmds/delidev-cli/...` completed with exit 1 on the composed source committed as
`5e7da13d1c96c46aee37f38c808f5b97789216d2`. No package reached the configured
twenty-minute deadline, and no restore test failed. The broad suite remains
non-passing; these focused and package-level passes do not replace that result.

| Package | Result | Observation |
| --- | --- | --- |
| CLI | Failed, 185.106s | `TestCLISessionAcceptanceQueueAndArchive`: creation-comparison workspace file reader unavailable at `sessions_test.go:239`. |
| Claude harness | Failed, 216.080s | `TestProbeOwnsOnlyBoundedPrivateInitialization/foreign`: timeout returned unavailable instead of unsupported; `TestStreamRejectsProtocolFailuresAndStopsOwnedScope/invalid-utf8`: owned incompatible scope did not stop within the fixture wait. |
| Workspace | Failed, 690.490s | `TestWorkspaceDiffUnbornAndBoundedResults`: bounded oversized-diff observation timed out at `diff_test.go:170`. |
| Store | Passed, 457.248s | Includes every backup restore test and the new PR activity deletion regression. |
| Server | Passed, 1043.885s | Includes authenticated restore/barrier tests and the merged server fixtures. |
| Worker | Passed, 325.227s | Existing execution/cleanup fixtures. |
| Grok harness | Passed, 1184.342s | Completed before the package deadline. |
| Nativewire | Passed, 19.963s | The earlier independently baseline-reproduced logging-buffer race did not recur in this run. |

All remaining tested packages passed, including API proxy, connections,
credentials, domain, forwarding, common harness, Codex, OpenCode, GitHub,
presentation, process, providers, security and user services. The independent
cache avoided the missing-cache-file build failures from the initial run.

Prior full-suite failures and exact-main comparisons in
[the original evidence](README.md) remain valid historical observations. Prior
unchanged-base runs independently reproduced the named CLI and Claude failures;
this maintenance pass did not rerun those baselines. The workspace fixture also
failed previously, but this oversized-diff timeout has no independent baseline
attribution. Its failure remains visible rather than being classified as a
restore regression or proven upstream defect.

Generated repository-owned `dist` directories have been removed. No tracked
`dist` output or unrelated checkout changes are included in this repair.

These results do not establish actual-account, native harness, Worker workspace,
Windows/Linux execution or platform-distribution acceptance. Cross-builds and
fixtures retain their original evidence limits. CI and Codex review of the newly
pushed merge remain separate from local validation.
