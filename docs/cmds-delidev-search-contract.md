# DeliDev retained conversation search

## Scope
This contract owns retained search in `cmds/delidev-cli/internal/{domain,store,server,cli}/search.go` and its `protos/delidev/v1` boundary.
The single-user Go server owns conversation search in its private SQLite database. `SearchService.SearchConversations` and `delidev search` share the same owner/paired-client authorization, validation and current-source reads. Workers cannot search conversations. This is a read-only product operation; it never changes inbox read state, dispatches input or invokes a harness/provider.

The current implementation searches retained canonical message text, command/input/output/patch observations, native plan/reasoning artifacts and progress text. Queued unsent prompts, protected interaction responses, credentials, configuration instructions, filesystem content and private native history are not search sources. It does not manufacture transcript content missing from an adapter. Archived sessions are included by default; future permanent session deletion must remove these derived records in the same database deletion transaction and include the index in managed-backup erasure.

## Runtime and Language
Go in the root pinned module, with modernc SQLite and the existing Connect runtime.

## Users and Operators
The server owner and authenticated paired desktop/CLI clients query retained conversations. Execution Workers publish source messages but cannot perform product search.

## Interfaces and Contracts
`delidev search --query TEXT` accepts literal Unicode text, at most 1,024 UTF-8 bytes. Matching uses Unicode lowercase without accent removal; quotes, SQL/FTS operators and wildcard characters remain literal. Empty/whitespace-only text, NUL and the internal U+001F field separator are rejected. Results are ordered by canonical message UUID, with their original resource identity, revision and complete document plus current session name, Agent Worker, execution outcome and archive state. The message's `session_id` and `project_id` provide origin links; an empty project identifies General Chat.

Optional CLI selectors are `--session-id`, `--project-id`, `--agent-id`, `--account-id`, `--outcome all|not-started|running|succeeded|failed|stopped` and `--archive all|active|archiving|archived`. Account filtering uses the original message execution's immutable job account, never current Agent candidates or a later account selection. Other selectors use current retained session membership. Missing historical execution authority cannot invent an account match. The wire exposes closed enums and rejects unknown numeric enum values.

## Storage
Schema 13 adds a private `transcript_search` derived table with stable integer keys and a SQLite FTS5 trigram index. Unicode lowercase is applied identically to source text and queries. Three-or-more-rune queries use escaped literal FTS phrases; shorter queries use literal substring lookup under the same two-second cancellable read budget. SQLite remains the exclusive local search backend, overriding the repository's cloud-search default for this project. See [SQLite FTS5](https://www.sqlite.org/fts5.html) for trigram and external-content semantics.

Every canonical message write updates its index in the same state/event/receipt transaction. Source-message and source-session foreign keys cascade derived text removal; FTS triggers and secure-delete keep current index content synchronized. Search returns original message documents joined to current sessions in one authorized read transaction. Stable index keys survive database `VACUUM` and backup/reopen. A failed source mutation rolls back index changes. The migration takes the existing validated, synchronized backup before installing and backfilling the index; conflicting schema or malformed retained source fails without replacing original records. Backfill neither increments message revisions nor invents events.

Pages default to 50 records and allow 1–200. Both protobuf and protobuf-JSON encodings share a 4 MiB aggregate result budget, with envelope/token headroom; pages never truncate an individual source document. A byte-limited page resumes after its last returned message. Signed, expiring cursors bind the actor, every filter, normalized query, source-change epoch and last returned identity. A keyed commitment keeps query text out of readable cursor payloads. Message/session/execution-job changes invalidate pagination explicitly rather than skipping or repeating changed membership. Read-time authorization rechecks device revocation within the transaction.

## Security
Search shares the private local SQLite and original transcript authorization boundary. It adds no cloud search or external file storage. Transactional device checks prevent revoked readers from using previously authenticated requests; typed projections exclude credential and private native/configuration sources. Source foreign-key cleanup is mandatory but is not a substitute for coordinated permanent session and backup erasure.

## Logging
Structured debug logs contain only correlation identity, result count and whether another page exists. Queries, match text, source contents and page tokens never enter diagnostics. Search does not read the credential store or log native diagnostics.

## Build and Test
Run `go test -race -p 1 ./cmds/delidev-cli/...`, `go vet ./cmds/delidev-cli/...`, Buf format/lint/compatibility and exact binding regeneration after changes.

Tests use real temporary SQLite and loopback Connect/CLI resources: literal Unicode and punctuation, original-account attribution, Archive and combined filters, rollback, replacement/deletion, source epoch/actor/filter cursor rejection, revocation, both wire formats and byte-limited pagination, migration backup/rollback, `VACUUM` and reopen. These tests do not establish native harness or other-OS integration evidence.

## Dependencies and Integrations
SQLite FTS5 uses the pinned modernc runtime; Go protobuf and Connect code is generated from the versioned schema. Source publication, session membership, immutable Worker jobs and existing authenticated cursor infrastructure are the only data dependencies.

## Change Triggers
Keep this contract, scoped `AGENTS.md`, protocol bindings, CLI help, the project index and validation records in pull requests, issues and CI logs/artifacts synchronized. Any new transcript shape needs an explicit typed search projection; never index raw private protocol envelopes or credential-bearing configuration.

## References
- [DeliDev project](project-delidev.md)
- [Protocol contract](protos-delidev-v1-contract.md)
- [Repository defaults](repository-defaults.md)
