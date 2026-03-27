# crates-typia-macros-foundation

## Scope
- Project/component: `typia` macros scaffold contract
- Canonical path: `crates/typia-macros`

## Runtime and Language
- Runtime: Rust proc-macro crate
- Primary language: Rust

## Users and Operators
- Rust developers expecting derive-like ergonomics for typia runtime integration
- Maintainers coordinating core/macro rollout sequencing

## Interfaces and Contracts
- Stable component identifier: `macros`.
- Current scaffold macro identifier `__typia_scaffold` is internal-only and not part of the stable public API contract.
- Derive macro identifiers and expansion contracts are not stabilized at scaffold stage.
- Future generated code must remain compatible with contracts defined by `crates/typia` runtime APIs.

## Storage
- No persistent storage contract.
- Macro expansion artifacts are compile-time outputs only.

## Security
- Macro expansion should avoid generating unsound code patterns.
- Diagnostics should avoid leaking unrelated source context beyond the invoking macro site.

## Logging
- Proc-macro diagnostics should remain concise and deterministic.
- Build tooling around macro workflows should prefer structured logs.

## Build and Test
- Local validation: `cargo test -p typia-macros`
- Workspace baseline: `cargo test --workspace --all-targets`

## Dependencies and Integrations
- Dependency domain: Rust proc-macro ecosystem.
- Integrates with `docs/crates-typia-core-foundation.md` compatibility contracts.

## Change Triggers
- Update `docs/project-typia.md` and this file when macro identifiers or expansion contracts are introduced or changed.
- Keep macro/runtime compatibility updates synchronized with `docs/crates-typia-core-foundation.md`.

## References
- `docs/project-typia.md`
- `docs/crates-typia-core-foundation.md`
- `docs/domain-template.md`
