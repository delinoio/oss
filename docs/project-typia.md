# Project: typia

## Goal
Provide serde-based LLM JSON parsing utilities for Rust with a runtime crate (`typia`) and a derive proc-macro companion crate (`typia-macros`).

## Project ID
`typia`

## Domain Ownership Map
- `crates/typia` (`core`)
- `crates/typia-macros` (`macros`)

## Domain Contract Documents
- `docs/crates-typia-core-foundation.md`
- `docs/crates-typia-macros-foundation.md`

## Cross-Domain Invariants
- Component identifiers remain stable: `core`, `macros`.
- Runtime and macro boundaries remain explicitly separated across crates.
- Stable public API contract identifiers:
  - Runtime: `Validate`, `IValidation`, `IValidationError`, `LLMData`, `LlmJsonParseResult`, `LlmJsonParseError`
  - Macro: `#[derive(LLMData)]`
- `LLMData::parse` performs parse-only coercion of stringified non-string JSON
  values (object/array/number/boolean/null) before returning validation
  failures, while direct `Validate::validate` / `validate_equals` calls remain
  strict.
- `LLMData` derive expansion must remain compatible with runtime trait bounds and helper types from `crates/typia`.
- Lenient parse parity baseline is pinned to
  `samchon/typia@29a02742661d476ce5ef5414fe32acc7e97c0e6c` parse tests.
- Parity exclusions remain explicit and stable:
  `undefined`, non-finite numbers (`Infinity`, `-Infinity`), and lone-surrogate expectations.

## Change Policy
- Update this index and both crate contract docs together when `LLMData` parsing semantics, trait methods, or derive expansion contracts change.
- Keep root and crate-domain `AGENTS.md` ownership mappings synchronized with this index when typia component paths or stability policies change.
- When upstream parity baseline commit changes, update this index, runtime contract docs, and parity tests together.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
