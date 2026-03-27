# Project: typia

## Goal
Provide type-safe JSON schema validation foundations for Rust with a core runtime crate and a planned proc-macro companion crate.

## Project ID
`typia`

## Domain Ownership Map
- `crates/typia` (`core`)
- `crates/typia-macros` (`macros`, planned)

## Domain Contract Documents
- `docs/crates-typia-core-foundation.md`
- `docs/crates-typia-macros-foundation.md`

## Cross-Domain Invariants
- Component identifiers remain stable: `core`, `macros`.
- Core runtime and macro code generation must preserve explicit crate boundary separation.
- Public API identifiers remain scaffold-stage and are not stabilized until this index and crate contract docs are updated in the same change.

## Change Policy
- Update this index and both crate contract docs together when component boundaries, public API stability, or compatibility expectations change.
- When `crates/typia-macros` becomes active, update root and crate-domain `AGENTS.md` ownership mappings in the same change set.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
