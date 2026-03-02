# Project: serde-feather

## Goal
`serde-feather` provides a size-first serde integration stack split into runtime and proc-macro crates.
The current goal is to provide stable MVP derive support while keeping default binary footprint small.

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
- Stable MVP derive macros: `FeatherSerialize` and `FeatherDeserialize`.
- Feature contracts for `std` and optional derive integration.
- Named-struct-only derive support (non-generic).
- Supported `serde(...)` attribute subset:
  - Container: `rename`
  - Field: `rename`, `default`, `skip`, `skip_serializing`, `skip_deserializing`

## Out of Scope
- Enum, tuple struct, and unit struct derive support.
- Generic type derive support.
- Advanced serde attributes (for example `rename_all`, `skip_serializing_if`).
- no-std derive support in this phase.
- Runtime performance tuning and benchmark optimization.
- crates.io publication in this phase (`publish = false` baseline).

## Architecture
- `serde-feather` is the runtime-facing crate.
: It exposes a minimal serde-compatible dependency surface with `serde` default features disabled.
: It keeps default features minimal (`std`) and exposes opt-in derive re-exports behind `derive`.
- `serde-feather-macros` is the proc-macro crate.
: It is isolated from runtime code and implements MVP derive code generation.
: It validates unsupported shapes and attributes with compile-time errors.
- Cross-crate contract:
: `serde-feather` references `serde-feather-macros` as an optional dependency through feature wiring.
: `derive` implies `std` in the runtime crate for MVP behavior.
: Runtime crate provides non-generic internal helpers used by derive output to reduce repeated monomorphized codegen.
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

MVP derive target and attribute contract:
- Derive target: non-generic structs with named fields only.
- Attribute namespace: `serde(...)` only.
- Unknown input fields during deserialization must be ignored.
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
Validation commands for MVP derive phase:
- `cargo check -p serde-feather`
- `cargo check -p serde-feather-macros`
- `cargo check -p serde-feather --features derive`
- `cargo check -p serde-feather --no-default-features`
- `cargo test`

## Roadmap
- Phase 1: Document-first skeleton with workspace wiring and minimal feature contracts. (completed)
- Phase 2: Derive macro API design and stabilization plan. (completed)
- Phase 3: MVP derive implementation for named structs with compatibility tests. (completed)
- Phase 4: Expand type and attribute coverage (enums, generics, advanced serde attributes).
- Phase 5: Evaluate publish readiness and lift `publish = false` when contracts are stable.

## Open Questions
- Which enum and tuple-struct derive semantics should be prioritized next.
- Whether no-std + alloc derive support should be introduced after std-first MVP.
- Which additional serde attributes should be added without bloating generated code size.

## References
- `docs/project-template.md`
- `AGENTS.md`
- `crates/AGENTS.md`
