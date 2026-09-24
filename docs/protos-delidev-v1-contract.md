# DeliDev v1 Connect contract

## Scope
`protos/delidev/v1` owns `delidev.v1`; generated Go bindings live in `protos/gen/go/delidev/v1`.

## Runtime and Language
Protocol Buffers and Connect RPC, with Go server/CLI clients. Native harness protocols never escape the Worker adapter boundary.

## Users and Operators
Authenticated owner clients and separately authorized outbound Workers. No unauthenticated product API or browser client.

## Interfaces and Contracts
The initial service boundary is `SystemService` (status, explicit stop, read-only doctor, durable manual backup), `ResourceService` (indexed get/list, coherent scoped snapshot, Connect event stream), and `ConfigurationService` (validated configuration save/delete, pure routing preview). `Resource.document_json` carries a version-1 closed Go-domain schema, capped at 1 MiB; the transport envelope carries typed entity kind, UUID-v7 identity, expected revision, and timestamps. Unknown fields, duplicate JSON keys, unsupported schema versions, and unsupported writable kinds fail. The configuration endpoint cannot write arbitrary lifecycle state or observed account health.

Version 1 uses canonical UUID-v7 entity/request identities, expected revisions, bounded pagination, typed errors and correlation metadata. Product operations share CLI/server validation. Mutations commit durable request receipts with their state/events. Streaming is Connect server streaming; snapshots carry the event cursor they represent. Expired/invalid cursors require resnapshot, and slow consumers reconnect instead of accumulating unbounded memory. Worker authorization is limited to that machine's assigned operations/events.

Page/event cursors are HMAC-bound to server identity and filter/session scope and expire after 24 hours. Pages are capped at 200 records. Snapshot overflow returns an explicit narrower-scope error instead of pretending a partial page is complete. Events carry metadata and indexed message/entity revisions; they never repeat transcript bodies. Stream sends have a 15-second write deadline and fetch at most 200 durable events at once. Authentication and correlation metadata also apply to streaming errors.

## Storage
Protocol messages never authorize clients to access SQLite. Credentials are write-only inputs to protected storage. Entity reads, snapshots, search, usage, diagnostics, and events contain no authentication material.

## Security
Bearer authentication and exact origin enforcement include local RPC. Remote transport requires TLS. Pairing codes are expiring and single-use; revocation invalidates live streams and future authorization. Only the server resolves provider keys and account eligibility.

## Logging
Versioned typed failures carry safe recovery guidance and correlation IDs. Never serialize raw upstream errors or credential values.

## Build and Test
Use the root pinned Buf/Go protobuf/Connect generators. Never edit generated code. Run schema formatting/lint, generation freshness, and command integration tests after protocol edits.

## Dependencies and Integrations
Root `buf.yaml`/`buf.gen.yaml`, Connect Go, protobuf, and `cmds/delidev-cli`.

## Change Triggers
Protocol changes update this contract and [command contract](cmds-delidev-contract.md), with generated output and compatible version handling in the same change.

## References
- [Project](project-delidev.md)
- [Repository defaults](repository-defaults.md)
