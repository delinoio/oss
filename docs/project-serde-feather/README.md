# Project: serde-feather

## Documentation Layout
- Canonical entrypoint for this project: docs/project-serde-feather/README.md
- Keep this directory flat (no nested directories).
- Add feature contracts as feature-<kebab-id>.md files.

## Goal
`serde-feather` provides a size-first serde integration stack split into runtime and proc-macro crates.
The current goal is to provide stable Phase 4 derive coverage while keeping default binary footprint small.


## Path
- `crates/serde-feather`
- `crates/serde-feather-macros`


## Runtime and Language
- Rust library crate (`serde-feather`)
- Rust proc-macro crate (`serde-feather-macros`)


## Users
- Rust library maintainers who prioritize binary-size footprint
- Application developers who want opt-in derive support with minimal default surface area
- Release operators preparing a future publishable serde-feather stack


## In Scope
- Two-crate architecture with explicit runtime/proc-macro boundaries.
- Stable derive macros: `FeatherSerialize` and `FeatherDeserialize`.
- Feature contracts for `std` and optional derive integration.
- Derive support for:
  - Structs with unit, tuple, and named fields.
  - Enums with unit, newtype, tuple, and named variants.
  - Generic type/lifetime/const parameter shapes.
- Supported `serde(...)` attribute subset:
  - Container: `rename`, `rename_all`
  - Field (struct + enum variant fields): `rename`, `default`, `skip`, `skip_serializing`, `skip_deserializing`, `skip_serializing_if`, `with`
  - Enum variant: `rename`, `alias`, `rename_all`


## Out of Scope
- `serde(...)` container attributes outside `rename` and `rename_all` (for example `tag`, `content`, `untagged`).
- `serde(...)` variant attributes outside `rename`, `alias`, and `rename_all` (for example `skip_*`, `untagged`).
- `serde(...)` field attributes outside the documented subset (for example `flatten`).
- Tuple-field `rename` support.
- `rename_all_fields` support.
- no-std derive support in this phase.
- Runtime performance tuning and benchmark optimization.
- crates.io publication in this phase (`publish = false` baseline).


## Document Index
- [feature-architecture.md](./feature-architecture.md)
- [feature-interfaces.md](./feature-interfaces.md)
- [feature-operations.md](./feature-operations.md)
- [feature-roadmap.md](./feature-roadmap.md)

## Documentation Update Rules
- Keep all project contract files in this directory (flat layout).
- Use feature-<kebab-id>.md naming for new capability contracts.
- Update this index whenever feature files are added or removed.
