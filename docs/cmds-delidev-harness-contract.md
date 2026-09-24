# DeliDev native harness adapter contract

## Scope
`cmds/delidev-cli/internal/harness` owns Worker-native installation discovery and protocol adapters. All four issue #964 harnesses remain required: Codex, Claude Code, OpenCode and Grok Build. This contract records implemented boundaries without reducing the complete [requirements](cmds-delidev-requirements.md).

## Runtime and Language
Go; native subprocess ownership follows the [process contract](cmds-delidev-process-contract.md). Harness protocols are private Worker implementation boundaries. Product commands and events remain authenticated Connect RPC, never native endpoints exposed to clients.

## Users and Operators
Users install and update harnesses themselves. A Worker selects an explicit executable or its own PATH. Installation/version detection, native protocol verification and selected-account execution readiness are distinct facts; none substitutes for another.

## Interfaces and Contracts
`machine discover --protocol` binds a non-inference native validation request to the same durable machine discovery generation as its executable selections. Detected installations receive a protocol observation with `verified`, `unsupported` or `failed` state. Missing/denied installations do not claim a handshake. An ordinary version-only discovery cannot publish native protocol observations. Only locally supported installed-version profiles can report verified; raw remote errors are reconstructed into safe diagnostics by the server. Current handshake observations never populate execution capabilities or authorize an account.

### Native stdio transport
The internal `nativewire` connection consumes newline-delimited JSON-RPC on distinct native stdout and stdin streams and discards raw stderr. It validates UTF-8, unique JSON keys, required envelope discrimination, request identity type and protocol version, and preserves byte-fragmented Unicode. Codex's optional bounded `emittedAtMs` notification field is retained as provenance, not an ordering or deduplication identity. Unknown envelope shapes fail instead of being silently interpreted.

Each frame/request/response is capped at 1 MiB. Pending outgoing requests and incoming interactions are each capped at 128; buffered events are capped at 128 and 8 MiB. A connection remembers at most 4,096 sent request identities, then requires explicit draining/reconciliation and a new connection. Slow consumers, oversized frames or invalid protocol input stop the owned connection with a typed diagnostic. Cleanup must still prove descendant termination before private runtimes are removed.

Callers allocate and durably record UUID-v7 operation identities before side-effecting sends. The transport never retries and rejects reuse of a sent identity. Cancellation before a write does not consume the identity. A missing acknowledgment after transmission is recoverable uncertainty; late definitive responses retain their original request identity for reconciliation. Response delivery serialized with timeout wins when already observed. Blocked writes terminate the owned scope instead of leaving an unbounded writer or authorizing another send. Raw native error messages/data never escape the adapter transport; only their numeric classification is retained.

Server requests carry a per-arrival token in addition to their native numeric/string identity. Exactly one concurrent reply may claim that outstanding request, and replaced/already-claimed interactions fail. The adapter must validate answers against the original typed request and current authorization before replying. Successful pipe delivery proves only transmission; native acknowledgment/state must establish semantic acceptance. Notifications and late replies use the bounded event path; no automatic answer, approval or prompt is synthesized.

### Codex app-server profile
The initial profile is Codex `0.151.0`, checked against that installed binary's generated schemas and a real macOS handshake. Unknown versions remain explicitly unsupported until their native contract is validated. This is not evidence of other operating systems, account combinations, or execution features.

The adapter starts owned `codex app-server` over stdio with a private `CODEX_HOME`, ephemeral native authentication, automatic update checks disabled and analytics/feedback disabled. Initialization identifies DeliDev, stays on the stable API, validates the returned native version/platform/private home, sends `initialized`, and confirms readiness with a bounded `thread/loaded/list`. A probe must have no loaded native threads or further cursor. It performs no login, account/model refresh, thread creation or inference. Unknown response fields, foreign homes/platforms, changed versions and nonempty loaded state fail explicitly. Failed validation closes and reconciles the process; uncertain cleanup retains the runtime.

Native thread/turn/Steer/fork/Plan/interaction operations, selected-account binding, normalized durable events and the remaining harness adapters remain required implementation work. A successful initial handshake does not claim those operations are implemented or supported end to end.

## Storage
Native protocol content remains transient inside the Worker adapter until typed product state is accepted by the server. Ownership journals contain only process metadata. Discovery stores selected/resolved paths, bounded versions, protocol classifications and server observation timestamps, never credentials, raw initialization payloads or native error bodies.

## Logging
Use structured owner/job/harness identifiers, bounded native versions, handshake phase enums and safe error codes. Do not log native messages, prompts, commands, user-agent strings, account data, home paths, or stderr. Native emission timestamps are event provenance and do not replace server commit ordering.

## Build and Test
Run delidev Go race tests and vet, regenerate/lint protocol bindings and verify reproducibility. Native subprocess fixtures test frame splitting, error redaction, concurrent replies, missing/late acknowledgments, pre-send cancellation, blocked input, malformed/oversized output, bounded consumers, profile mismatches and ownership cleanup. Ordinary tests must never launch installed user harnesses or access their logins. Record real native evidence separately in the [ledger](cmds-delidev-evidence.md); cross-compilation is not native acceptance.

## Dependencies and Integrations
- [Codex app-server](https://learn.chatgpt.com/docs/app-server): installed-version schema generation, initialization and native methods.
- [Codex configuration](https://learn.chatgpt.com/docs/config-file/config-reference) and [authentication](https://learn.chatgpt.com/docs/auth): private homes, ephemeral native credentials and update settings.
- DeliDev owned native process scopes and authenticated outbound Worker jobs.

## Change Triggers
Update this contract, command/protocol contracts, scoped AGENTS and the evidence ledger when adapter profiles, wire semantics, runtime isolation, capability validation or native acceptance changes. Keep incomplete harness/account/platform combinations explicit.
