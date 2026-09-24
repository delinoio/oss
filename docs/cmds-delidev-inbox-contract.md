# DeliDev retained inbox contract

## Scope
The Go domain, store, server and CLI under `cmds/delidev-cli` own retained inbox records. The complete issue #964 remains normative; desktop-native notification presentation belongs to a future app component.

## Runtime and Language
Go and the server's existing private SQLite/Connect boundary. No independent client database or notification service owns inbox state.

## Users and Operators
One server owner and its authorized paired clients share read state. Execution Workers publish native observations but cannot mark requests read or authorize owner responses.

## Interfaces and Contracts
Every inbox entry has its own UUID-v7 identity and revision, independent of its source interaction or execution. The initial closed source kinds are `interaction` and `execution-terminal`; read state is `unread` or `read`. Reading, listing, snapshotting and inspecting never change read state. Explicit read-state mutations cannot change source content, question revision, answer/claim/delivery, session state, routing, cleanup or execution authorization. Response acceptance never implicitly marks an entry read.

A native question and its unread inbox reference must commit in the same publication transaction. A native terminal observation similarly retains an unread completion/failure/interruption entry with immutable execution/input/job/thread/turn identity, publication sequence and observed native outcome. Terminal outcome does not prove process cleanup and can differ from the independently retained session outcome after Stop or earlier failure. Original receipt replay never duplicates an entry or resets read state. Repeated native closure, cleanup, Archive/Restore and later state inspection preserve the original entry identity and current read state.

Inbox documents retain references and bounded terminal metadata, not copies of question/answer/transcript content. Current response validity must be resolved from the original interaction plus current session state. A stale notification or inbox reference never renews execution authority. Completion/failure records remain alongside questions even when the desktop is disconnected or notifications are disabled. Notification presentation, permission/delivery handling, client activation and per-device delivery deduplication are separate pending contracts; no desktop delivery is claimed from an inbox write.

The storage foundation makes entries available through existing authenticated `inbox list/get` resource reads and snapshots/events. Dedicated joined list/inspect, mark-read/unread RPC/CLI and response navigation are the next integration boundary; they must use owner/client authorization, actor-bound reference-only receipts, exact inbox revision and current-state read-back. Their reads must join current source/session state transactionally and use bounded, filter-bound cursor pages. Interaction responses continue to use `InteractionService.RespondQuestion`, never generic inbox writes.

## Storage
Schema v10 adds unique source-kind/source-ID ownership and read-state/session indexes over `inbox` entities. Creation, source publication, state/events and request receipts are atomic. Source IDs are interaction UUIDs or execution UUIDs; every terminal record also binds its original queue input. Existing source records remain authoritative and are never rewritten by inbox backfill or read-state changes.

Migration from v1–v9 creates a synchronized private pre-migration backup, preserves earlier indexes/transcripts and backfills retained questions plus the terminal execution progress available in each legacy session. It does not invent missing historical native executions, closure, cleanup, answer acceptance or read state: newly materialized entries are unread, and their creation timestamp records backfill time. Process legacy entities in bounded pages within the migration transaction; malformed or contradictory source evidence aborts the migration and retains the original database and backup. Reopen cannot repeat a successful backfill. Retained source deletion must eventually remove associated inbox references through the coordinated session/deletion contract; public generic configuration cannot delete these records.

## Security
Only validated server/native observations create entries. Inbox text, source questions and response content are not permissions. Secret answers cannot enter ordinary inbox records, and metadata-only events never duplicate original question/answer bodies. Revoked clients and Worker credentials cannot mutate read state or use owner response APIs. Private server-local storage intentionally follows the session contract instead of cloud object storage.

## Logging
Use structured `log/slog` events with inbox/source/session/request/correlation identities and typed read state or failure codes. Never log question/answer text, transcript bodies, credentials or raw native diagnostics.

## Build and Test
Run `go test -race -p 1 ./cmds/delidev-cli/...` and `go vet ./cmds/delidev-cli/...`. Verify migration/backups/reopen, source deduplication, publication rollback, immutable completion evidence, original content retention and read-state independence. Dedicated RPC/CLI integration must additionally cover concurrent reads/responses, stale revision, exact receipt replay, revoked clients, filtered pagination and bounded joined documents. Native notification acceptance remains separate from deterministic inbox tests.

## Dependencies and Integrations
Native publication, retained interactions, execution completion, resource events, private SQLite, authenticated Connect and the future desktop inbox/notification presentation.

## Change Triggers
Update this contract, command/session/protocol contracts, project index, evidence ledger and scoped AGENTS whenever source kinds, read-state ownership, public operations, migration or notification boundaries change.

## References
- [Project](project-delidev.md)
- [Session and interaction contract](cmds-delidev-sessions-contract.md)
- [Connect protocol](protos-delidev-v1-contract.md)
- [Complete requirements](cmds-delidev-requirements.md)
- [Repository defaults](repository-defaults.md)
