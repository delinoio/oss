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
  - resolves runtime crate path through `proc-macro-crate` to support renamed `typia` dependencies
- Generated impls must remain compatible with runtime trait contracts defined by `crates/typia`.

## Storage
- No persistent storage contract.
- Macro outputs are compile-time artifacts only.

## Security
- Macro expansion must avoid unsound generated code patterns.
- Diagnostics must stay local to macro invocation sites.

## Logging
- Proc-macro diagnostics should remain concise and deterministic.

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
