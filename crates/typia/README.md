# typia

`typia` is a scaffold-stage Rust project for type-safe JSON schema validation.

## Goal and Current Status

- Goal: provide a stable runtime foundation for type-safe validation and schema workflows.
- Current status: scaffold only; the crate does not expose stabilized public APIs yet.
- API policy: v0 public function/type and macro identifiers are intentionally not frozen at this stage.

## Planned Architecture

- `core`: `crates/typia` (active scaffold)
- `macros`: `crates/typia-macros` (planned; path not created yet)

Future macro-generated code must remain compatible with the core runtime contracts.

## Current Non-Goals

- Freezing public runtime API identifiers before scaffold-stage contracts are finalized.
- Defining stable derive macro names or expansion schemas before macro crate activation.
- Providing production-ready validation semantics before core and macro contracts are documented as active.

## Local Validation

Run from repository root:

```bash
cargo test -p typia
cargo test --workspace --all-targets
```

## Documentation Links

- Project index: [`docs/project-typia.md`](../../docs/project-typia.md)
- Core contract: [`docs/crates-typia-core-foundation.md`](../../docs/crates-typia-core-foundation.md)
- Macros contract: [`docs/crates-typia-macros-foundation.md`](../../docs/crates-typia-macros-foundation.md)
