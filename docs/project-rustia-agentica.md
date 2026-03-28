# Project: rustia-agentica

## Goal
Provide a Rust-first MicroAgentica runtime that ports the `@agentica/core` micro loop to `rustia`-based tool validation while keeping AISDK model/provider neutrality.

## Project ID
`rustia-agentica`

## Domain Ownership Map
- `crates/rustia-agentica` (`core`)

## Domain Contract Documents
- `docs/crates-rustia-agentica-foundation.md`

## Cross-Domain Invariants
- Stable component identifier remains `core`.
- v1 scope remains `MicroAgentica` only; `Agentica` orchestration split (`initialize/select/cancel/execute`) is out of scope.
- Function-calling protocols for v1 are fixed to class + MCP controllers.
- Class tool path must accept pre-built `aisdk::core::tools::Tool` and keep `rustia-llm::tool(...)` as first-class integration.
- MCP tool path must source schemas from `tools/list` and execute through `tools/call` via `rmcp` client peer bridging.
- Duplicate tool names across all controllers must fail construction with explicit build errors (no implicit renaming).
- Conversation state must persist as AISDK `Messages`, while public results are exposed through Rust-native outcome records (`ConversationOutcome`, `StepRecord`, `UsageSummary`).
- Non-streaming AISDK `generate_text` loop is the only supported execution mode in v1.

## Change Policy
- Update this index and `docs/crates-rustia-agentica-foundation.md` together when runtime APIs, supported controller protocols, or loop semantics change.
- Keep root `AGENTS.md`, `crates/AGENTS.md`, and `docs/README.md` synchronized with ownership and identifier changes.
- Update root `Cargo.toml` workspace membership in the same change when crate location or membership changes.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
- `docs/project-rustia.md`
