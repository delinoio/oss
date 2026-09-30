# Managed backup restore replacement for issue #1080

## Source and scope

Started from freshly fetched `origin/main` at
`ad0e3e9a29cb3d8375ab5d168bb160c35a023250` on 2026-09-30. Issue #1080 remains
open; PRs #1114 and #1180 closed without merging. This replacement ports the
restore implementation and its pre-publication lifecycle/directory durability
repairs from #1180 onto main, without merging either old branch. Canonical
`SystemService` schema is reconciled and regenerated, preserving main's permanent
session deletion capability 9 and the already reserved restore capability 7.
No SQLite schema migration is added; the executable sequence remains at 24.

Owner/client Connect and equivalent confirmed CLI restore require exact inspected
bytes/hash and the current committed revision. Exclusive maintenance rejects
active/uncertain original work and unfinished external session deletions. A
current consistent safety image supplies authorization revocations, tombstones
and permanent backup obligations. Historical sessions stay paused and
recovery-required, nonterminal work is canceled, schedules are disabled, accounts
are disconnected and Worker grants/claims cannot revive. Credentials and Worker
files are outside restore.

External receipts pin the original actor/request, source and both publication
fingerprints. Stopped lifecycle intent precedes atomic publication. Startup
reconciles one original or replacement outcome before SQLite migration/serving;
exact requests observe the original receipt. Current deletion redaction also
removes shared remediation/session activity operands from old candidate graphs.
Settled temporary safety/candidate/migration images are identity-checked and
removed before serving, retaining metadata-only journals and the unchanged source.
Unknown or changed staging is preserved and blocks permanent erasure completion.

## Validation available at the implementation commit

- Root frozen `pnpm install` succeeded, including shared-worktree Lefthook setup.
- `go vet ./cmds/delidev-cli/...` passed after the final cleanup repair.
- `go test -race ./cmds/delidev-cli/internal/store -run
  'TestBackupRestore(RedactsDeletedSharedRemediation|HonorsPermanentDeletion|RejectsPendingSessionDeletion|UnacceptedImages)'
  -count=1 -timeout 5m` passed (47.170 seconds). These use real private SQLite
  images and external deletion journals, including later erasure of a restored
  survivor and retention of metadata restore receipts.
- The earlier imported restore store/server/CLI race suite passed before adding
  the deletion composition (176.824 / 38.021 / 20.967 seconds). Its seven
  process-crash checkpoints, WAL, corrupt/foreign/replaced images, revoked
  authority and exact receipts are being rerun against the final implementation;
  earlier results alone do not establish the final head.
- Generated-client lint/typecheck and explicit `dist` build passed.
- Two complete client-test attempts each passed 43 unit cases but failed the
  three-test real-server setup during its bounded 120-second Go build. A separate
  Go build succeeded; those attempts are not full client-suite passes.

The first merge-resolution script mishandled conflict markers; original Git merge
inputs were restored before substantive validation. The initial cleanup tests
then exposed macOS `/var` temporary-root alias rejection and a missing test-owned
parent directory. Both are repaired, and the four new deletion tests above pass.
One initial focused store run also exhausted its five-minute watchdog amid those
failures. An initial broad race run was intentionally interrupted after identifying
the cleanup bug; it is not a completed validation. Final focused/full race,
protocol and client retries are recorded separately after completion.

No frontend or Rust source changed. No LFS asset is consumed by the Go/protocol
or client checks. Generated client `dist` is temporary and removed before final
publication. Real credentials, native harness/provider execution and release or
platform distribution acceptance were not performed. Historical issue #964
acceptance gaps remain open; controlled SQLite/process fixtures are not native
account or distribution proof.
