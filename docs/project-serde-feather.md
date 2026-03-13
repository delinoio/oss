# Project: serde-feather

## Goal
Provide a size-first serialization contract split between runtime core and derive-macro crates.

## Project ID
`serde-feather`

## Domain Ownership Map
- `crates/serde-feather` (`core`)
- `crates/serde-feather-macros` (`macros`)

## Domain Contract Documents
- `docs/crates-serde-feather-core-foundation.md`
- `docs/crates-serde-feather-macros-foundation.md`

## Cross-Domain Invariants
- Component identifiers remain stable: `core`, `macros`.
- Derive macro names remain stable: `FeatherSerialize`, `FeatherDeserialize`.
- Runtime and macro crates must preserve explicit boundary separation.

## Change Policy
- Any derive surface or runtime behavior update must modify this index and the relevant component docs.
- Workspace boundary changes must remain synchronized with crate ownership contracts.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
