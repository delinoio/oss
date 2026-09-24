# DeliDev v1 Connect contract

## Scope
`protos/delidev/v1` owns `delidev.v1`; generated Go bindings live in `protos/gen/go/delidev/v1`.

## Runtime and Language
Protocol Buffers and Connect RPC, with Go server/CLI clients. Native harness protocols never escape the Worker adapter boundary.

## Users and Operators
Authenticated owner clients and separately authorized outbound Workers. No unauthenticated product API or browser client.

## Interfaces and Contracts
`DeviceService` owns expiring, single-use pairing grants and per-device revocation. Pairing is the sole unauthenticated RPC; it authenticates the presented one-time code inside its acceptance transaction. The initiating device generates its own random credential and sends only its SHA-256 verifier. An exact replay of an accepted pairing request recovers its acknowledgment without creating another device; a different use of a consumed grant fails. Pairing codes and device credentials never appear in ordinary resource documents, receipts, or CLI JSON. The CLI stores issuance codes in a private file and accepts joining codes through stdin.

`WorkerService` is the outbound execution boundary: an authenticated Worker attaches with a fresh process instance identity, receives bounded server-streamed work assignments, and reports results through separate unary RPCs. The server binds every assignment, acknowledgment, and result to that machine, job, and claimed process instance. A disconnect never authorizes a duplicate execution or a different-machine retry. Per-device revocation cancels active RPC contexts and rejects both later authorization and in-flight mutation commits.

The initial service boundary is `SystemService` (status, explicit stop, read-only doctor, durable manual backup), `ResourceService` (indexed get/list, coherent scoped snapshot, Connect event stream), and `ConfigurationService` (validated configuration save/delete, pure routing preview). `Resource.document_json` carries a version-1 closed Go-domain schema, capped at 1 MiB; the transport envelope carries typed entity kind, UUID-v7 identity, expected revision, and timestamps. Unknown fields, duplicate JSON keys, unsupported schema versions, and unsupported writable kinds fail. The configuration endpoint cannot write arbitrary lifecycle state or observed account health.

Repository saves return an accepted coordinator job in `SaveConfigurationResponse.job`. The server dispatches fresh read-only inspection to every configured checkout, validates preferred/base/starting remote names on each Worker, and commits the canonical repository plus successful coordinator outcome atomically only after all results arrive. Validation or revision failure preserves the previous configuration and a typed failed job; retries reuse the accepted operation. The successful job stores only the target ID/revision as its result, not a second configuration body. `--wait` waits within a bounded CLI deadline; accepted job identity remains available in failure output. Omitted `auto_fetch` defaults to true.

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
