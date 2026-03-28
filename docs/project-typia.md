# Project: typia

## Goal
Provide serde-based LLM JSON parsing and function-calling tool adapter utilities for Rust with three crates: runtime (`typia`), derive proc-macro companion (`typia-macros`), and aisdk adapter (`typia-llm`).

## Project ID
`typia`

## Domain Ownership Map
- `crates/typia` (`core`)
- `crates/typia-llm` (`llm`)
- `crates/typia-macros` (`macros`)

## Domain Contract Documents
- `docs/crates-typia-core-foundation.md`
- `docs/crates-typia-llm-foundation.md`
- `docs/crates-typia-macros-foundation.md`

## Cross-Domain Invariants
- Component identifiers remain stable: `core`, `llm`, `macros`.
- Runtime and macro boundaries remain explicitly separated across crates.
- Runtime, adapter, and macro crates remain publishable (`publish = true`) and are eligible for workspace-managed `cargo-mono publish`.
- Stable public API contract identifiers:
  - Runtime: `Validate`, `IValidation`, `IValidationError`, `LLMData`, `LlmJsonParseResult`, `LlmJsonParseError`
  - Adapter: `LlmToolInput`, `LlmToolOutput`, `LlmToolSpec`, `tool`, `LlmToolBuildError`, `LlmToolInputError`, `LlmToolExecutionError`
  - Macro: `#[derive(LLMData)]`
- `LLMData::parse` performs parse-only coercion of stringified non-string JSON
  values (object/array/number/boolean/null) before returning validation
  failures, while direct `Validate::validate` / `validate_equals` calls remain
  strict.
- `typia-llm::tool` must parse aisdk tool inputs via `LLMData::parse` before handler execution and return deterministic validation feedback on failure.
- `LLMData` derive expansion must remain compatible with runtime trait bounds and helper types from `crates/typia`.
- Lenient parse parity baseline is pinned to
  `samchon/typia@29a02742661d476ce5ef5414fe32acc7e97c0e6c` parse tests.
- Parity exclusions remain explicit and stable:
  `undefined`, non-finite numbers (`Infinity`, `-Infinity`), and lone-surrogate expectations.
- Release tag eligibility remains explicit through root workspace metadata:
  `[workspace.metadata.cargo-mono.publish.tag].packages` must include `typia`, `typia-llm`, and `typia-macros`.

## Change Policy
- Update this index and related crate contract docs together when runtime parsing semantics, adapter contracts, or derive expansion contracts change.
- Keep root and crate-domain `AGENTS.md` ownership mappings synchronized with this index when typia component paths or stability policies change.
- Update root `Cargo.toml` publish-tag package configuration in the same change when typia package release eligibility changes.
- When upstream parity baseline commit changes, update this index, runtime contract docs, and parity tests together.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
