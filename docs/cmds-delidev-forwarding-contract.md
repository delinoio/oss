# DeliDev Session Development-Server Forwarding

## Scope
`cmds/delidev-cli/internal/forwarding`, the server/Worker forwarding handlers and `session forward` CLI own issue #1089's authenticated session-bound TCP forwarding. The Go client owns a local listener; the execution Worker owns outbound connections to one explicitly selected Worker-loopback development port. This is independent of agent execution and the server-relative model API relay.

## Runtime and Language
Go owns durable control, authorization, bounded traffic relay, private cleanup receipts and native TCP handles. Connect server streams plus bounded unary frames use the existing authenticated outbound Worker connection. No inbound Worker listener, WebSocket product transport or hosted public tunnel is introduced.

## Users and Operators
The server owner or an authorized paired client can create a forward for a current unarchived session and its explicitly selected owning machine. Each forward belongs to its original client principal, paired Worker device, Worker instance and primary stream. Another client cannot claim, observe through ForwardService, stop or send traffic for it. Generic resource metadata is non-secret shared product state; it grants no forwarding authority.

## Interfaces and Contracts
- `ForwardService.StartForward` requires a UUID-v7 request ID, exact session revision, session/machine IDs and Worker port 1–65535. Local port zero requests an OS-assigned port; an occupied explicit port fails without remapping. The forward ID is the original start request ID. Start is durable acceptance, separate from listener readiness.
- `GetForward` reports current metadata. `StopForward` requires the exact forward revision and owning session. Start/stop receipts bind the authenticated actor and exact original request; replay returns current records without recreating side effects.
- `ClaimForward` grants each original client/Worker runtime one native lifetime before Listen or Dial. A repeated receipt returns `granted: false`; another request cannot reclaim it. A lost claim acknowledgment stays unconfirmed and cannot be retried as native authority.
- `WatchForward` binds each claimed peer once. The client publishes its actual `127.0.0.1:port` listener endpoint; endpoint validation also accepts exact `[::1]:port` for other Go clients. The bundled client and Worker use IPv4 loopback. The Worker never accepts a caller-selected address, hostname, URL, redirect or ambient proxy. It dials only `127.0.0.1` and the accepted Worker port.
- `WorkerService.WatchForwardRequests` is a separately joined outbound lane, bound to the current primary stream, Worker instance, device, enabled machine and fresh connection lease. It dispatches only a fresh connected client's original forward. Reconnecting the lane never scans retained records to reopen sockets.
- Both peers receive readiness only after their stream bindings are live. `SendForward` carries original UUID-v7 connection identities, contiguous per-direction sequences and closed `OPEN`, `DATA`, `EOF`, `CLOSE` frame kinds. Only the client can Open. TCP half-close is preserved. Frames are never replayed after a lost acknowledgment; a canceled or failed send ends the original forward.
- At most 64 unclosed forwards per server, eight per client and sixteen per machine; at most sixteen live sockets and 4,096 lifetime connection identities per forward. Each direction buffers at most sixteen 32 KiB frames, for a 64 MiB server payload bound across all forwards. Unary backpressure and stream writes have ten-second limits; native writes have ten-second limits, Worker dials five seconds, and silent streams lose authority after the existing 45-second connection timeout. Retained stopped history is paginated, not buffered as traffic.
- `ReportForwardCleanup` uses the original peer runtime and a durable UUID-v7 receipt, only after all original listener/socket handles and goroutines are closed and joined. Both peer outcomes are independent. An unclaimed peer never had native authority and needs no socket cleanup report.
- States are `pending`, `active`, `stopping`, `stopped`. `stopped` requires both independent cleanup flags. Endpoint metadata is historical after Stop, not evidence that a listener remains open. Process/server loss never recreates a claimed lifetime; unknown cleanup remains visible as `stopping` after Stop/reconciliation.
- Workspace storage waits for every original forward to be stopped with both peer cleanup confirmations. New starts and live claims/traffic require a present workspace; original cleanup authority remains usable during pending, uncertain or stored workspace states. These acceptance gates share the serialized session transaction, so storage and a new forward cannot acquire ownership concurrently.
- Agent Stop leaves forwards running. Archive requests forward Stop in the same transaction and every session Archive completion path shares a pending-forward cleanup gate. Archive completes only after forward and existing preparation/agent/title cleanup. Session deletion stops retained forwards atomically; deleted session authority cannot carry traffic. Restore does not reopen forwards.
- Revocation requests Stop atomically and cancels authenticated streams. Original Go handles close on canceled streams even during blocked reads/writes. A revoked client cannot submit cleanup; its private positive proof remains retained and server cleanup stays unconfirmed rather than fabricated.
- `session forward start --session-id ID --revision N --machine-id ID --worker-port N [--local-port N]` runs in the foreground until Stop, Archive, loss or cancellation. It emits a readiness JSON envelope with the exact local endpoint, followed by an independent final envelope when its lifetime ends. Repeating `--request-id` returns the original record and starts no listener.
- `session forward status --session-id ID --id ID`, `stop ... --revision N`, and `reconcile ...` share the same authenticated operations. Reconcile submits only an existing positive original local cleanup receipt; it cannot infer cleanup from process absence or reopen sockets. Opening the returned endpoint is an explicit user action.
- Typed server and Worker `SESSION_FORWARDING_V1` capabilities gate availability independently of automatic-title capability. Older Workers retain their existing primary behavior and cannot forward without negotiation.
- Managed database replacement requires every current forward to be stopped with both independent original peer cleanup flags. Restored historical forwards pass through Stop without reopening sockets or manufacturing cleanup; an unknown historical claimed lifetime stays stopping. Server capability wire value 2 remains forwarding, published user services retain value 3, and managed backup restore uses reserved value 7.

