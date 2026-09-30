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
