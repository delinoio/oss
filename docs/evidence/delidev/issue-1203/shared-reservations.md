# Issue #1203 shared-compaction prerequisite

## Inspected source

On 2026-09-30, freshly fetched `origin/main` and the initial clean checkout both
resolved to `ad0e3e9a29cb3d8375ab5d168bb160c35a023250`. Issue #1203 was open,
with no discussion or matching open implementation PR. Related issues #1093
and #1202 require the same owner/client manual-compaction boundary.

This is source inspection and reservation validation. In that revision,
`observer_parts.go` rejects compaction at its unsupported-part boundary,
`observer.go` has no accepted compaction event profile, and
`checkpoint_history.go` compares unchanged original message/part digests.
There is no public `CompactSession` RPC, CLI command, durable compaction action
or complete desktop control. Existing part decoding and Claude private native
fixtures cannot satisfy OpenCode's full acceptance criteria.

The pinned OpenCode source was read through authenticated `gh api` at
`545f51d26cc39a907d2867492d498d9607ea5fa4`. The summarize handler selects the
explicit provider/model, defaults `auto` to false, runs its native prompt loop
and returns true. The compaction implementation owns summary messages, recent
tail selection and original tool-part compacted timestamps. These source reads
did not execute OpenCode or a provider.

## Change and prerequisite

The main-first allocation rule prevents dependent implementation before a shared
reservation has landed. This change reserves EntityKind 32, SystemCapability 15,
WorkerCapability 5 and SessionChange field 9 under issue #1203, recording #1093
and #1202 as shared consumers. It adds a planned shared contract and routes the
owning project/protocol/command instructions to it. Ledger checks validate a
single PR-or-issue owner and distinct positive shared issue identities.

The existing generic entity/job/receipt/cancellation storage is retained. No
executable migration, schema field, generated binding, native adapter, RPC,
CLI command, desktop control or runtime capability changes. Pending migration
reservations 25–27 and executable schema 24 remain unchanged. A later schema
requirement must receive its own main-first reservation.

This prerequisite cannot close issue #1203. The reservation PR must merge before
dependent implementation starts; the fix-issue workflow prohibits the agent
from merging or enabling auto-merge. A feature PR must later provide the actual
implementation and full closing reference, independently of this prerequisite.

## Executed validation

- `pnpm install --frozen-lockfile`: passed; linked-worktree Lefthook installation
  and app preparation completed. The lockfile did not change.
- `node --test scripts/ci/delidev-structure.test.mjs scripts/ci/delidev-proto.test.mjs`:
  passed all six checks, including wire-number collision protection and unchanged
  pending migration ordering.
- `pnpm ci:contracts`: passed all 113 checks.
- `pnpm proto:check`: passed format/lint, FILE breaking comparison and forced
  generation freshness; generated Go and TypeScript sources had no drift.
- `pnpm --filter @delinoio/delidev-api-client typecheck`: passed.
- Initial `pnpm --filter @delinoio/delidev-api-client test:unit`: 41 tests passed;
  the three real-Go integration cases could not run because the fixture build
  failed at its 120-second limit. The suite returned failure.
- Separate `go build -o /tmp/delidev-issue-1203-build ./cmds/delidev-cli`: passed.
- Final `pnpm --filter @delinoio/delidev-api-client test:unit` rerun: all 44 tests
  passed across four files, including the three temporary real-Go Connect cases.
- `git diff --check`: passed. No repository-owned generated `dist` directory was
  present, and no generated binary is included in the change.

## Remaining acceptance

No native compaction acceptance is claimed. All seven issue #1203 scenarios,
including automatic ownership publication, one-request manual summarize,
repeated compaction with retained tails and completed tool history, fresh-process
checkpoint restoration, response-loss reconciliation, races and usage coverage,
remain required implementation work. Account/model/transcript preservation and
independent native settlement/cleanup must be demonstrated by that work.

The reservation changes no Go, Rust or frontend runtime source. Full Go race/vet,
frontend and native/profile/platform/account acceptance were not performed for
this prerequisite. The client suite's temporary real-Go server is transport
regression evidence, not OpenCode execution or compaction acceptance.
