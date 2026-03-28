# crates-rustia-core-foundation

## Scope
- Project/component: `rustia` core runtime LLM data contract
- Canonical path: `crates/rustia`

## Runtime and Language
- Runtime: Rust library crate
- Primary language: Rust

## Users and Operators
- Rust developers consuming serde-based LLM argument parsing helpers
- Maintainers preserving runtime compatibility for `rustia-macros` derive expansion

## Interfaces and Contracts
- Stable component identifier: `core`.
- Stable runtime identifiers:
  - `Validate` trait
  - `IValidation<T>`
  - `IValidationError`
  - `LLMData` trait
  - `LlmJsonParseResult<T>`
  - `LlmJsonParseError`
- `Validate` method contract:
  - `validate(value: serde_json::Value) -> IValidation<Self>`
  - `validate_equals(value: serde_json::Value) -> IValidation<Self>`
- `IValidation<T>` contract:
  - success discriminator result (`Success` / `Failure`)
  - failure payload preserves original input value and accumulated validation errors
- `IValidationError` contract:
  - required fields: `path`, `expected`, `value`
  - optional field: `description`
- `LLMData` method contract:
  - `parse(input: &str) -> LlmJsonParseResult<Self>`
  - `stringify(&self) -> Result<String, serde_json::Error>`
- `LLMData` trait bound contract:
  - `LLMData: Validate + serde::Serialize + DeserializeOwned + Sized`
- `LLMData::parse` contract:
  - lenient parse first, then serde validation
  - parse-only coercion stage retries serde validation by reparsing string values
    on failing validation paths (`$input...`) with the lenient parser
  - coercion candidates are applied only when string reparsing succeeds without
    parser errors and produces a different JSON value
  - coercion retries are bounded (`MAX_PARSE_COERCION_ROUNDS = 16`)
  - preserves original input on failure
  - includes partial parsed `serde_json::Value` when recoverable
  - incomplete/truncated inputs that can be recovered without parser-token errors
    (for example: unclosed string/object/array, key-only EOF, key-colon EOF)
    must succeed at lenient parse stage
- Internal lenient parser behavior contract:
  - supports markdown code-block extraction, junk-prefix skipping, comments, unquoted keys, trailing commas, partial keyword recovery, and unclosed string/bracket recovery
  - supports unicode escape decoding including surrogate-pair handling
  - enforces depth guard (`MAX_DEPTH = 512`)
  - TS parity baseline for parse behavior is pinned to
    `samchon/rustia@29a02742661d476ce5ef5414fe32acc7e97c0e6c`
    (`tests/test-utils/src/features/llm/parse`)
  - explicit parity exclusions (documented test policy):
    - JS `undefined`-dependent expectations are excluded
    - `Infinity` / `-Infinity` expectations are excluded
    - lone-surrogate code-unit expectations are excluded

## Storage
- No persistent internal storage contract.
- Parsing is in-memory and request-scoped.

## Security
- Parsing path must remain fail-closed at serde validation boundaries.
- Recursive parsing must continue enforcing the depth guard against stack-overflow-style inputs.

## Logging
- Library-level logging remains opt-in and minimal.
- No mandatory runtime logging side effects are introduced by default parsing methods.

## Release and Distribution
- Crate remains publishable (`publish = true`) via `crates/rustia/Cargo.toml`.
- Workspace release orchestration is owned by `cargo-mono publish`.
- Publish tag eligibility for this crate is controlled by root
  `[workspace.metadata.cargo-mono.publish.tag].packages`.

## Build and Test
- Local validation: `cargo test -p rustia`
- Workspace baseline: `cargo test --workspace --all-targets`

## Dependencies and Integrations
- Serde integration: `serde`, `serde_json`, `serde_path_to_error`.
- Downstream integration: `rustia-macros` derive expansion compatibility.
- Downstream integration: `rustia-llm` adapter input parsing via `LLMData::parse`.

## Change Triggers
- Update `docs/project-rustia.md` and this file when runtime trait signatures, parsing error shapes, or lenient parsing semantics change.
- Keep runtime/derive compatibility updates synchronized with `docs/crates-rustia-macros-foundation.md`.
- Keep runtime/adapter compatibility updates synchronized with `docs/crates-rustia-llm-foundation.md`.

## References
- `docs/project-rustia.md`
- `docs/crates-rustia-llm-foundation.md`
- `docs/crates-rustia-macros-foundation.md`
- `docs/domain-template.md`
