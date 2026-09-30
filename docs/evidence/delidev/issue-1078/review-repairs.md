# PR #1171 review repairs

## Full deletion-plan capacity

The general linked-ID validator stopped at 1,000 entries. Permanent deletion now
checks UUID validity and uniqueness within its independent 4,096-copy limit.
Boundary fixtures accept 1,000, 1,001 and 4,096 entries, reject 4,097, and reject
invalid/duplicate UUIDs at the end of a full-capacity plan.

`GOCACHE=/tmp/delidev-1171-gocache GOMAXPROCS=4 go test -race -p 2
./cmds/delidev-cli/internal/domain -run SessionDeletion -count=1` passed on
2026-09-30 (1.643 seconds of package test time). The first shared-cache attempt
failed during compilation because a cached standard-library object disappeared;
the repair uses a private temporary Go cache. These are temporary fixture results,
not native/account/distribution acceptance.

## Complete Worker copy inventory

Removal and completed-proof replay now share the same path inventory. The
inventory includes job/session process records, their recovery locks and one
bounded scan of matching title-runtime directories. Restoring any such copy
blocks acknowledgment without deleting the replacement. Fixtures also cover
restored execution runtimes, journals, recovery manifests, outboxes, execution
claims/history and PR-startup data, then confirm the original report remains
reusable after the fixture copies are removed.

`GOCACHE=/tmp/delidev-1171-gocache GOMAXPROCS=4 go test -race -p 2
./cmds/delidev-cli/internal/worker -run SessionDeletion -count=1` passed on
2026-09-30 (28.891 seconds of package test time). This is isolated managed-copy
fixture evidence; it does not add real native/account acceptance.

## Final backup inventory ownership

The final gated scan now requires every remaining backup to match the native
identity and metadata of the image classified without session content. An image
published under a new UUID or replaced at the same name cannot inherit the earlier
classification. Real temporary SQLite fixtures restore a containing image between
classification and the final scan, confirm it is preserved as pending, then confirm
a fresh pass classifies and removes it through the durable backup-deletion path
while preserving an unrelated clean image.

`GOCACHE=/tmp/delidev-1171-gocache GOMAXPROCS=4 go test -race -p 2
./cmds/delidev-cli/internal/store -run 'SessionDeletion|ManagedBackupInspection'
-count=1` passed on 2026-09-30 (17.745 seconds of package test time). These are
managed-image race fixtures, not physical free-space or native acceptance evidence.

## Read-only acknowledged report replay

A previously acknowledged report with the exact actor/work-bound SQL receipt now
returns without rewriting the external journal or changing its revision. The
fixture pins the journal's native identity, bytes and an old modification time to
detect an identical atomic rewrite, rejects a conflicting receipt, then removes
the SQL receipt and verifies reconstruction of the original digest without a new
acknowledgment revision. Original live-Worker authorization remains required.

`GOCACHE=/tmp/delidev-1171-gocache GOMAXPROCS=4 go test -race -p 2
./cmds/delidev-cli/internal/store -run SessionDeletion -count=1` passed on
2026-09-30 (11.641 seconds of package test time). These are temporary durable-state
fixtures, not native/account acceptance.

## Final repair validation

The four source repairs are `780355e19`, `a51b6eca2`, `9175343d8` and
`02b777db7`. Final checks on that source passed on 2026-09-30:

- `GOCACHE=/tmp/delidev-1171-gocache GOMAXPROCS=4 go vet -p 2 ./cmds/delidev-cli/...`.
- `GOCACHE=/tmp/delidev-1171-gocache GOMAXPROCS=4 go test -race -p 2
  ./cmds/delidev-cli/internal/store ./cmds/delidev-cli/internal/worker
  ./cmds/delidev-cli/internal/server ./cmds/delidev-cli/internal/cli
  ./cmds/delidev-cli/internal/workspace -run 'SessionDeletion|ManagedBackup|BackupDeletion'
  -count=1`: store 87.653 seconds, Worker 10.352 seconds, server 21.614 seconds
  and CLI 1.816 seconds. The workspace package had no matching tests and is not
  counted as additional fixture evidence.
- The full domain package passed under the root race attempt (2.232 seconds),
  including the new deletion-plan capacity fixtures.

The required root race attempt (`GOCACHE=/tmp/delidev-1171-gocache GOMAXPROCS=4
go test -race -p 2 ./cmds/delidev-cli/...`) reached the previously documented
`TestCLISessionAcceptanceQueueAndArchive` creation-diff `unavailable` failure at
`sessions_test.go:239`; the CLI package failed in 205.179 seconds. The identified
validation process tree was stopped after that confirmed failure (exit 143).
This attempt did not complete every package and is not a full-suite pass. The
prior unchanged-base qualification in this issue's README remains applicable;
no new base-only reproduction of that specific diff failure is claimed.

The remote pre-push head `ec94f5e060eca6754740d89cc99b60cedbe6fa93` had ten passing
and twenty-five skipped checks. Those results do not validate the repair commits;
new CI and review evidence must be observed after publication. These checks do
not expand real-account, native platform, distribution or reclaimed-space evidence.
