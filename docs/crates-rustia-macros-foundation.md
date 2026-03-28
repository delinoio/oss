# crates-rustia-macros-foundation

## Scope
- Project/component: `rustia` macros derive contract
- Canonical path: `crates/rustia-macros`

## Runtime and Language
- Runtime: Rust proc-macro crate
- Primary language: Rust

## Users and Operators
- Rust developers deriving `LLMData` implementations for domain structs/enums
- Maintainers preserving derive/runtime compatibility boundaries

## Interfaces and Contracts
- Stable component identifier: `macros`.
- Stable derive identifier: `#[derive(LLMData)]`.
- Expansion contract:
  - derives for `struct` and `enum`
  - emits compile-time error for `union`
  - supports `#[rustia(tags(...))]` on fields with lowerCamelCase rustia tag identifiers
  - accepts signed numeric literals for numeric tags (`minimum`, `maximum`, `exclusiveMinimum`, `exclusiveMaximum`, `multipleOf`)
  - supports nested tag groups: `items(tags(...))`, `keys(tags(...))`, `values(tags(...))`
  - validates tag-target compatibility and tag exclusivity at compile time
  - respects serde field-shape attributes used by validator codegen (`rename`, `rename_all`, `default`, `flatten`, `skip`, `skip_deserializing`)
  - accepts additional serde key-value field options without derive parse failures (for example `alias`, `with`, `skip_serializing_if`)
  - resolves runtime crate path through `proc-macro-crate` to support renamed `rustia` dependencies
- Generated impls must remain compatible with runtime trait contracts defined by `crates/rustia`.
- Derived output contract:
  - always emits `impl LLMData`
  - emits `impl Validate`

## Storage
- No persistent storage contract.
- Macro outputs are compile-time artifacts only.

## Security
- Macro expansion must avoid unsound generated code patterns.
- Diagnostics must stay local to macro invocation sites.

## Logging
- Proc-macro diagnostics should remain concise and deterministic.

## Release and Distribution
- Crate remains publishable (`publish = true`) via `crates/rustia-macros/Cargo.toml`.
- Workspace release orchestration is owned by `cargo-mono publish`.
- Publish tag eligibility for this crate is controlled by root
  `[workspace.metadata.cargo-mono.publish.tag].packages`.

## Build and Test
- Local validation: `cargo test -p rustia-macros`
- Workspace baseline: `cargo test --workspace --all-targets`

## Dependencies and Integrations
- Dependency domain: Rust proc-macro ecosystem (`syn`, `quote`, `proc-macro2`, `proc-macro-crate`).
- Integrates with `docs/crates-rustia-core-foundation.md` compatibility contracts.

## Change Triggers
- Update `docs/project-rustia.md` and this file when derive identifiers or expansion constraints change.
- Keep derive/runtime compatibility updates synchronized with `docs/crates-rustia-core-foundation.md`.
- Keep rustia component contract updates synchronized with `docs/crates-rustia-llm-foundation.md` when shared API identifiers or release policy boundaries change.

## References
- `docs/project-rustia.md`
- `docs/crates-rustia-core-foundation.md`
- `docs/crates-rustia-llm-foundation.md`
- `docs/domain-template.md`
