# Project: nodeup

## Goal
`nodeup` provides a rustup-like Node.js version management experience in Rust.
The primary goal is stable multi-version Node.js execution with automatic download and executable-name-based dispatch.

## Path
- `crates/nodeup`

## Runtime and Language
- Rust CLI

## Users
- Developers who need multiple Node.js versions on one machine
- CI operators who need deterministic Node.js runtime selection

## In Scope
- Install and manage multiple Node.js versions
- Resolve versions by explicit number and channel aliases
- Dispatch behavior based on executable name (`argv[0]`)
- Symlink-based shim strategy similar to rustup-style toolchains
- Automatic Node.js binary download when requested version is missing

## Out of Scope
- JavaScript package manager features (`npm`, `pnpm`, `yarn`) beyond runtime delegation
- Node package dependency resolution
- Remote execution services

## Architecture
- CLI entrypoint resolves command mode and target version.
- Version resolver normalizes user inputs (exact version, channel aliases).
- Installer/downloader fetches and verifies runtime artifacts.
- Local store manager handles installed versions and cache layout.
- Shim dispatcher handles executable-name-based branch logic.

## Interfaces
Canonical nodeup command identifiers:

```ts
enum NodeupCommand {
  Install = "install",
  Use = "use",
  List = "list",
  Remove = "remove",
  Which = "which",
  Run = "run",
}
```

Canonical channel identifiers:

```ts
enum NodeupChannel {
  Lts = "lts",
  Current = "current",
  Latest = "latest",
}
```

Dispatch contract:
- If invoked as `node`, `npm`, `npx`, or another managed alias, nodeup resolves target Node.js version and forwards execution.
- If invoked as `nodeup`, nodeup performs management commands.

Symlink contract:
- Shims point to one nodeup binary.
- Runtime behavior branches by `argv[0]`.

## Storage
- Install root: managed Node.js runtimes per version.
- Cache root: downloaded archives and metadata.
- Config root: optional defaults (preferred channel/version).
- Exact path conventions will be finalized during implementation and recorded in this document.

## Security
- Validate download integrity before activation.
- Restrict permissions on local install and cache directories.
- Avoid executing unverified artifacts.
- Log provenance metadata for each installed version.

## Logging
Required baseline logs:
- Requested version input and normalized resolution
- Download source, checksum result, and install result
- Dispatch executable name and final runtime path
- Activation/deactivation results

## Build and Test
Planned commands:
- Build: `cargo build -p nodeup`
- Test: `cargo test -p nodeup`
- Workspace validation: `cargo test`

## Roadmap
- Phase 1: Version install/list/use/remove core commands.
- Phase 2: Executable-name dispatch and shim lifecycle.
- Phase 3: Channel update metadata and checksum hardening.
- Phase 4: Cross-platform parity and CI optimization.

## Open Questions
- Final source of Node.js release metadata (official mirror strategy).
- Locking strategy for concurrent installs.
- Policy for global default version fallback.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
