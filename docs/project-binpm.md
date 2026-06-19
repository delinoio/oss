# Project: binpm

## Goal
Provide a Rust-based, Node-free binary package manager for installing and running command-line tools from GitHub Releases. `binpm` exists to replace dependency-heavy installer flows, especially npm-based global installers and `npx`-style execution paths that require Node.js even when the delivered artifact is only a native executable.

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
- `~/.binpm` remains the canonical global home directory for globally installed binaries, package records, cache entries, and temporary extraction state.
- `~/.binpm/cache` is the user-level global asset cache shared by all `binpm` installs for the same account.
- Global cache reuse must never bypass GitHub asset digest, upstream checksum, signature, or locally recorded SHA-256 verification.
- Cache management commands must preserve installed package records and `~/.binpm/bin` entries unless a separate uninstall contract explicitly changes that behavior.
- Project-local tooling must use `binpm.toml` at the repository root as the committed local tool manifest.
- Project-local tooling must use `binpm.lock` at the repository root as the committed deterministic resolution record for release tags, target-specific assets, selected binaries, checksums, and installed paths.
- Project-local executable files must be installed under `$repoRoot/.binpm/bin`; other project-local binpm runtime state must stay under `$repoRoot/.binpm`.
- `binpm x CMD [args...]` must run commands from the local manifest or from an explicitly supplied `--package`; it must not guess a GitHub repository from `CMD`.
- `binpm` must not require Node.js, npm, pnpm, yarn, or Bun to install native binary tools.
- Installs without upstream checksum or signature material are allowed in v1 only with an explicit warning and locally recorded SHA-256 metadata.

## Change Policy
- Update this index and `docs/crates-binpm-foundation.md` together when CLI shape, local manifest or lockfile format, target selection, storage layout, cache behavior, security behavior, or heuristic scoring changes.
- Update root `AGENTS.md` and `crates/AGENTS.md` when `binpm` ownership, planned path status, or repository policy boundaries change.
- Create the planned `crates/binpm` path and add it to the Rust workspace in the same change set where runtime implementation begins.

## References
- `docs/project-template.md`
- `docs/domain-template.md`
- `docs/README.md`
- `docs/crates-binpm-foundation.md`
