### Instructions for `crates/`

- Follow root `AGENTS.md` and each crate-specific project document.
- Keep repository and domain rules in the appropriate `AGENTS.md` files.
- Write all source and comments in English.
- Prefer enums over free-form strings for stable internal and external contracts.

### Scope in This Domain

- `crates/cargo-mono`: Cargo-based Rust monorepo management CLI.
- `crates/nodeup`: Rust-based Node.js version manager.
- `crates/serde-feather`: Size-first serde runtime-facing core crate.
- `crates/serde-feather-macros`: Proc-macro companion crate for serde-feather.
- `crates/dexdex-main-server`: Rust Connect RPC control-plane server for DexDex.
- `crates/dexdex-worker-server`: Rust execution-plane worker server for DexDex.

### Rust Workspace Rules

- Add new crates as explicit workspace members in root `Cargo.toml`.
- Keep crate naming aligned with project IDs when possible.
- Document CLI behavior contracts in `docs/project-<id>.md` before large implementation changes.
- For new package scaffolding, default `publish = false` until publish contracts are explicitly approved.
- Prefer minimal default features and keep optional capabilities opt-in for size-sensitive crates.
- Keep proc-macro crates and runtime crates separated by explicit crate boundaries.
- Keep DexDex server crates aligned with `docs/project-dexdex.md` contracts for Connect RPC-first flows and normalized worker output boundaries.
- Use `tracing` structured logs for DexDex server operational and business events.

### nodeup-Specific Rules

- Preserve rustup-like shim behavior: symlink strategy plus executable-name dispatch.
- Keep channel and command identifiers stable and documented.
- Record storage and download behavior in project docs whenever changed.

### cargo-mono-Specific Rules

- Keep command identifiers stable and documented in `docs/project-cargo-mono.md`.
- Preserve `cargo mono` subcommand compatibility (`cargo-mono` binary naming contract).
- Ensure release automation (`bump`, `publish`) logs include structured operational context.

### serde-feather-Specific Rules

- Keep `serde-feather` as the runtime-facing crate and `serde-feather-macros` as the proc-macro crate.
- Keep binary-size-first defaults: minimal default features and no convenience dependencies by default.
- Do not stabilize public derive macro identifiers before they are documented in `docs/project-serde-feather.md`.

### dexdex-Specific Rules

- Keep `dexdex-main-server` as the control-plane crate and `dexdex-worker-server` as the execution-plane crate.
- Prioritize Connect RPC contracts for DexDex business flows over platform-specific bindings.
- Keep provider-native agent payload handling inside worker boundaries and expose only normalized session outputs upstream.
- Preserve ordered real commit-chain metadata for SubTask outputs that modify code.

### Testing and Validation

- If Rust code changes in this domain, run `cargo test` from repository root.
- Keep logs sufficient for debugging install, dispatch, and runtime resolution flow.
- Keep CLI logs colorized by default for human operators, with explicit opt-out controls.
