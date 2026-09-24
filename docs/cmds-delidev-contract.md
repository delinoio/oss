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

`device create-pairing --type worker|client --name NAME` writes a five-minute, single-use pairing document in a private file and returns only its path and public grant metadata. Pipe that document into `worker pair --worker-dir PATH --code-stdin` or `device pair --device-dir PATH --code-stdin`. Pairing journals its generated credential and exact request before contacting the server. A paired client uses its directory with `--data-dir`; Worker credentials cannot run owner product commands. `worker start --worker-dir PATH` runs the outbound foreground Worker explicitly and holds an exclusive private scope lock. Reconnects reuse the same process identity and report receipt; process replacement preserves unconfirmed jobs as uncertain. `repository inspect --machine-id ID --path PATH [--preferred-remote NAME] [--wait]` returns an accepted job; `job get/list` exposes durable progress. Worker inspection never fetches. Repository create/edit queues fresh validation on every configured Worker and accepts `--wait` for bounded completion. Only all-success validation can atomically publish the canonical checkouts; missing remotes or a concurrent revision change leave configuration untouched. Failed `--wait` results retain the accepted job alongside the typed error. Automatic fetch defaults to true when omitted.

Entity kinds, workspace modes, harnesses, account modes, lifecycle/recovery/archive/delivery states, source kinds, capabilities, routing and overlap policies are closed typed enums. IDs cannot collide across kinds. First-execution snapshots and account selection commit atomically with routing state. Stop, Archive, restore, outcome, and recovery are independent; restore never dispatches.

Workers continuously receive their outbound stream while a native operation runs. Connection loss/revocation or a 45-second heartbeat timeout cancels that operation and retains its journal after owned cleanup. The server keeps later jobs queued until the current assignment resolves, preventing a backlog of preclaimed native operations. Reconnect reuses definitive journal outcomes and preserves missing-completion uncertainty.

### Provider and model catalog
`provider presets` lists nine editable API/local defaults with explicit server-relative endpoint and compatibility guidance. `provider create --preset PRESET` creates ordinary provider configuration. `provider discover --account-id ID --revision N` publishes a durable catalog observation; enabled connected accounts also refresh automatically while the server runs. `model search` supports provider groups, display preferences and bounded scoped pages; `model resolve` returns one canonical provider/model identity or an ambiguity error. General `model create/edit/delete` supports manual registration, display names/aliases/order/hiding and NEW acknowledgement. Catalog discovery preserves manual/user-declared data and never changes health, compatibility or session configuration. See the [catalog contract](cmds-delidev-catalog-contract.md) for scheduling, failure/replay and deletion suppression.

### Sessions and ordered input
`session create` now atomically retains a session and initial input; `session enqueue`, `queue list/edit/remove`, session rename and inactive Stop/Archive/Restore are available through dedicated owner/client RPCs. Creation currently reports blocked native execution, and Resume is explicitly unsupported until the execution adapter is integrated. Input modes and transaction-assigned order remain immutable; removed content cannot return through old receipts. Restore preserves pause and outcome. See the [session contract](cmds-delidev-sessions-contract.md) for selections, bounds, Local origin verification, pagination and remaining native lifecycle requirements.

### Worker executable discovery
`machine discover --id ID --revision N [--input FILE|-] [--protocol] [--wait]` saves executable selections and queues a durable Worker job atomically. Without `--input`, it refreshes the existing selections. A supplied document requires an explicit `executables` array and replaces all four selections; an empty array resets every harness to PATH, while omitted harnesses in a nonempty array use that Worker's PATH:

```json
{"executables":[{"harness":"codex","path":"/absolute/worker/path/codex"}]}
```

Only owner/client authorization may change selections. A monotonically increasing machine `discovery_revision` binds every refresh to its selections independently of connection metadata revisions. An older result becomes a typed failed job and cannot replace newer observations. Repeated request IDs reuse the same accepted job. Completion and machine observations commit together after job revision and Worker-instance authorization checks. Remote diagnostic text is reconstructed from a closed local classification before persistence.

