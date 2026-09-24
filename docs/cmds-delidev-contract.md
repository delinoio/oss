# DeliDev command, server, and Worker contract

## Scope
`cmds/delidev-cli` produces the `delidev` binary for macOS, Windows, and Linux. It contains the standalone CLI and the identical bundled-sidecar server/Worker entry points. The complete issue is retained in [requirements](cmds-delidev-requirements.md); implementation and verification progress is recorded in the [evidence ledger](cmds-delidev-evidence.md).

## Runtime and Language
Go, using the root module and pinned dependencies. Business logic is independent of desktop presentation. Initial version is `0.1.0` (unreleased).

## Users and Operators
One server owner, authenticated paired desktop/CLI devices, and outbound execution Workers. Agent Worker configurations are distinct from execution machines.

## Interfaces and Contracts
`delidev server start|stop|status` is the explicit infrastructure boundary. Normal product commands require a running server. JSON output is versioned, carries stable IDs and typed failures, and is separated from stderr progress. Mutations require a UUID-v7 request identity; identical retries return the accepted result, while reuse with different parameters fails. Writes use expected entity revisions.

Server defaults to `127.0.0.1:46310`; an explicitly configured `::1` or listener is supported. Conflicts fail. Non-loopback listeners require TLS. Authentication applies to loopback too, and origins use an exact allowlist. The server exposes actual bound addresses and compatible version/data-scope identity. Connect server streams use durable ordered cursors, coherent snapshots, indexed messages, bounded pages/buffers, and explicit resnapshot errors.

The default private data directory is the operating system's user configuration directory plus `delidev`; `--data-dir` selects an explicit scope. `owner.json` is an owner-only local bootstrap credential and never appears in command output. `server.json` contains only the actual endpoint and compatible scope/version identity. The database pins the server identity; missing or mismatched owner material cannot silently re-pair an existing scope. Detached explicit startup holds a separate startup lock and waits for authenticated readiness; a failed or uncertain launch directs the user to status and the private structured server log. A stop receipt binds the targeted server start time so retrying an old stop cannot stop a replacement process.

Configuration commands accept `--input FILE|-`; writes accept `--request-id UUID-V7`, and edits/deletes require the current `--revision`. A generated request ID is returned in the version-1 result/error envelope. Explicit remote connections use `--server URL --token-stdin`; stdin credentials are never placed in argv or ordinary output. Normal resource reads cannot create a data directory. A terminal with no document/credential input returns `missing_input` instead of waiting indefinitely.

Entity kinds, workspace modes, harnesses, account modes, lifecycle/recovery/archive/delivery states, source kinds, capabilities, routing and overlap policies are closed typed enums. IDs cannot collide across kinds. First-execution snapshots and account selection commit atomically with routing state. Stop, Archive, restore, outcome, and recovery are independent; restore never dispatches.

## Storage
The server exclusively locks its private data scope, owns SQLite with foreign keys/WAL/transactions, and refuses corrupt/newer state. Mutations and events commit together. Request receipts survive restart. Consistent backups include committed WAL state; destructive migrations require a backup. Restore validates integrity/schema and deletion tombstones before replacement. Secrets are excluded from SQLite, transcripts, snapshots, and ordinary output. Worker-owned workspaces/snapshots and local retained-content search deliberately override cloud file/search defaults.

## Security
Secrets enter through stdin/hidden input and authenticated transport only. Managed credentials use protected secure storage; GitHub PATs use the OS credential store and never enter Worker environments. The API proxy issues execution-scoped revocable credentials, injects upstream keys only on the server, fixes `HTTP-Referer`, and neither translates nor retries. Workspace operations preserve original Local checkouts. No secret-bearing raw child stderr, provider bodies, or URL credentials enter diagnostics.

## Logging
Use `log/slog` on stderr with correlation, operation, session, and Worker IDs. Stable typed failures retain safe causes and recovery guidance. Prompts, raw emails, tokens, provider bodies, and internal instructions are excluded. Structured JSON is never mixed with progress.

## Build and Test
- `go build ./cmds/delidev-cli`
- `go test -race ./cmds/delidev-cli/...`
- `go vet ./cmds/delidev-cli/...`
- Protocol formatting/lint and reproducible Go generation follow the protocol contract.
- Existing repository Go CI selects `cmds/**` and `protos/**` on all three operating systems. Native integration tests must use temporary SQLite/Git/process/PTY resources and never real user configuration or credentials.

Cross-compilation does not substitute for native platform evidence. Native adapter tests require explicit user-provided test accounts and installed binaries; ordinary tests use fixtures that are never present in production paths.

## Dependencies and Integrations
Connect Go/protobuf, modernc SQLite, UUID v7, native Git, and installed Codex/Claude Code/OpenCode/Grok Build interfaces. Harness discovery checks the explicit executable before PATH; never downloads a harness. Read-only GitHub integration uses explicit repository/PAT associations.

## Change Triggers
Update this document, the project/protocol contracts, the evidence ledger, and scoped AGENTS when ownership or behavior changes. Preserve the normative requirements snapshot and record any subsequent owner amendments explicitly.

## References
- [Project](project-delidev.md)
- [Repository defaults](repository-defaults.md)
- [Complete requirements](cmds-delidev-requirements.md)
