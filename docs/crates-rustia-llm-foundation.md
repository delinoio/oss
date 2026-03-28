# crates-rustia-llm-foundation

## Scope
- Project/component: `rustia` LLM tool adapter contract
- Canonical path: `crates/rustia-llm`

## Runtime and Language
- Runtime: Rust library crate
- Primary language: Rust

## Users and Operators
- Rust developers integrating `rustia` typed validation with `aisdk` function calling/tool execution.
- Maintainers preserving compatibility between `rustia`, `rustia-macros`, and `rustia-llm` public contracts.

## Interfaces and Contracts
- Stable component identifier: `llm`.
- Stable public identifiers:
  - `LlmToolInput`
  - `LlmToolOutput`
  - `LlmToolSpec`
  - `tool`
  - `LlmToolBuildError`
  - `LlmToolInputError`
  - `LlmToolExecutionError`
- `LlmToolInput` trait bound contract:
  - `LlmToolInput: rustia::LLMData + schemars::JsonSchema + Send + Sync + 'static`
- `LlmToolOutput` trait bound contract:
  - `LlmToolOutput: serde::Serialize + Send + Sync + 'static`
- `tool` function contract:
  - `tool<I, O, F, E>(spec, handler) -> Result<aisdk::core::tools::Tool, LlmToolBuildError>`
  - `F: Fn(I) -> Result<O, E> + Send + Sync + 'static`, `E: Display`
- Input execution contract:
  - aisdk-provided `serde_json::Value` input is converted to string and parsed through `I::parse`
  - rustia parse/parse-coercion/validation feedback harness is always applied before handler execution
  - validation failure returns deterministic error feedback text including rustia paths (`$input...`) and expected constraints
- Output execution contract:
  - handler success payload is serialized as JSON string via `serde_json::to_string`
  - handler and serialization failures are returned as tool errors

## Storage
- No persistent storage contract.
- All parsing/validation/execution state remains request-scoped in memory.

## Security
- Tool input validation must remain fail-closed before typed handler execution.
- Input coercion must remain delegated to rustia runtime semantics; no bypass path may skip `LLMData::parse`.
- Error payloads must avoid leaking hidden internal state beyond tool input/value-level diagnostics.

## Logging
- Tool parse success/failure and execution outcomes must emit structured logs using `tracing`.
- Baseline log fields must include `tool_name`, parse success/failure, and error count.

## Build and Test
- Local validation: `cargo test -p rustia-llm`
- Workspace baseline: `cargo test --workspace --all-targets`

## Dependencies and Integrations
- Runtime dependencies: `rustia`, `aisdk`, `schemars`, `serde`, `serde_json`, `tracing`.
- Upstream contract dependency: `docs/crates-rustia-core-foundation.md`.
- Project-level contract dependency: `docs/project-rustia.md`.

## Change Triggers
- Update `docs/project-rustia.md` and this file when adapter API signatures, handler contracts, or error payload contracts change.
- Keep rustia cross-component compatibility updates synchronized with `docs/crates-rustia-core-foundation.md` and `docs/crates-rustia-macros-foundation.md`.
- Update root `Cargo.toml` workspace membership and publish-tag package list in the same change when release eligibility changes.

## References
- `docs/project-rustia.md`
- `docs/crates-rustia-core-foundation.md`
- `docs/crates-rustia-macros-foundation.md`
- `docs/domain-template.md`
