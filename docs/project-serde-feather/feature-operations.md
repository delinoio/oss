# Feature: operations

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

