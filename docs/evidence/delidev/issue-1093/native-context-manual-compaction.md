# Issue #1093: native context and manual compaction evidence

Implementation branch: `kdy1/delidev-1093-compaction`, based on
`74701b8948694e2bf8f8ba6d07e596c2d2f358a7` (`origin/main`).
Recorded on 2026-09-30; this record covers the independent public action
composition in [the owning contract](../../../cmds-delidev-compaction-contract.md).

The change is additive existing-schema JSON, RPCs and generated clients. It adds no
SQL migration and does not rewrite legacy history or automatic compaction records.
Server/Worker tests use disposable state, private runtimes and controlled loopback
providers. No real account credentials or user harness state are required.

## Validation status

Executed on macOS arm64 with Go 1.26.8 and isolated temporary state:

- Focused server compaction race tests passed (29.368 s), including one durable
  receipt, exact revision/auth rejection, FIFO serialization, independent failed
  `compact_result`, original execution preservation, relay registration/revocation,
  conversation-recovery exclusion and Archive/forward cleanup ownership.
- Final focused Worker and CLI race tests passed (2.205 s / 2.451 s). Worker
  fixtures retain interrupted receipts without execution, reject changed immutable
  restore assignments and preserve native null/zero and failed compact status.
- `go vet -p 1 ./cmds/delidev-cli/...` passed with no diagnostics.
- Protocol lint and breaking checks passed; generated-client typecheck and all
  44 client tests passed. Full `pnpm proto:check` passed after merging base
  `9d110ced702e66bb50974c5ec98e830b86adbe5b`, with the breaking baseline pinned
  to that exact main revision; generated sources reproduced without drift.
  Generated-client typecheck and all 44 tests passed again after the merge.
- Post-merge focused race checks passed for CLI/server/Worker (2.108 s /
  19.139 s / 2.017 s), and full Go vet passed again.
- `pnpm test` in `apps/delidev` passed typecheck and 1,012 frontend tests, but the
  existing singleton Server Preferences integration test failed while waiting for
  its create/edit button. An isolated retry also failed. The aggregate command
  stopped before its bundle/desktop/widget/build stages. This change has no
  frontend implementation diff against the merged base, and does not claim that
  the complete frontend suite passed or establish the failure's cause.
- `GOMAXPROCS=2 go test -race -p 2 -timeout=30m ./cmds/delidev-cli/...` ran but did
  not pass. Existing CLI forwarding/session fixtures, discovery probes and Claude/
  Codex owned-process fixtures failed with unavailable/recovery/cleanup timeouts.
  After these failures the remaining run was interrupted at roughly 30 minutes.
  No race detector report was observed in the collected output; this does not
  establish a passing full suite. No fixture deadlines or process ownership
  requirements were weakened.

## Native acceptance limitation

The official temporary npm native package reported Claude Code 2.1.236. The public
`TestManualNativePublicSessionCompaction/success` fixture and the preexisting private
`TestManualNativeExplicitCompaction/successful/continue-false` baseline failed during
native initialization, before the new public command. A raw disposable launch
responded in 4.09 seconds; an owned diagnostic launch with stdin kept open responded
in 16.44 seconds, beyond the adapter's ten-second initialization bound. This is
consistent with launch scheduling delays, but is not proof of their sole cause.

A temporary scheduling adjustment confined to this test's supervisor processes
also failed initialization; it did not change production code or the adapter bound.
Shared temporary binary, Go cache and compiler files disappeared during separate
validation attempts. Final focused checks use private cache/toolchain copies to
avoid promoting those missing-file build failures into implementation evidence.

Success/rejection/insufficient-history/cancellation and post-action replacement
fixtures are committed as opt-in acceptance tests. Their actual pinned-native
acceptance remains unverified on this host. Unit/controlled lifecycle fixtures prove
missing versus zero and outer-success/failed-compact handling, but are not a
substitute for installed-native compaction and replacement acceptance.

No real account credentials or user harness state were used. No hosted-account,
subscription, other-OS, rich/stopped-history, release or complete issue-964 acceptance
is claimed. Automatic compaction/history owners and frozen prior evidence are
retained unchanged.

## PR #1195 queued-revocation repair

At reviewed head `2a0c04a45387771b9e9808c9df3d5350d82d1ef8`, the new
`TestWorkerRevocationPreservesCompactionDispatchBoundary` failed its queued case:
the canceled job retained compaction ownership and invented native recovery. Its
claimed case retained the required uncertainty. The repair releases only canceled
undispatched ownership, keeps FIFO paused and preserves the original execution,
checkpoint, pending input and last-action history. Authenticated revocation replay
and subsequent Stop/Archive are covered; claimed Stop/Archive cannot release
uncertain action ownership.

`go test -race -p 1 ./cmds/delidev-cli/internal/server -run
'Test(WorkerRevocationPreservesCompactionDispatchBoundary|PublicCompactionAtomicReceiptAndFIFO)'
-count=1` passed after the fix (16.735 s), using the same isolated Go 1.26.8
toolchain/cache on macOS arm64. This regression check does not change the full-suite
or installed-native acceptance limitations recorded above.
