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
  44 client tests passed. Full `pnpm proto:check` freshness is checked against the
  committed generated sources.
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
