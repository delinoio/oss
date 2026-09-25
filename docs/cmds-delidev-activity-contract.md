# DeliDev retained activity

## Scope
This contract owns `cmds/delidev-cli/internal/{store,server,cli}/activity.go` and `ActivityService` in `protos/delidev/v1`.
`ActivityService.ListActivity` and `delidev activity list` expose a read-only chronological projection of durable server-owned execution jobs, native terminal inbox entries and schedule occurrences. The source record's canonical UUID-v7 is the activity identity. No copied prompt, output, instruction, credential, native thread/turn identifier or arbitrary diagnostic belongs in the projection. This is separate from inbox read state and cannot approve, dispatch, resume or confirm cleanup.

## Runtime and Language
Go in the root pinned module, SQLite and authenticated Connect.

## Users and Operators
The owner and paired desktop/CLI clients read activity; execution Workers publish original evidence without authority to query the product timeline.

## Interfaces and Contracts

### Sources and meaning
- `execution-accepted` links the immutable execution job, execution, session, project and original account. It means the server accepted dispatch; it does not prove native input started or succeeded. A separate typed current job state preserves queued/claimed/succeeded/failed/uncertain/canceled status, including pre-native failures that have no native terminal evidence.
- `execution-succeeded`, `execution-failed` and `execution-stopped` come from the original native terminal inbox evidence and link its exact job/execution/account. They preserve the native observation even if the independent product outcome is Stop or recovery remains unresolved. No activity row claims process cleanup.
- `schedule-cron` and `schedule-run-now` link the immutable occurrence and its schedule, project and any assigned session. The separately typed occurrence state is current, not an invented historical transition at acceptance. Waiting/skipped occurrences can have no session. Schedule configuration deletion preserves accepted occurrence activity.

Every entry exposes its original source kind/revision and source-record creation time as `observed_at_unix_ms`. This is server retention time, not an inferred provider execution timestamp. Legacy inbox backfills retain their actual retention time; missing historical events and precise native times are not reconstructed. Public PR handling remains unimplemented and therefore supplies no activity; its future adapter must retain stable original evidence and handling provenance rather than inventing a passing/completed state.

### Queries and consistency
`activity list` accepts optional `--session-id`, `--project-id`, `--limit` (1–200, default 50) and `--page-token`. Archive is included. Pages sort newest-first by source creation time and UUID as a deterministic tie-breaker. A signed expiring cursor binds actor, filters, source/session event epoch and last returned source identity. Source updates, deletion or changed session membership invalidate pagination with explicit restart guidance. Source payload reads are byte-bounded even though returned metadata is small; a page resumes strictly after its last returned source without copying a partial document.

## Storage
Activity is derived from existing private SQLite source records, with no separate content store, external search backend or remote storage. Original sources remain durable across server/client restarts and retain configuration-independent ownership.

## Security
Owner/paired-client authorization is mandatory and revalidated in the same bounded two-second read transaction. Workers and revoked clients cannot read activity. Original account attribution is resolved through immutable execution jobs, never through current Agent candidates or later session account selections. Invalid source relationships fail with recovery guidance. Sources whose owning session is gone are excluded immediately; future permanent session deletion must remove the underlying sources and managed backups as well. No new copied-content table or schema migration is introduced.

## Logging
Structured debug records contain only correlation ID, count and continuation presence.

## Build and Test
Run `go test -race -p 1 ./cmds/delidev-cli/...`, `go vet ./cmds/delidev-cli/...`, Buf format/lint/compatibility and exact binding regeneration after changes.

Real temporary SQLite and loopback RPC/CLI tests cover chronological versus UUID ordering, pagination/reopen, source deletion, native terminal receipt deduplication, original account/source links, cron versus Run now, schedule deletion, Archive, metadata exclusions, independent unread/cleanup state, invalid filters and transactional device revocation. Fixture evidence does not establish real provider inference, desktop presentation or other-platform native acceptance.

## Dependencies and Integrations
Use the existing execution job, inbox, schedule occurrence and session records; the metadata projection shares current revocation and signed cursor infrastructure. No native harness, GitHub or provider request runs during activity reads.

## Change Triggers
Keep protocol enums/bindings, CLI help, scoped `AGENTS.md`, project index and evidence ledger synchronized. New activity sources require an explicit metadata projection of retained original evidence and must participate in permanent session deletion. Never derive successful execution from dispatch, connection loss, a read-state change or generic resource updates.

## References
- [DeliDev project](project-delidev.md)
- [Schedule contract](cmds-delidev-schedules-contract.md)
- [Inbox contract](cmds-delidev-inbox-contract.md)
- [Repository defaults](repository-defaults.md)
