# crates-typia-macros-foundation

## Scope
- Project/component: `typia` macros derive contract
- Canonical path: `crates/typia-macros`

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
  - supports `#[typia(tags(...))]` on fields with lowerCamelCase typia tag identifiers
  - accepts signed numeric literals for numeric tags (`minimum`, `maximum`, `exclusiveMinimum`, `exclusiveMaximum`, `multipleOf`)
  - supports nested tag groups: `items(tags(...))`, `keys(tags(...))`, `values(tags(...))`
  - validates tag-target compatibility and tag exclusivity at compile time
  - respects serde field-shape attributes used by validator codegen (`rename`, `rename_all`, `default`, `flatten`, `skip`, `skip_deserializing`)
  - accepts additional serde key-value field options without derive parse failures (for example `alias`, `with`, `skip_serializing_if`)
  - resolves runtime crate path through `proc-macro-crate` to support renamed `typia` dependencies
- Generated impls must remain compatible with runtime trait contracts defined by `crates/typia`.
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
- Crate remains publishable (`publish = true`) via `crates/typia-macros/Cargo.toml`.
- Workspace release orchestration is owned by `cargo-mono publish`.
- Publish tag eligibility for this crate is controlled by root
  `[workspace.metadata.cargo-mono.publish.tag].packages`.

## Build and Test
- Local validation: `cargo test -p typia-macros`
- Workspace baseline: `cargo test --workspace --all-targets`

## Dependencies and Integrations
- Dependency domain: Rust proc-macro ecosystem (`syn`, `quote`, `proc-macro2`, `proc-macro-crate`).
- Integrates with `docs/crates-typia-core-foundation.md` compatibility contracts.

## Change Triggers
- Update `docs/project-typia.md` and this file when derive identifiers or expansion constraints change.
- Keep derive/runtime compatibility updates synchronized with `docs/crates-typia-core-foundation.md`.

## References
- `docs/project-typia.md`
- `docs/crates-typia-core-foundation.md`
- `docs/domain-template.md`
