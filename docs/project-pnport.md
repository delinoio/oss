# pnport

## Goal
Run PnP-unaware subprocesses against an installed Yarn 4 dependency graph without creating a project node_modules directory. Issue #958 defines the complete 0.1.0 acceptance boundary; no partial preview release is permitted.

## Project ID
`Pnport = "pnport"`.

## Domain Ownership Map
- Rust: `crates/pnport` and `crates/pnport-preload`, the private native CLI and filesystem/runtime implementation.
- Packages: `packages/pnport`, the private source of the npm launcher and six native optional packages.
- Apps: `apps/public-docs/docs/pnport`, the consolidated public guide sources for `https://oss.delino.io/pnport`. They build into the public-docs tree before a pnport distribution and visibly mark 0.1.0 as unreleased.

## Domain Contract Documents
- [Rust foundation](crates-pnport-foundation.md)
- [Complete accepted requirements](crates-pnport-requirements.md)
- [npm distribution](packages-pnport-distribution-contract.md)
- [Public documentation](apps-pnport-docs-foundation.md)

## Cross-Domain Invariants
- Version 0.1.0 targets macOS 13+, Windows 10 22H2+ MSVC, and Ubuntu 22.04-equivalent glibc on x64/arm64. Linux static children are a release gate. Musl hosts and mixed architectures are excluded.
- Use Rust, as explicitly required by #958, instead of the default Go. Retain nightly-2026-01-01 and protected Tauri dependencies. All owned Cargo packages are private.
- Use pnp exactly 0.12.12 and adapt necessary fspy mechanisms from 3aac49e31fba6905bb0b3d0e29d7755493241e9c with retained provenance and licensing. No upstream release is required.
- Private local content-addressed cache storage replaces the default remote R2 storage because native filesystem backing must remain offline and user-local. SHA-256 identities, rather than UUIDs, identify immutable content; execution identities use UUID v7.
- No runtime networking, service, account, telemetry, feature flags, installer lifecycle scripts, automatic updates, or public Rust/JavaScript API.
- Native/npm versions agree; release identity is pnport@v<MAJOR.MINOR.PATCH>. Preserve 0.1.x command and diagnostic compatibility. Native artifacts, installers, Homebrew, npm, conformance and documentation must pass all six targets before publication.
- Unsupported interception fails closed. Never substitute a protected executable, elevate privileges, silently run without virtualization, or claim universal executable compatibility.
- The complete requirements remain normative even when a development build has incomplete platform capabilities. Readiness must describe evidence truthfully and block publication until every gate passes.

The current source provides a tested development foundation and public guides, not a releasable implementation. [The Rust evidence section](crates-pnport-foundation.md#current-implementation-evidence-and-remaining-gates) records the remaining acceptance work. The public route does not establish distribution availability or completion of issue #958.

## Change Policy
Update this index, the relevant domain contracts and AGENTS files together. Record implemented behavior and outstanding release gates separately; do not narrow #958 by omission.

## References
- [Requirements](crates-pnport-requirements.md)
- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
