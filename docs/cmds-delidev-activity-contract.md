# DeliDev retained activity

## Scope
This contract owns `cmds/delidev-cli/internal/{store,server,cli}/activity.go`, the domain/store `pr_activity.go` sources, the server `activity_pr.go` adapter, and `ActivityService` in `protos/delidev/v1/activity.proto`.
`ActivityService.ListActivity` and `delidev activity list` expose a read-only chronological projection of durable server-owned execution jobs, native terminal inbox entries, schedule occurrences and immutable PR handling metadata. Execution/schedule sources use their canonical UUID-v7 as the activity identity. PR transitions have independent immutable UUID-v7 identities and retain the original source UUID/revision. No copied prompt, output, instruction, credential, native thread/turn identifier or arbitrary diagnostic belongs in the projection. This is separate from inbox read state and cannot approve, dispatch, resume or confirm cleanup.

## Runtime and Language
Go in the root pinned module, SQLite and authenticated Connect.

## Users and Operators
The owner and paired desktop/CLI clients read activity; execution Workers publish original evidence without authority to query the product timeline.

## Interfaces and Contracts

### Sources and meaning
- `execution-accepted` links the immutable execution job, execution, session, project and original account. It means the server accepted dispatch; it does not prove native input started or succeeded. A separate typed current job state preserves queued/claimed/succeeded/failed/uncertain/canceled status, including pre-native failures that have no native terminal evidence.
- `execution-succeeded`, `execution-failed` and `execution-stopped` come from the original native terminal inbox evidence and link its exact job/execution/account. They preserve the native observation even if the independent product outcome is Stop or recovery remains unresolved. No activity row claims process cleanup.
- `schedule-cron` and `schedule-run-now` link the immutable occurrence and its schedule, project and any assigned session. The separately typed occurrence state is current, not an invented historical transition at acceptance. Waiting/skipped occurrences can have no session. Schedule configuration deletion preserves accepted occurrence activity.

Every entry exposes its original source kind/revision and source-record creation time as `observed_at_unix_ms`. This is server retention time, not an inferred provider execution timestamp. Legacy inbox backfills retain their actual retention time; missing historical events and precise native times are not reconstructed. PR handling retains new problem observations, local dismissal and each semantic attempt-state transition in the same source transaction. Existing historical resources remain unchanged; no retrospective events are reconstructed. Recorded attempt navigation retains its original repository labels across later shared-set recollection or renames; verification navigation comes from its immutable original problem version, not the mutable current set. Stable numeric identity, rather than current owner/name, validates historical membership. A dedicated immutable handling-verification record supplies `pr-verified-handled` only after an independent verifier has established the exact original versions. The private publication boundary is implemented and fixture-tested, but no production verifier, public write, Fix now or automatic controller calls it. Attempt success and provider resolution cannot create this source.

### PR metadata and outcomes
`pr-problem-observed` and `pr-problem-dismissed` retain stable remote repository/PR numeric identities, original problem UUID/content version, source revision, actor namespace/device, request UUID and server retention time. `pr-remediation-attempt` snapshots reserved, bound, running, uncertain, succeeded, failed, stopped, canceled and positive pre-native not-started outcomes separately. Succeeded means only the original attempt completed; it never means a push or verified handling. Exact request replay, unchanged collection through another repository alias, and repeated identical uncertainty create no additional transition. Publication failure rolls back both source and activity.

`pr-verified-handled` retains its dedicated verification UUID and exact original problem/content-version references. An internal proof commitment remains private and is never returned in the timeline. Re-retaining the same proof preserves its original identity/actor/time, including through another request. Dedicated verification retention is separately capped at 10,000 records per stable PR; exact replay remains usable at capacity and no proof is evicted. The verifier's production creation and remote/native acceptance remain separate required work.

