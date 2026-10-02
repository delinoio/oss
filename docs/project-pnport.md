# pnport

## Goal
Run PnP-unaware subprocesses against an installed Yarn 4 dependency graph without creating a project node_modules directory. Issue #958 defines the complete staged acceptance boundary: 0.1.0 ships macOS/glibc Linux first, and 0.2.0 adds Windows. No partial preview release is permitted; the original Windows requirements remain in scope.

## Project ID
`Pnport = "pnport"`.

## Domain Ownership Map
- Rust: `crates/pnport`, `crates/pnport-core`, and `crates/pnport-preload`, plus the private fspy source fork. The macOS injection library is built from `fspy_preload_unix`; Linux and Windows retain `pnport-preload`.
- Packages: `packages/pnport`, the private source of the npm launcher and four native optional packages for 0.1.0. Windows adds its two native packages in 0.2.0.
- Apps: `apps/public-docs/docs/pnport`, the consolidated public guides at `https://oss.delino.io/pnport`, explicitly marked unreleased until the four-target 0.1.0 gate passes and publication completes.

## Domain Contract Documents
- [Rust foundation](crates-pnport-foundation.md)
- [fspy source fork](crates-fspy-vendor-contract.md)
- [Complete accepted requirements](crates-pnport-requirements.md)
- [npm distribution](packages-pnport-distribution-contract.md)
- [Public documentation](apps-pnport-docs-foundation.md)

## Cross-Domain Invariants
- Version 0.1.0 targets macOS 13+ and Ubuntu 22.04-equivalent glibc on x64/arm64. Version 0.2.0 adds Windows 10 22H2+ MSVC on x64/arm64 after its full native acceptance. Linux static children are a release gate. Musl hosts and mixed architectures are excluded.
- Use Rust, as explicitly required by #958, instead of the default Go. Retain nightly-2026-01-01 and protected Tauri dependencies. All owned Cargo packages are private.
- Use pnp exactly 0.12.12 and fork the required local fspy crates from 3aac49e31fba6905bb0b3d0e29d7755493241e9c with retained provenance and licensing. No upstream release is required.
- Private local content-addressed cache storage replaces the default remote R2 storage because native filesystem backing must remain offline and user-local. SHA-256 identities, rather than UUIDs, identify immutable content; execution identities use UUID v7.
- No runtime networking, service, account, telemetry, feature flags, installer lifecycle scripts, automatic updates, or public Rust/JavaScript API.
- Native/npm versions agree; release identity is pnport@v<MAJOR.MINOR.PATCH>. Preserve 0.1.x command and diagnostic compatibility. Native artifacts, installers, Homebrew, npm, conformance and documentation must pass every target in the selected release before publication: four for 0.1.0 and six when Windows is added in 0.2.0.
- The unpublished source version is `0.0.0` in all three pnport Cargo packages, their lock entries, and the npm source. Release Project rejects patch or major bumps from this source; the required first minor bump produces `0.1.0`. It creates the version commit and tag through the common coordinator path. The separate pnport tag workflow requires complete four-host evidence before 0.1.0 publication. CI and release matrices derive from the package-owned target registry; the private source `pnportReleaseReady` gate stays false until full conformance, minimum-OS and benchmark acceptance is reviewed. No pnport release uses crates.io or Cargo registry credentials.
- Unsupported interception fails closed. Never substitute a protected executable, elevate privileges, silently run without virtualization, or claim universal executable compatibility.
- Yarn unplugged installation structure retains native sibling lookup and ordinary missing-path errors; traversal into later package-owned dependency namespaces remains virtual and read-only, with genuine physical conflicts rejected without modifying user files.
- The complete requirements remain normative even when a development build has incomplete platform capabilities. Readiness must describe evidence truthfully and block publication until every gate passes.

The current source provides a tested development foundation, configured distribution tooling, and public guides. Shared direct-lookup classification now also drives virtual dependency entries in macOS and Linux parent enumeration, including nested issuers and peer contexts; per-stream/shared-offset lifecycle handling does not replace the outstanding platform and tool acceptance gates. Its macOS preload synchronizes concurrent fork state, propagates injection through virtual-PATH `posix_spawnp` launches, and makes pathname and descriptor-relative link reads share logical target and native buffer semantics. Its macOS supervisor uses an authenticated process-group guardian for same-group signal forwarding and abrupt-supervisor recovery, hands off the caller's controlling terminal for ordinary foreground/background stop and resume, and retains detached-tree ownership and broader terminal job-control acceptance as release gates. Its Linux owned-child syscall backend runs dynamic and static ELF children; Ubuntu 22.04 arm64 Docker execution passes native C, static Go, and official TypeScript inline/split fixtures. The arm64 host's amd64 container emulation cannot perform the required child tracing and never substitutes for native x64 CI acceptance. [The Rust evidence section](crates-pnport-foundation.md#current-implementation-evidence-and-remaining-gates) records the remaining runtime acceptance work. The separate exact-tag workflow validates four-host candidates, while its reviewed publication gate stays closed until the full conformance, minimum-OS and benchmark requirements are accepted; passing candidate fixtures or preparing a version commit or tag does not authorize publication. No release has been published and 0.1.0 is not yet releasable.

## Change Policy
Update this index, the relevant domain contracts and AGENTS files together. Record implemented behavior and outstanding release gates separately; preserve deferred Windows requirements and keep #958 open until the complete scope is accepted.

## References
- [Requirements](crates-pnport-requirements.md)
- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
