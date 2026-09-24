# DeliDev v1 Connect contract

## Scope
`protos/delidev/v1` owns `delidev.v1`; generated Go bindings live in `protos/gen/go/delidev/v1`.

## Runtime and Language
Protocol Buffers and Connect RPC, with Go server/CLI clients. Native harness protocols never escape the Worker adapter boundary.

## Users and Operators
Authenticated owner clients and separately authorized outbound Workers. No unauthenticated product API or browser client.

## Interfaces and Contracts
Version 1 uses canonical UUID-v7 entity/request identities, expected revisions, bounded pagination, typed errors and correlation metadata. Product operations share CLI/server validation. Mutations commit durable request receipts with their state/events. Streaming is Connect server streaming; snapshots carry the event cursor they represent. Expired/invalid cursors require resnapshot, and slow consumers reconnect instead of accumulating unbounded memory. Worker authorization is limited to that machine's assigned operations/events.

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
