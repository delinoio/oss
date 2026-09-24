# Project: DeliDev

## Goal
Run personal AI sessions across projects, accounts, native harnesses, and execution machines with durable single-user ownership. Issue #964 is normative; the historical web platform in #722 is not inherited.

## Project ID
`delidev`; the Go component is `delidev-cli` and its standalone/sidecar executable is `delidev`.

## Domain Ownership Map
- `cmds/delidev-cli`: Go CLI, server, execution Worker, native adapters, storage, and service lifecycle.
- `protos/delidev/v1`: versioned Connect RPC schemas.
- `protos/gen/go/delidev/v1`: generated Go messages and Connect bindings.
- Desktop native presentation is a separate future app component; this implementation request covers the CLI and its server/Worker product boundaries.

## Domain Contract Documents
- [CLI/server/Worker contract](cmds-delidev-contract.md)
- [Complete issue #964 requirements](cmds-delidev-requirements.md)
- [Protocol contract](protos-delidev-v1-contract.md)
- [Worker workspace contract](cmds-delidev-workspace-contract.md)
- [Owned process contract](cmds-delidev-process-contract.md)
- [Native harness adapter contract](cmds-delidev-harness-contract.md)
- [Protected credential storage](cmds-delidev-credentials-contract.md)
- [Account lifecycle](cmds-delidev-accounts-contract.md)
- [Provider inspection](cmds-delidev-providers-contract.md)
- [Implementation and evidence ledger](cmds-delidev-evidence.md)

## Cross-Domain Invariants
Go owns product logic. Every product operation uses authenticated Connect RPC with shared CLI validation and durable mutation request identities. A normal CLI command never starts a server. Server and Worker data are private and local, explicitly overriding the repository's R2 storage and cloud search defaults. SQLite belongs exclusively to the server; workspace files belong to the execution Worker. Public entity IDs are canonical lowercase UUID v7.

Local is the default; listeners default to loopback. Remote transport requires authentication and encryption. Native harness/provider protocols remain internal adapter boundaries. No WebSocket, standalone client SSE, browser client, Docker distribution, account failover, harness installation, or automatic server updates are introduced.

Only observed real-environment results qualify as harness/platform/account integration evidence. Unit fixtures and cross-compilation must remain separately labeled. Unsupported native features must fail explicitly, without emulation.

## Change Policy
Keep command, protocol, evidence, and scoped AGENTS contracts synchronized with each implementation increment. Preserve the complete normative requirements even when individual acceptance items remain in progress. Generated bindings are tool-owned. Never claim the project complete while required CLI/server/Worker acceptance items remain unimplemented or unverified.

## References
- https://github.com/delinoio/oss/issues/964
- [Repository defaults](repository-defaults.md)
- [Project template](project-template.md)
