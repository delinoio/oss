### Instructions for `crates/`

- Follow `docs/monorepo.md` and each crate-specific project document.
- Write all source and comments in English.
- Prefer enums over free-form strings for stable internal and external contracts.

### Scope in This Domain

- `crates/cargo-mono`: Cargo-based Rust monorepo management CLI.
- `crates/nodeup`: Rust-based Node.js version manager.

### Rust Workspace Rules

- Add new crates as explicit workspace members in root `Cargo.toml`.
- Keep crate naming aligned with project IDs when possible.
- Document CLI behavior contracts in `docs/project-<id>.md` before large implementation changes.

### nodeup-Specific Rules

- Preserve rustup-like shim behavior: symlink strategy plus executable-name dispatch.
- Keep channel and command identifiers stable and documented.
- Record storage and download behavior in project docs whenever changed.

### cargo-mono-Specific Rules

- Keep command identifiers stable and documented in `docs/project-cargo-mono.md`.
- Preserve `cargo mono` subcommand compatibility (`cargo-mono` binary naming contract).
- Ensure release automation (`bump`, `publish`) logs include structured operational context.

### Testing and Validation

- If Rust code changes in this domain, run `cargo test` from repository root.
- Keep logs sufficient for debugging install, dispatch, and runtime resolution flow.
