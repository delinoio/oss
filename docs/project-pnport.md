# pnport

## Goal
Run PnP-unaware subprocesses against an installed Yarn 4 dependency graph without creating a project node_modules directory. Issue #958 defines the complete 0.1.0 acceptance boundary; no partial preview release is permitted.

## Project ID
`Pnport = "pnport"`.

## Domain Ownership Map
- Rust: `crates/pnport`, `crates/pnport-core`, and `crates/pnport-preload`, plus the private fspy source fork. The macOS injection library is built from `fspy_preload_unix`; Linux and Windows retain `pnport-preload`.
- Packages: `packages/pnport`, the private source of the npm launcher and six native optional packages.
- Apps: `apps/public-docs/docs/pnport`, the consolidated public guides at `https://oss.delino.io/pnport`, explicitly marked unreleased until the six-target gate passes.

## Domain Contract Documents
- [Rust foundation](crates-pnport-foundation.md)
- [fspy source fork](crates-fspy-vendor-contract.md)
- [Complete accepted requirements](crates-pnport-requirements.md)
- [npm distribution](packages-pnport-distribution-contract.md)
- [Public documentation](apps-pnport-docs-foundation.md)

## Cross-Domain Invariants
- Version 0.1.0 targets macOS 13+, Windows 10 22H2+ MSVC, and Ubuntu 22.04-equivalent glibc on x64/arm64. Linux static children are a release gate. Musl hosts and mixed architectures are excluded.
- Use Rust, as explicitly required by #958, instead of the default Go. Retain nightly-2026-01-01 and protected Tauri dependencies. All owned Cargo packages are private.
- Use pnp exactly 0.12.12 and fork the required local fspy crates from 3aac49e31fba6905bb0b3d0e29d7755493241e9c with retained provenance and licensing. No upstream release is required.
- Private local content-addressed cache storage replaces the default remote R2 storage because native filesystem backing must remain offline and user-local. SHA-256 identities, rather than UUIDs, identify immutable content; execution identities use UUID v7.
- No runtime networking, service, account, telemetry, feature flags, installer lifecycle scripts, automatic updates, or public Rust/JavaScript API.
- Native/npm versions agree; release identity is pnport@v<MAJOR.MINOR.PATCH>. Preserve 0.1.x command and diagnostic compatibility. Native artifacts, installers, Homebrew, npm, conformance and documentation must pass all six targets before publication.
- The unpublished source version is `0.0.0` in all three pnport Cargo packages, their lock entries, and the npm source. Release Project rejects patch or major bumps from this source; the required first minor bump produces `0.1.0`. It creates the version commit and tag through the common coordinator path. The separate pnport tag workflow requires complete six-host evidence before publication. No pnport release uses crates.io or Cargo registry credentials.
- Unsupported interception fails closed. Never substitute a protected executable, elevate privileges, silently run without virtualization, or claim universal executable compatibility.
- The complete requirements remain normative even when a development build has incomplete platform capabilities. Readiness must describe evidence truthfully and block publication until every gate passes.

The current source provides a tested development foundation, configured distribution tooling, and public guides. Its Linux owned-child syscall backend runs dynamic and static ELF children; Ubuntu 22.04 arm64 Docker execution passes native C, static Go, and official TypeScript inline/split fixtures. The arm64 host's amd64 container emulation cannot perform the required child tracing, so native x64 evidence remains open. [The Rust evidence section](crates-pnport-foundation.md#current-implementation-evidence-and-remaining-gates) records the remaining runtime acceptance work. The separate exact-tag workflow's six-host gate remains closed until those capabilities pass on every target; a prepared version commit or tag does not authorize publication. No release has been published and 0.1.0 is not yet releasable.

## Change Policy
Update this index, the relevant domain contracts and AGENTS files together. Record implemented behavior and outstanding release gates separately; do not narrow #958 by omission.

## References
- [Requirements](crates-pnport-requirements.md)
- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
