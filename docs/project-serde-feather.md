# Project: serde-feather

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

## Architecture
- `serde-feather` is the runtime-facing crate.
: It exposes a minimal serde-compatible dependency surface with `serde` default features disabled.
: It keeps default features minimal (`std`) and exposes opt-in derive re-exports behind `derive`.
- `serde-feather-macros` is the proc-macro crate.
: It is isolated from runtime code and implements derive code generation.
: It validates unsupported shapes and attributes with compile-time errors.
- Cross-crate contract:
: `serde-feather` references `serde-feather-macros` as an optional dependency through feature wiring.
: `derive` implies `std` in the runtime crate in this phase.
: Runtime crate provides internal helpers used by derive output to keep generated code compact.
: Runtime and proc-macro concerns remain separated to preserve package boundaries.

## Interfaces
Canonical component identifiers:

```ts
enum SerdeFeatherComponent {
  Core = "core",
  Macros = "macros",
}
```

Canonical feature identifiers:

```ts
enum SerdeFeatherFeature {
  Std = "std",
  Derive = "derive",
}
```

Canonical derive macro identifiers:

```ts
enum SerdeFeatherDeriveMacro {
  FeatherSerialize = "FeatherSerialize",
  FeatherDeserialize = "FeatherDeserialize",
}
```

Package and feature contract:
- `serde-feather` default features: `["std"]`.
- `serde-feather` feature `std` maps to `serde/std`.
- `serde-feather` feature `derive` maps to optional dependency `serde-feather-macros` and requires `std`.
- `serde-feather-macros` is configured as `proc-macro = true`.
- Stable public derive macro identifiers:
  - `FeatherSerialize`
  - `FeatherDeserialize`

Phase 4 derive target and attribute contract:
- Derive target:
  - Structs: unit, tuple, named.
  - Enums: unit, newtype, tuple, named variants.
  - Generic type/lifetime/const parameter forms.
- Attribute namespace: `serde(...)` only.
- `rename_all` scope:
  - Container-level `rename_all` applies to struct named fields and enum variant names.
  - Variant-level `rename_all` applies to named variant field names.
- Enum encoding/decoding uses serde default externally tagged representation.
- Enum deserialization must accept both string variant names and numeric variant discriminants.
- Variant aliases (`alias`) are deserialization-only names.
- Unknown input fields during deserialization must be ignored.
- Struct and named-variant deserialization must support both map and sequence encodings.
- Tuple-struct and tuple-variant deserialization must support sequence encodings.
- Sequence decoding must treat `skip_deserializing` fields as omitted positions (no placeholder element is consumed).
- Unknown enum variants must fail with deterministic `unknown_variant` errors.
- Overlapping `skip`, `skip_serializing`, and `skip_deserializing` combinations must be rejected deterministically.
- Tuple-field `rename` must be rejected with compile-time errors.
- Variant/container unsupported attributes must be rejected with compile-time errors.
- `with` must be honored for both serialization and deserialization hooks.
- `skip_serializing_if` must be honored for both struct fields and enum variant fields.
- Effective wire field names must be unique in both serialization and deserialization field sets.
- Effective wire enum variant names must be unique in serialization and deserialization name sets (including aliases).
- Auto-generated generic bounds must be added only for type parameters used in active serialization/deserialization paths.
- Default wire field names must strip Rust raw identifier prefixes (for example `r#type` -> `type`).
- Unsupported shapes and unsupported `serde(...)` attributes must fail with compile-time errors at attribute/type span.

## Storage
- No project-owned persistent storage.
- Uses standard Cargo workspace metadata and lockfile management.

## Security
- Keep proc-macro expansion boundaries explicit and isolated from runtime crate contracts.
- Avoid introducing network access, secret handling, or implicit code generation side effects.
- Keep publish disabled until API and release contracts are documented and reviewed.

## Logging
- Runtime crate behavior remains derive-focused and introduces no dedicated runtime logging API.
- Future operational logging requirements for build tooling or commands must use structured logging (`tracing`) and be documented before implementation.

## Build and Test
Validation commands for Phase 4 derive phase:
- `cargo check -p serde-feather`
- `cargo check -p serde-feather-macros`
- `cargo check -p serde-feather --features derive`
- `cargo check -p serde-feather --no-default-features`
- `cargo test -p serde-feather --features derive`
- `cargo test`

## Roadmap
- Phase 1: Document-first skeleton with workspace wiring and minimal feature contracts. (completed)
- Phase 2: Derive macro API design and stabilization plan. (completed)
- Phase 3: MVP derive implementation for named structs with compatibility tests. (completed)
- Phase 4: Expand type and attribute coverage (tuple/unit structs, enum tuple/named variants, generic bounds, `rename_all`/`alias`/`with`/`skip_serializing_if`). (completed)
- Phase 5: Evaluate publish readiness and lift `publish = false` when contracts are stable.

## Open Questions
- Whether no-std + alloc derive support should be introduced after std-first behavior.
- Whether `rename_all_fields`, tagging (`tag`/`content`), or `flatten` should be supported in a future phase.
- Whether build-time optimization of generated visitors should be prioritized before publish readiness.

## References
- `docs/project-template.md`
- `AGENTS.md`
- `crates/AGENTS.md`
