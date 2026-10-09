# Worker-owned MCP management contract

## Scope

Issue #2129 owns `cmds/delidev-cli/internal/mcpmanagement`, the MCP management
relay, bounded server coordination metadata, Agent selections and the desktop
MCP Settings page. Native adapters in #2001, #2002, #2131 and #2132 retain
independent runtime acceptance. Management never launches an MCP executable.

## Runtime and Language

Go owns the authenticated Worker catalog, protected credentials, revisions,
receipts and OAuth exchange. TypeScript/React uses generated Connect operations
and Connect Query inventory. Rust owns the separate trusted-window MCP callback
profile and joined listener/opener disposal.

## Users and Operators

An authenticated owner or paired client explicitly selects the original Runner.
The original primary Worker owns its joined management stream. Only an
independently accepted native execution owner may pin a runtime generation.

## Interfaces and Contracts

System `MCP_MANAGEMENT_V1 = 64`, Worker `MCP_MANAGEMENT_V1 = 40` and
`SaveAgentWorkerRequest.mcp_selections = 7` belong to this complete feature.
Field 6 remains the separate managed Skills owner; route models retain field 5.
These allocations confer management authority only.

`McpManagementService` lists, mutates and authenticates definitions through
closed typed messages. `McpWorkerService` joins the original primary stream and
reports one exact command result. Original machine, Worker device, instance,
primary-stream generation and actor must agree before forwarding and acceptance.
An auxiliary reconnect never resubmits a command. Requests and replies are
bounded at 128 KiB and 256 KiB; the Worker catalog has at most 128 definitions.

Every mutation has an actor-bound immutable request digest and receipt. Server
coordination stores only safe metadata, references and sanitized request digests,
never request bytes or secret values. A durable pending fence prevents new Agent
references during uncertain replacement or deletion. Only a positive original
Worker journal proof of rejection without effect clears a failed fence.

Agent omission preserves saved selections; explicit empty selections clear them.
New selections require exact original device/current revision, enabled readiness
and no pending mutation. Optional references are part of immutable execution
configuration. An independently accepted adapter calls `PinCurrent` for a new
dependent owner and retains that exact generation on replay; `Pin` checks an
explicit generation. Neither method starts a process. Management-selected
executions remain unsupported until an independent native adapter admits them.

## Storage

The original Worker owns a locked private version-1 catalog and a separately
protected `mcp-server` vault purpose. Catalog journals contain only definitions,
reference claims, digests, original actor receipts, OAuth claim metadata and
independent dependent-owner pins. They never contain environment/header values,
PKCE state/verifier, authorization codes, access tokens or refresh tokens.
Protected-reference claims precede writes; confirmed removal intent precedes
cleanup. Failed persistence fences the manager until original-scope recovery.

The server uses the existing bounded metadata table, with no SQLite migration.
Current Agent references block deletion and are identified in Settings. After
explicit unselection, catalog deletion retires independently pinned generations.
Only the original dependent owner's joined cleanup releases its pins. A failed
last cleanup retains its durable pin for idempotent original recovery. All
catalog-generation, pending-write and OAuth references join catalog cleanup.
User host configuration and unrelated processes remain outside this owner.

Portable Agent documents contain safe references only. Transfer preserves each
reference and sets `rebinding_required`; imported references grant no Worker,
credential or execution authority. Explicit current-catalog selection replaces
that inert state. Omitted legacy fields preserve it. Older readers fail closed
on unknown metadata rather than writing a destructive replacement.

## Security

Secret environment and header values are write-only. Inventory exposes names
only. Manual replacements require the complete selected name set and reject
header/control injection. Unchanged protected values may be retained only when
transport, endpoint, authentication and secret names remain the same.

OAuth uses one exact protected-resource issuer, public dynamic registration,
PKCE S256 and an original IPv4 loopback `/oauth/mcp/callback` listener. Registration
and token exchange each have a durable original claim before outbound effects.
An uncertain attempt cannot register or exchange again. Original callback host,
path, state, actor, Worker, definition revision and expiry are checked before
exchange. Callback bytes remain transient; verifier/state and tokens belong only
to the original Worker's protected vault. The native MCP profile grants no
provider-registration or provider-credential authority. No redirect is followed.

Opening Settings, reading inventory and saving configuration cannot authenticate,
launch a server or infer. Authentication is explicit. Connection-owned desktop
controllers preserve original uncertain requests and native generations across
Settings category changes; connection/window disposal joins original native
cleanup. The management catalog advertises no supported native harnesses.

## Logging

Structured logs contain only operation/definition/machine IDs, revisions, closed
outcomes and safe error classifications. Do not log endpoint/path content,
secret values, authorization URLs, callback state/codes or native content.

## Build and Test

Focused Go catalog, OAuth HTTP fixture, server reference/transfer and frontend
component fixtures establish automated ownership behavior. They do not establish
installed-native, real-account, remote-machine or packaged-platform acceptance.
Record source revision, exact commands/results and unresolved limits in PR/CI
records. Keep required checks and any explicitly authorized validation exceptions in
PR/CI records. Omitted checks are not passes. Run mandatory commit hooks without
bypassing them.

## Dependencies and Integrations

This owner composes the credential vault, original Worker lifecycle, existing
server transactions, desktop trusted OAuth host and portable configuration.
Each Codex, Claude Code, OpenCode and Grok runtime adapter must separately prove
its actual original process, selected immutable catalog snapshot, native
advertisement, failure attribution and independent cleanup before input.

## Change Triggers

Changes to catalog ownership, secret purposes, selection/import authority,
callback profiles or native admission must update this contract, the project
index, related domain contracts and scoped AGENTS files in the same feature.
Protocol numbers retain original ownership and allocation collision checks.

## References

- [DeliDev project](project-delidev.md)
- [Parallel ownership and allocation workflow](cmds-delidev-structure-contract.md)
- [Catalog](cmds-delidev-catalog-contract.md)
- [Credentials](cmds-delidev-credentials-contract.md)
- [Desktop](apps-delidev-desktop-contract.md)
- [Storage](cmds-delidev-storage-contract.md)
- [Protocol](protos-delidev-v1-contract.md)
- [Harnesses](cmds-delidev-harness-contract.md)
- [Repository defaults](repository-defaults.md)