`ActivityPRMetadata` contains only bounded IDs, decimal remote identities, repository owner/name, typed owner/client actor, typed mode/state and original version references. It excludes PR titles, feedback/comment bodies, output, native identifiers and credentials. `ListActivityResponse.capabilities` advertises `PR_HANDLING_V1`; `delidev activity list` exposes the same metadata using protobuf field/enum names in `pull_request`. Reading changes neither Inbox state nor PR handling. Desktop source inspection is an explicit read-only, disposable ResourceQuery disclosure and labels recorded versus current source revisions.

Shared PR problem/dismissal/verification sources have no session/project owner and appear in the unfiltered timeline. Bound attempt transitions retain their original session/project and remain visible in Archive. Deleting that session removes its activity, including its earlier unbound reservation; it preserves shared PR evidence and other associations. Permanent source/managed-backup cleanup remains subject to the coordinated session deletion contract.

### Queries and consistency
`activity list` accepts optional `--session-id`, `--project-id`, `--limit` (1–200, default 50) and `--page-token`. Archive is included. Pages sort newest-first by source creation time and UUID as a deterministic tie-breaker. A signed expiring cursor binds actor, filters, source/session/PR event epoch and last returned source identity. Source updates, deletion or changed session membership invalidate pagination with explicit restart guidance. Source payload reads are byte-bounded even though returned metadata is small; a page resumes strictly after its last returned source without copying a partial document.

## Storage
Execution/schedule activity is derived from existing private SQLite sources. PR transition snapshots and dedicated verification metadata use typed `problem` resources in the same existing tables, without a schema change, historical backfill, copied-content table, external search backend or remote storage. Original sources remain durable across server/client restarts and retain configuration-independent ownership.

## Security
Owner/paired-client authorization is mandatory and revalidated in the same bounded two-second read transaction. Workers and revoked clients cannot read activity. Original account attribution is resolved through immutable execution jobs, never through current Agent candidates or later session account selections. Invalid source relationships fail with recovery guidance. Sources whose owning session is gone are excluded immediately. Permanent session deletion removes the underlying sources and managed backups under the storage contract, including original attempt-source activity recorded before session binding. Shared attempt redaction preserves reservation provenance and lifetime counters without publishing a replacement business activity. No new copied-content table or schema migration is introduced. PR source reads independently validate stable ownership, exact content-version references, original revisions and actor provenance. Both protobuf and JSON pages are capped at 3 MiB without truncating a source or skipping entries; reads retain the existing two-second deadline.

## Logging
Structured debug records contain only correlation ID, count and continuation presence.

## Build and Test
Run `go test -race -p 1 ./cmds/delidev-cli/...`, `go vet ./cmds/delidev-cli/...`, Buf format/lint/compatibility and exact binding regeneration after changes.

Real temporary SQLite and loopback RPC/CLI tests cover chronological versus UUID ordering, pagination/reopen, source deletion, native terminal receipt deduplication, original account/source links, cron versus Run now, schedule deletion, Archive, metadata exclusions, independent unread/cleanup state, invalid filters and transactional device revocation. PR tests additionally cover original version references, dismissal and proof replay, alias deduplication, semantic attempt transitions, explicit failure/uncertainty, dedicated verification, atomic rollback, equal-time pagination, source inspection and session-owned cleanup. Fixture evidence does not establish real provider inference, independent production verification or other-platform native acceptance.

## Dependencies and Integrations
Use the existing execution job, inbox, schedule occurrence, session and PR problem/attempt resources; the metadata projection shares current revocation and signed cursor infrastructure. No native harness, GitHub or provider request runs during activity reads.

## Change Triggers
Keep protocol enums/bindings, CLI help and scoped `AGENTS.md` synchronized. Update the project index for ownership or cross-domain invariant changes, and record validation in independent `docs/evidence/delidev/issue-1083/` files while preserving the historical ledger. New activity sources require an explicit metadata projection of retained original evidence and must participate in permanent session deletion. Never derive successful execution from dispatch, connection loss, a read-state change or generic resource updates.

## References
- [DeliDev project](project-delidev.md)
- [Schedule contract](cmds-delidev-schedules-contract.md)
- [Inbox contract](cmds-delidev-inbox-contract.md)
- [Repository defaults](repository-defaults.md)
