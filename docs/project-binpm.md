# Project: binpm

## Goal
Provide a Rust-based, Node-free binary package manager for installing command-line tools from GitHub Releases. `binpm` exists to replace dependency-heavy installer flows, especially npm-based global installers that require Node.js even when the delivered artifact is only a native executable.

## Project ID
`binpm`

## Domain Ownership Map
- Planned: `crates/binpm`

## Domain Contract Documents
- `docs/crates-binpm-foundation.md`

## Cross-Domain Invariants
- `binpm` must be implemented as a Rust CLI before runtime behavior is introduced.
- The canonical project path is planned as `crates/binpm`; no runtime skeleton exists until implementation begins.
- The default package source for v1 is GitHub Releases addressed by `github:owner/repo`.
- Versionless installs must resolve to the latest stable GitHub Release, excluding draft and prerelease releases.
- Binary selection must be deterministic and target-aware across operating system, CPU architecture, and libc or ABI environment.
- The asset selection heuristic must remain fully documented in `docs/crates-binpm-foundation.md` before implementation changes alter scoring behavior.
- `~/.binpm` is the canonical home directory for installed binaries, package records, cache entries, and temporary extraction state.
- `binpm` must not require Node.js, npm, pnpm, yarn, or Bun to install native binary tools.
- Installs without upstream checksum or signature material are allowed in v1 only with an explicit warning and locally recorded SHA-256 metadata.

## Change Policy
- Update this index and `docs/crates-binpm-foundation.md` together when CLI shape, target selection, storage layout, security behavior, or heuristic scoring changes.
- Update root `AGENTS.md` and `crates/AGENTS.md` when `binpm` ownership, planned path status, or repository policy boundaries change.
- Create the planned `crates/binpm` path and add it to the Rust workspace in the same change set where runtime implementation begins.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
- `docs/crates-binpm-foundation.md`