## Storage
Forward resources use the existing schema-24 generic entity, session, revision, event and receipt storage; no relational migration or destructive schema change is needed. Each record retains only ownership, ports, endpoint, state and cleanup flags. Actor-bound receipts store references, not traffic. Existing records are preserved.

Client/Worker private `forward-cleanup/<runtime-id>.json` journals bind the original endpoint, peer and cleanup request ID. They are synchronized before native work and marked clean only after joined original closure. Clean receipts can be reported after reconnect/server replacement without another claim or dial. Completed acknowledged files are removed; incomplete files remain recovery evidence. Inventory is bounded to 256 entries. Missing, changed or incomplete records cannot establish cleanup. No tokens or traffic enter these files. This private local state follows DeliDev's explicit exception to the repository's default R2 storage.

## Security
Every operation, including loopback traffic, is authenticated. Public control is owner/client-only; paired Worker credentials receive only the forwarding-request lane and original peer claim/traffic/cleanup endpoints. Each live operation rechecks current client and Worker authorization, exact session/machine binding, primary stream and server epoch. Traffic is opaque untrusted external content and gains no Connect, native command, credential or product authority. Existing exact allowed-origin/loopback Host validation remains active. Remote transport uses the existing mandatory HTTPS boundary. No port discovery or target expansion occurs. Model-provider localhost still means the server machine.

## Logging
Use structured `slog` events for accepted starts, lane/lifetime interruption, confirmed cleanup and retained cleanup failures. Debug traffic observations include only forward/connection UUIDs, role, closed frame kind, sequence and byte count. Never log traffic bytes, HTTP bodies/headers, authentication tokens, private journal content, local paths or raw native/network errors.

## Build and Test
Run `go test -race ./cmds/delidev-cli/...` and `go vet ./cmds/delidev-cli/...` from the root, plus `pnpm proto:check` for schema/generated changes. Run the generated client package's tests/typecheck. Tests use isolated SQLite, paired credentials, loopback TCP fixtures and private temporary cleanup state. Verify exact binary bidirectional bytes, half-close, start/claim receipt replay, foreign devices/sessions, explicit port conflicts, bounded order/backpressure, Stop versus Archive, revocation and receipt-only offline cleanup. Hydrate only assets consumed by validation and remove any generated repository-owned `dist` before completion. Fixture success does not establish real remote TLS, provider-account, native desktop or release acceptance.

## Dependencies and Integrations
The existing authenticated primary Worker stream and connection lease remain mandatory. Forwarding is independent of workspace observation, title execution, model API routing and agent job ownership. Additive `delidev.v1` schemas generate Go and TypeScript/Connect Query bindings; `ForwardQuery` is exported by the client package. No new upstream dependency is needed.

## Change Triggers
Update this contract, the project index, protocol/client contracts, evidence ledger and relevant command/protocol/package `AGENTS.md` when ownership, cleanup, bounds, capabilities, CLI shapes or wire schemas change.

## References
- [DeliDev project](project-delidev.md)
- [Complete product requirements](cmds-delidev-requirements.md)
- [CLI/server/Worker contract](cmds-delidev-contract.md)
- [Protocol contract](protos-delidev-v1-contract.md)
- [Client contract](packages-delidev-api-client-contract.md)
- [Session contract](cmds-delidev-sessions-contract.md)
- [Repository defaults](repository-defaults.md)
