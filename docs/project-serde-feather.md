# Project: serde-feather

## Goal
`serde-feather` provides a size-first serde integration scaffold split into runtime and proc-macro crates.
The initial goal is to reduce final binary size even when it means giving up runtime performance optimizations.

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
- Two-crate project skeleton with explicit runtime/proc-macro boundaries.
- Workspace membership and package metadata contracts.
- Feature contracts for `std` and optional derive integration.
- Documentation contracts for future derive API stabilization.

## Out of Scope
- Actual serde derive implementation logic.
- Runtime performance tuning and benchmark optimization.
- Stable public derive macro identifiers in this phase.
- crates.io publication in this phase (`publish = false` baseline).

## Architecture
- `serde-feather` is the runtime-facing crate.
: It exposes a minimal serde-compatible dependency surface with `serde` default features disabled.
: It keeps default features minimal (`std`) and reserves `derive` as opt-in.
- `serde-feather-macros` is the proc-macro crate.
: It is isolated from runtime code and prepared for future derive expansion.
: It exists as scaffolding only in the current phase and intentionally avoids stabilized macro contracts.
- Cross-crate contract:
: `serde-feather` references `serde-feather-macros` as an optional dependency through feature wiring.
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

Package and feature contract:
- `serde-feather` default features: `["std"]`.
- `serde-feather` feature `std` maps to `serde/std`.
- `serde-feather` feature `derive` maps to optional dependency `serde-feather-macros`.
- `serde-feather-macros` is configured as `proc-macro = true`.
- Public derive macro identifiers are intentionally not stabilized in this phase.

## Storage
- No project-owned persistent storage.
- Uses standard Cargo workspace metadata and lockfile management.

## Security
- Keep proc-macro expansion boundaries explicit and isolated from runtime crate contracts.
- Avoid introducing network access, secret handling, or implicit code generation side effects in scaffolding phase.
- Keep publish disabled until API and release contracts are documented and reviewed.

## Logging
- This phase introduces no runtime logging surface because no runtime behavior is implemented.
- Future operational logging requirements for build tooling or commands must use structured logging (`tracing`) and be documented before implementation.

## Build and Test
Planned validation commands for the scaffolding phase:
- `cargo metadata`
- `cargo check -p serde-feather`
- `cargo check -p serde-feather-macros`
- `cargo test`

## Roadmap
- Phase 1: Document-first skeleton with workspace wiring and minimal feature contracts.
- Phase 2: Introduce derive macro design and stabilization plan.
- Phase 3: Implement derive behavior and compatibility tests.
- Phase 4: Evaluate publish readiness and lift `publish = false` when contracts are stable.

## Open Questions
- Which public derive macro identifiers should be stabilized first.
- Whether no-std + alloc support should be introduced after std-first baseline.
- Which generated-code policies best balance binary size and developer ergonomics.

## References
- `docs/project-template.md`
- `AGENTS.md`
- `crates/AGENTS.md`
