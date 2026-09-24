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
- [Provider and model catalog](cmds-delidev-catalog-contract.md)
- [Native API relay](cmds-delidev-proxy-contract.md)
- [Session acceptance and input queue](cmds-delidev-sessions-contract.md)
- [Implementation and evidence ledger](cmds-delidev-evidence.md)

## Cross-Domain Invariants
Go owns product logic. Every product operation uses authenticated Connect RPC with shared CLI validation and durable mutation request identities. A normal CLI command never starts a server. Server and Worker data are private and local, explicitly overriding the repository's R2 storage and cloud search defaults. SQLite belongs exclusively to the server; workspace files belong to the execution Worker. Session acceptance and its preparation job commit together; complete preparation results and session readiness publish together. Stop/Archive cancellation targets immutable job ownership, and uncertain native cleanup cannot become completed Archive or automatic retry. Preparation recovery binds the original immutable assignment and Worker journal, preserves ready files and requires explicit cleanup of incomplete preparation; successful recovery keeps dispatch paused. Public entity IDs are canonical lowercase UUID v7.

Local is the default; listeners default to loopback. Remote transport requires authentication and encryption. Native harness/provider protocols remain internal adapter boundaries. Codex native thread/turn controls preserve exact observed settings, input/turn identities and uncertain acceptance; typed core events cannot clear recovery or replace owned cleanup. These primitives do not independently authorize public session execution or freeze server configuration. No WebSocket, standalone client SSE, browser client, Docker distribution, account failover, harness installation, or automatic server updates are introduced.

Only observed real-environment results qualify as harness/platform/account integration evidence. Unit fixtures and cross-compilation must remain separately labeled. Unsupported native features must fail explicitly, without emulation.

First-execution configuration, initial account selection, input claim and per-Agent routing state share one durable transaction. The implemented internal primitive requires independently established native readiness; existing public sessions remain blocked until that execution integration is complete. Template contents/order are retained exactly, later edits cannot rewrite them, and current restrictions still apply.

The native API relay accepts only Worker-registered execution token digests bound to durable claimed jobs and the current server process epoch. Every request revalidates session, input, Worker, account connection and restrictions; account disconnection atomically requests cancellation of selected-account native jobs and joins relay cleanup before credential deletion. Worker cleanup/reporting remains a separate confirmation, and old disconnect receipts cannot cancel work on a replacement connection. First-native Stop/Archive persist targeted pause/cancellation; only matching terminal publication plus verified process cleanup can complete Archive, and Restore cannot resume execution. Codex verifies effective private provider configuration and passes an installed-harness/server-relay composition with a scripted local provider. Core input/message/terminal events now publish through a locked durable Worker outbox into atomic server state/transcripts with immutable native identity mapping. Native command/patch lifecycle and revision observations now retain dedicated tool records through the same outbox, including separate nullable stream/aggregate output. An installed macOS Codex Worker fixture verifies a private POSIX command with a scripted local provider; file-patch acceptance remains fixture-only. Native token observations and redacted notices now share that outbox; counters retain event-time attribution without becoming billable aggregates or inferred costs. The Worker loop now delivers scoped credentials and executes the accepted first Codex assignment through terminal cleanup reporting. Public native dispatch, aggregate usage, complete event normalization and native recovery remain pending.

Prepared first-execution workspace leases persist native ownership outside the workspace and serialize with preparation/recovery. Only verified execution-job process cleanup closes that claim; replacement Workers cannot infer cleanup from an absent directory or a released OS lock. The first Codex Worker runner now uses this lease; public dispatch and explicit recovery/later-turn lease integration remain required.

## Change Policy
Keep command, protocol, evidence, and scoped AGENTS contracts synchronized with each implementation increment. Preserve the complete normative requirements even when individual acceptance items remain in progress. Generated bindings are tool-owned. Never claim the project complete while required CLI/server/Worker acceptance items remain unimplemented or unverified.

## References
- https://github.com/delinoio/oss/issues/964
- [Repository defaults](repository-defaults.md)
- [Project template](project-template.md)
