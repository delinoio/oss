# Project: nodeup

## Documentation Layout
- Canonical entrypoint for this project: docs/project-nodeup/README.md
- Keep this directory flat (no nested directories).
- Add feature contracts as feature-<kebab-id>.md files.

## Goal
`nodeup` provides a rustup-like Node.js version management experience in Rust.
The primary goal is deterministic multi-version Node.js execution with automatic runtime installation, directory-aware override selection, and executable-name-based dispatch.


## Path
- `crates/nodeup`


## Runtime and Language
- Rust CLI


## Users
- Developers who need multiple Node.js versions on one machine
- CI operators who need deterministic Node.js runtime selection


## In Scope
- Rustup-style hierarchical command surface for Node.js runtime management.
- Toolchain lifecycle management: list, install, uninstall, and local-link runtime directories.
- Runtime selection controls: global default runtime, per-directory overrides, and explicit one-shot execution.
- Runtime-aware introspection commands: active runtime and runtime home discovery.
- Update flows for installed runtimes.
- Shim-aware command delegation for `node`, `npm`, and `npx`.
- Dispatch behavior based on executable name (`argv[0]`) for runtime shims.
- Automatic Node.js binary download and activation when a requested runtime is missing.
- Human and JSON output modes (`--output human|json`) for machine-parseable command output.
- Participation in workspace-wide release automation via `cargo-mono publish` when nodeup versions are publishable.


## Out of Scope
- JavaScript package manager features (`npm`, `pnpm`, `yarn`) beyond runtime delegation
- Node package dependency resolution
- Remote execution services
- Rust-only command families and concepts: `target`, `component`, `doc`, `man`, `set`
- Rust compiler-specific target triples, standard library components, and documentation topics


## Document Index
- [feature-architecture.md](./feature-architecture.md)
- [feature-interfaces.md](./feature-interfaces.md)
- [feature-operations.md](./feature-operations.md)
- [feature-roadmap.md](./feature-roadmap.md)

## Documentation Update Rules
- Keep all project contract files in this directory (flat layout).
- Use feature-<kebab-id>.md naming for new capability contracts.
- Update this index whenever feature files are added or removed.
