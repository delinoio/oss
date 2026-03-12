# Feature: architecture

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

