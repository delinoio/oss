# crates-serde-feather-core-foundation

## Scope
- Project/component: `serde-feather` core runtime contract
- Canonical path: `crates/serde-feather`

## Runtime and Language
- Runtime: Rust library crate
- Primary language: Rust

## Users and Operators
- Rust developers integrating size-first serialization
- Maintainers validating binary-size-sensitive defaults

## Interfaces and Contracts
- Runtime serialization/deserialization primitives define the stable core API.
- Feature flags must remain minimal and opt-in by default.
- Public interfaces must remain compatible with derive output from `serde-feather-macros`.

### Project requirements

`serde-feather` is a two-component project with fixed mapping:

- `Core` -> `crates/serde-feather`

### Rust component integration

- `crates/serde-feather`: Size-first serde runtime-facing core crate.

- `crates/rustia`: Serde-based LLM JSON runtime crate.

- Keep binary-size-first defaults: minimal default features and no convenience dependencies by default.

- `serde-feather` core crate changes must update `crates-serde-feather-core-foundation.md` and `project-serde-feather.md`.

## Storage
- No persistent internal storage contract.
- Runtime buffer handling and serialization formats must remain deterministic.

## Security
- Serialization behavior must avoid unsafe default expansions.
- Input validation and error handling must prevent panics from malformed data where feasible.

## Logging
- Library logging should remain minimal and diagnostic-oriented when enabled.
- Operational tooling around the crate should use structured `tracing` events.

## Build and Test
- Local validation: `cargo test -p serde-feather`
- Workspace baseline: `cargo test --workspace --all-targets`

## Dependencies and Integrations
- Upstream integration: consumer crates using serde-feather runtime APIs.
- Downstream integration: derive output from `serde-feather-macros`.

## Change Triggers
- Update `docs/project-serde-feather.md` and this file for runtime API/feature changes.
- Keep compatibility with `docs/crates-serde-feather-macros-foundation.md` in the same change set.

## References
- `docs/project-serde-feather.md`
- `docs/crates-serde-feather-macros-foundation.md`
- `docs/domain-template.md`