The Worker checks explicit absolute paths first and never falls back after their failure. Unset paths search only absolute PATH entries on that Worker. Symlinks resolve to the installed file; a found but broken or denied candidate remains visible. The current Windows discovery launcher accepts native `.exe` files; shell wrappers are explicitly incompatible rather than interpolated through a shell. Native launch failures preserve typed missing/interpreter, permission, incompatible-format, and unavailable classifications.

Version probes use ten-second deadlines and independent 64 KiB stdout/stderr bounds, private per-harness home/config/cache/temp directories, an explicit environment without inherited account tokens, SSH agents, proxies or loader options, and the owned process start/cleanup contract. Grok receives `--no-auto-update`; Claude Code and OpenCode receive their documented update-disabling settings ([Claude Code environment](https://code.claude.com/docs/en/env-vars), [OpenCode CLI environment](https://opencode.ai/docs/cli/)). No harness is downloaded, installed, updated, or invoked for inference. Runtime deletion follows proof of owned descendant termination; uncertain ownership retains the runtime and a recovery error. Logs contain job/harness identities and typed states, never raw probe output or executable paths.

Installation states are `unchecked`, `detected`, `missing`, `permission-denied`, `incompatible`, and `failed`. `detected` means only that the executable returned a bounded recognizable version. Version-only discovery leaves `protocol_verified=false` and capabilities empty. The optional `--protocol` performs a separate non-inference native handshake, records `verified`, `unsupported` or `failed`, and still grants no execution capabilities or selected-account readiness. The initial Codex `0.151.0` profile validates native initialization and a bounded read-only readiness operation; the remaining profiles and full execution adapters are pending. See the [harness contract](cmds-delidev-harness-contract.md) for bounded streams, late acknowledgments, profile validation and cleanup. Server timestamps distinguish observed results from pending refreshes. Ordinary tests use temporary executable fixtures and never discover or launch a user's installed harnesses.

## Storage
The server exclusively locks its private data scope, owns SQLite with foreign keys/WAL/transactions, and refuses corrupt/newer state. Mutations and events commit together. Request receipts survive restart. Consistent backups include committed WAL state; destructive migrations require a backup. Schema v3 adds indexed model identity/search and deletion suppressions. Schema v4 adds session visibility and unique ordered queue indexes, with synchronized pre-migration backups and transactional rollback from v1/v2/v3 on conflicting legacy identities/order. Restore validates integrity/schema and deletion tombstones before replacement. Secrets are excluded from SQLite, transcripts, snapshots, and ordinary output. Worker-owned workspaces/snapshots and local retained-content search deliberately override cloud file/search defaults.

## Security
Secrets enter through stdin/hidden input and authenticated transport only. The protected storage primitive and its native/file crash boundaries are specified in the [credential storage contract](cmds-delidev-credentials-contract.md); API account connect/disconnect/status RPC and CLI behavior is implemented in the [account lifecycle contract](cmds-delidev-accounts-contract.md), with unverified readiness and durable cleanup retries. `account validate` records a bounded non-inference observation through the [provider inspection contract](cmds-delidev-providers-contract.md); public model catalogs do not establish credential validity, and validation does not grant native harness capabilities or quota recovery. Managed credentials use protected secure storage; GitHub PATs use the OS credential store and never enter Worker environments. The [native API relay](cmds-delidev-proxy-contract.md) implements scoped JSON/SSE forwarding, server-only key injection, fixed `HTTP-Referer`, bounded cancellation and redaction without translation or retries. Production execution-token issuance, durable session authority and harness integration remain pending; the internal relay is not yet registered in the server router. Workspace operations preserve original Local checkouts. No secret-bearing raw child stderr, provider bodies, or URL credentials enter diagnostics.

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
