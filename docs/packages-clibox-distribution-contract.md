# clibox npm distribution

## Scope
`packages/clibox` owns the private source workspace and generates the public `@delino/clibox` launcher and eight platform packages.

## Runtime and Language
The launcher is unbundled CommonJS using Node.js built-ins on Node.js 22+. Build and release tooling is ESM on the repository's Node.js 24 baseline. No frontend bundler is necessary for this native CLI wrapper.

## Users and Operators
JavaScript developers using `pnpm add -D -E @delino/clibox` followed by `pnpm exec clibox`, npm users installing the same package as an exact dev dependency, and release maintainers.

## Interfaces and Contracts
- The installed command is `clibox`; no public JavaScript import API is provided. All target packages carry `run env`, `port which`, `port kill`, `open`, and text `clipboard copy`/`paste` from the Rust command contract without feature flags. Linux desktop tools are runtime capabilities, not npm install scripts or bundled dependencies.
- The source workspace is private and contains no dependency on an unpublished binary package. Public manifests are generated explicitly, never by an install lifecycle hook.
- The main package pins all eight optional dependencies to its exact version. Platform packages declare `os`, `cpu`, and, for Linux, `libc`.
- Linux GNU binaries target the build runner baselines: glibc 2.35 on x64 and 2.39 on arm64. Alpine uses the separate musl builds.
- Both musl targets use the pinned Rust toolchain's `rust-lld` with `-C link-self-contained=yes`, keeping startup objects and libc matched. The Linux CLI has no additional system C-library dependency; X11 inspection uses pure-Rust x11rb and desktop integrations invoke separately installed tools. macOS adapters bind OS frameworks and use libproc with the standard SDK/libclang at build time. Ubuntu's external musl linker is not used; release jobs execute each binary on its native host and exercise npm/pnpm consumers in Alpine.
- Platform suffixes are `darwin-x64`, `darwin-arm64`, `win32-x64-msvc`, `win32-arm64-msvc`, `linux-x64-gnu`, `linux-arm64-gnu`, `linux-x64-musl`, and `linux-arm64-musl`; every name starts with `@delino/clibox-`.
- Resolve OS/architecture from Node and distinguish Linux glibc/musl using the Node diagnostic report header. Do not log the report or its environment contents.
- Resolve only the selected installed dependency, verify its version, and launch its executable without a shell, preserving argv, cwd, environment, stdio, exit code, and termination signals.
- Unsupported platforms, missing dependencies, version drift, and spawn failures report bounded structured stderr diagnostics. Missing dependencies include reinstall guidance with optional dependencies enabled. No PATH, download, or compile fallback is allowed.
- Generated tarballs carry the exact source commit, expected file allowlist, README, and MIT license. Native binaries are version-checked before packing. Unix binaries and the npm bin shim require archive execute bits; Windows PE payloads do not require POSIX execute bits.
- Creation verifies npm pack's original integrity, then finalizes the executable archive header to mode `0755` and records the finalized tarball's integrity. This preserves executable modes even on NTFS without altering payload bytes. Verification of downloaded artifacts remains read-only and rejects missing Unix execute bits; it never repairs publication/retry inputs. Archive fixtures create Unix and Windows package payloads without relying on host `chmod` support.
- Source manifest validation accepts both LF and Windows CRLF checkouts. Packaging canonicalizes launcher text, README, and license to UTF-8/LF; assembly verifies those exact canonical bytes so Windows-built packages validate on Linux. Native executable bytes are copied unchanged.
- Release verification requires exactly nine expected packages with matching source version, revision, metadata, and computed SHA-512 integrity. Publish platform packages first and confirm each registry integrity before publishing the main package. Existing identical versions are reused; conflicting versions fail without overwriting.

## Storage
Generated packages and tarballs live under ignored `dist` or an explicitly supplied temporary output directory. Never track generated output; remove repository-owned `dist` directories after local verification. The installed runtime adds no settings, cache, or history; the native commands read requested input and write requested output plus temporary files required for atomic publication.

## Security
Consumers need no install scripts, network downloads outside their package manager, or Rust compiler. Release jobs obtain OIDC only after all native builds and package checks succeed. Dry runs and regular CI never publish or receive registry credentials. Publication uses fixed npm registry HTTPS endpoints and never prints tokens or raw process environments.

## Logging
Packaging and publication report structured events containing action, package, target, version, revision, integrity, and outcome. Launcher diagnostics contain stable error codes and actionable messages, not raw argv or environment values.

## Build and Test
- `pnpm --filter @delino/clibox test` runs deterministic launcher and packaging/release fixtures.
- `pnpm --filter @delino/clibox test:package` builds the host CLI, creates tarballs, and installs them in temporary npm and pnpm consumers with scripts disabled. Installed-launcher checks exercise environment execution, empty argv and delegated status, all seven issue #917 commands, binary bytes, timezone output, and checksum verification as well as help/version.
- Package-local Turbo tasks include external Cargo/source inputs and disable caching for native packaging/integration checks.
- CI's Linux/macOS/Windows `node-clibox-test` runs `cargo test --locked -p clibox` for unit/process/adapter coverage and participates in the shared change planner and `CI Result` aggregation. Release CI builds, runs Rust command/process/adapter tests, and smoke-tests all eight targets; Linux musl execution is also checked in Alpine.
- Fixtures cover selection, argument and signal forwarding, missing/mismatched dependencies, archive contents/modes, identical package integrity from isolated LF/CRLF source trees, version mismatch, partial publication recovery, conflicting registry integrity, and credential-free dry runs.

## Dependencies and Integrations
`Release Project` synchronizes the Cargo and npm source versions in one version-only commit. `release-clibox.yml` accepts the exact version tag or a manual dry run. Publication requires the exact tag at the selected source commit and a matching crates.io version. The guarded publish job explicitly installs npm `11.6.2` before checking the OIDC minimum of `11.5.1` and publishing; Node.js 24's bundled npm is not the publication version contract.

The `CLIBOX_NPM_PUBLISH_ENABLED` repository variable must be `true` for publication. All nine packages require a Trusted Publisher permitting publication from `delinoio/oss` and `release-clibox.yml`; tagged releases use OIDC and npm provenance. Leaving the variable unset or setting it to `false` limits the workflow to validated CI artifacts and reports publication as disabled. A rerun reconciles already-published identical bytes. Never rebuild or edit an artifact during a partial publication retry; retain the complete verified artifact set.

Public package READMEs describe installation, supported platforms, all seven issue #917 commands, quoting, byte/newline semantics, calendar/DST behavior, bundled timezone updates, checksum path resolution, atomic file replacement, partial stdout, exit codes, and troubleshooting. Publisher configuration, credentials, repository paths, and release internals stay in this document and the repository workflow contract.

## Change Triggers
Keep the project index, Rust contract, package tests, CI path rules, release coordinator, workflows, and root/package AGENTS rules synchronized.

## References
- [Project index](project-clibox.md)
- [Rust foundation](crates-clibox-foundation.md)
- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
- [npm package metadata](https://docs.npmjs.com/cli/v11/configuring-npm/package-json/)
- [npm trusted publishing prerequisites](https://docs.npmjs.com/cli/v11/commands/npm-trust/#prerequisites)
