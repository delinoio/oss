# crates-typia-core-foundation

## Scope
- Project/component: `typia` core runtime LLM data contract
- Canonical path: `crates/typia`

## Runtime and Language
- Runtime: Rust library crate
- Primary language: Rust

## Users and Operators
- Rust developers consuming serde-based LLM argument parsing helpers
- Maintainers preserving runtime compatibility for `typia-macros` derive expansion

## Interfaces and Contracts
- Stable component identifier: `core`.
- Stable runtime identifiers:
  - `LLMData` trait
  - `LlmJsonParseResult<T>`
  - `LlmJsonParseError`
- `LLMData` method contract:
  - `parse(input: &str) -> LlmJsonParseResult<Self>`
  - `validate(value: serde_json::Value) -> Result<Self, serde_json::Error>`
  - `stringify(&self) -> Result<String, serde_json::Error>`
- `LLMData::parse` contract:
  - lenient parse first, then serde validation
  - preserves original input on failure
  - includes partial parsed `serde_json::Value` when recoverable
- Internal lenient parser behavior contract:
  - supports markdown code-block extraction, junk-prefix skipping, comments, unquoted keys, trailing commas, partial keyword recovery, and unclosed string/bracket recovery
  - supports unicode escape decoding including surrogate-pair handling
  - enforces depth guard (`MAX_DEPTH = 512`)

## Storage
- No persistent internal storage contract.
- Parsing is in-memory and request-scoped.

## Security
- Parsing path must remain fail-closed at serde validation boundaries.
- Recursive parsing must continue enforcing the depth guard against stack-overflow-style inputs.

## Logging
- Library-level logging remains opt-in and minimal.
- No mandatory runtime logging side effects are introduced by default parsing methods.

## Build and Test
- Local validation: `cargo test -p typia`
- Workspace baseline: `cargo test --workspace --all-targets`

## Dependencies and Integrations
- Serde integration: `serde`, `serde_json`, `serde_path_to_error`.
- Downstream integration: `typia-macros` derive expansion compatibility.

## Change Triggers
- Update `docs/project-typia.md` and this file when runtime trait signatures, parsing error shapes, or lenient parsing semantics change.
- Keep runtime/derive compatibility updates synchronized with `docs/crates-typia-macros-foundation.md`.

## References
- `docs/project-typia.md`
- `docs/crates-typia-macros-foundation.md`
- `docs/domain-template.md`
