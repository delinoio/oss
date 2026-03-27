# crates-typia-core-foundation

## Scope
- Project/component: `typia` core runtime scaffold contract
- Canonical path: `crates/typia`

## Runtime and Language
- Runtime: Rust library crate
- Primary language: Rust

## Users and Operators
- Rust developers integrating type-safe schema validation flows
- Maintainers defining stable runtime boundaries for future macro integrations

## Interfaces and Contracts
- Stable component identifier: `core`.
- The crate is scaffold-stage and does not yet define stabilized public API identifiers.
- Future public APIs must preserve compatibility boundaries for `typia-macros` generated code.
- Any stabilization of public function/type identifiers must update this contract and `docs/project-typia.md` in the same change set.

## Storage
- No persistent internal storage contract.
- Caching contracts for schema or validation artifacts are not yet defined at scaffold stage.

## Security
- Validation behavior should default to fail-closed semantics for malformed inputs when APIs are introduced.
- Runtime contracts should avoid unsafe-by-default parsing or deserialization shortcuts.

## Logging
- Library-level logging should remain minimal and opt-in.
- Supporting tooling should prefer structured `tracing` events for diagnostics.

## Build and Test
- Local validation: `cargo test -p typia`
- Workspace baseline: `cargo test --workspace --all-targets`

## Dependencies and Integrations
- Upstream integration: consumer crates depending on typia runtime APIs.
- Planned downstream integration: `typia-macros` generated code compatibility.

## Change Triggers
- Update `docs/project-typia.md` and this file for public API, feature-flag, or compatibility-boundary changes.
- Keep derive/runtime compatibility updates synchronized with `docs/crates-typia-macros-foundation.md`.

## References
- `docs/project-typia.md`
- `docs/crates-typia-macros-foundation.md`
- `docs/domain-template.md`
