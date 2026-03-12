# Project: cargo-mono

## Documentation Layout
- Canonical entrypoint for this project: docs/project-cargo-mono/README.md
- Keep this directory flat (no nested directories).
- Add feature contracts as feature-<kebab-id>.md files.

## Goal
`cargo-mono` provides a Cargo-native command surface (`cargo mono`) for operating Rust workspaces at monorepo scale.
The project focuses on deterministic package selection, safe version orchestration, and publish automation with structured operational logs.


## Path
- `crates/cargo-mono`


## Runtime and Language
- Rust CLI (`cargo` external subcommand)


## Users
- Rust monorepo maintainers
- Release managers handling multi-crate publication
- CI operators automating version bump and publish workflows


## In Scope
- Cargo external subcommand entrypoint via `cargo-mono` binary and `cargo mono ...` invocation.
- Workspace package discovery (`list`) based on `cargo metadata`.
- Git-aware package impact resolution (`changed`) with optional working tree inclusion.
- Version bump orchestration (`bump`) with independent versions and internal dependency requirement updates.
- Optional dependent package patch propagation on bump (`--bump-dependents`).
- Automated release commit and crate-level tag generation.
- Publish orchestration (`publish`) with dependency-aware ordering and retry handling for index propagation lag.
- Human and JSON output modes (`--output human|json`).


## Out of Scope
- Non-Rust workspace management.
- Changelog generation or release-note authoring.
- Automatic remote push of release commits or tags.
- Cross-registry credential management beyond invoking Cargo publish with the configured environment.


## Document Index
- [feature-architecture.md](./feature-architecture.md)
- [feature-interfaces.md](./feature-interfaces.md)
- [feature-operations.md](./feature-operations.md)
- [feature-roadmap.md](./feature-roadmap.md)

## Documentation Update Rules
- Keep all project contract files in this directory (flat layout).
- Use feature-<kebab-id>.md naming for new capability contracts.
- Update this index whenever feature files are added or removed.
