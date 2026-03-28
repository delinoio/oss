# crates-rustia-agentica-foundation

## Scope
- Project/component: `rustia-agentica` MicroAgentica runtime contract
- Canonical path: `crates/rustia-agentica`

## Runtime and Language
- Runtime: Rust library crate
- Primary language: Rust

## Users and Operators
- Rust developers building tool-calling agents with AISDK + rustia validation.
- Maintainers integrating class tools and MCP tools under one deterministic agent loop.

## Interfaces and Contracts
- Stable component identifier: `core`.
- Stable public identifiers:
  - `MicroAgentica<M>`
  - `MicroAgenticaConfig`
  - `MicroAgenticaController`
  - `ClassController`
  - `McpController`
  - `ConversationOutcome`
  - `StepRecord`
  - `UsageSummary`
  - `MicroAgenticaBuildError`
  - `MicroAgenticaConversationError`
- Model trait bound contract:
  - `M: aisdk::core::language_model::LanguageModel + ToolCallSupport + TextInputSupport + Clone`
- Controller flattening contract:
  - class controllers register provided `aisdk::core::tools::Tool` values unchanged
  - MCP controllers enumerate tools via `Peer<RoleClient>::list_all_tools()`
  - MCP tool execution delegates to `Peer<RoleClient>::call_tool(...)`
  - duplicate tool names fail construction with `MicroAgenticaBuildError::DuplicateToolName`
- Prompt contract:
  - default execute prompt is ported from `@agentica/core` `prompts/execute.md`
  - default common prompt is ported from `@agentica/core` `prompts/common.md`
  - common prompt placeholders `${locale}`, `${timezone}`, `${datetime}` must be rendered each turn
- Loop contract:
  - non-streaming AISDK `generate_text` only
  - tools are attached through `with_tool`
  - stop condition uses configurable max-step hook (`stop_when`)
- MCP bridge workaround contract:
  - synchronous AISDK tool callback bridges async MCP call with `tokio::task::block_in_place + Handle::block_on`
  - workaround remains until AISDK exposes async tool callbacks

## Storage
- No persistent storage contract.
- Conversation history is in-memory AISDK `Messages` state maintained per `MicroAgentica` instance.
- Usage totals are in-memory accumulators (`UsageSummary`).

## Security
- Tool-name collision handling must remain fail-closed.
- MCP argument bridging must reject non-object/non-null JSON inputs before remote execution.
- Tool error payloads should preserve actionable diagnostics without exposing unrelated runtime internals.

## Logging
- Structured logs use `tracing`.
- Required baseline log points:
  - initialization (`tool_count`, `max_steps`)
  - turn start/end (`history_len`, `step_count`, `stop_reason`)
  - MCP bridge operations (`controller_name`, `tool_name`, success/failure)

## Build and Test
- Local validation: `cargo test -p rustia-agentica`
- Workspace baseline: `cargo test --workspace --all-targets`
- MCP bridge tests use local in-memory `rmcp` client/server transport.

## Dependencies and Integrations
- Core dependencies: `aisdk`, `rustia-llm`, `rmcp`, `schemars`, `serde_json`, `tokio`, `tracing`.
- Upstream contracts:
  - `docs/project-rustia-agentica.md`
  - `docs/project-rustia.md`
  - `docs/crates-rustia-llm-foundation.md`

## Change Triggers
- Update this file and `docs/project-rustia-agentica.md` together when API shapes, controller semantics, or loop behavior change.
- Update root `AGENTS.md`, `crates/AGENTS.md`, and `docs/README.md` in the same change when ownership or identifier mappings change.
- Update workspace membership in root `Cargo.toml` when crate path/membership changes.

## References
- `docs/project-rustia-agentica.md`
- `docs/project-rustia.md`
- `docs/crates-rustia-llm-foundation.md`
- `docs/domain-template.md`
