# crates-serde-feather-macros-foundation

## Scope
- Project/component: `serde-feather-macros` derive-macro contract
- Canonical path: `crates/serde-feather-macros`

## Runtime and Language
- Runtime: Rust proc-macro crate
- Primary language: Rust

## Users and Operators
- Rust developers using derive helpers for serde-feather
- Maintainers ensuring macro/runtime compatibility

## Interfaces and Contracts
- Stable derive identifiers: `FeatherSerialize`, `FeatherDeserialize`.
- Macro output must remain compatible with `serde-feather` core runtime APIs.
- Generated code should remain deterministic for equivalent input types.

### Project requirements

```ts
enum SerdeFeatherComponent {
  Core = "core",
  Macros = "macros",
}
```

- `Macros` -> `crates/serde-feather-macros`

### Rust component integration

- `crates/serde-feather-macros`: Proc-macro companion crate for serde-feather.

- Keep `serde-feather` as the runtime-facing crate and `serde-feather-macros` as the proc-macro crate.

- Keep stable derive macro identifiers (`FeatherSerialize`, `FeatherDeserialize`) aligned with `project-serde-feather.md` and crate component docs.

- `serde-feather-macros` changes must update `crates-serde-feather-macros-foundation.md` and `project-serde-feather.md`.

## Storage
- No persistent storage contract.
- Macro expansion artifacts are compile-time outputs only.

## Security
- Macro expansion must avoid generating unsound or privilege-escalating code patterns.
- Error messages should avoid exposing unrelated source context.

## Logging
- Proc-macro diagnostics should remain concise and deterministic.
- Build tooling around macro workflows should prefer structured logs.

## Build and Test
- Local validation: `cargo test -p serde-feather-macros`
- Workspace baseline: `cargo test --workspace --all-targets`

## Dependencies and Integrations
- Depends on Rust proc-macro ecosystem.
- Integrates directly with `serde-feather` runtime crate contracts.

## Change Triggers
- Update `docs/project-serde-feather.md` and this file when derive API or expansion contracts change.
- Keep derive/runtime compatibility updates synchronized with `docs/crates-serde-feather-core-foundation.md`.

## References
- `docs/project-serde-feather.md`
- `docs/crates-serde-feather-core-foundation.md`
- `docs/domain-template.md`
