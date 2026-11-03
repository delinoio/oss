# CacheCow npm and native distribution

## Scope

Own `packages/ccow`: distribution tooling and npm package `@delino/ccow`, exposing `ccow`. Native runtime remains owned by `crates/cachecow`.

## Runtime and Language

The native CLI is the Rust `ccow` binary from `crates/cachecow`. The npm launcher requires Node.js 24+; native Cargo users must be able to use the native archive without Node.js. Repository-owned distribution code and metadata use Apache-2.0.

## Users and Operators

Native CLI and npm users; release maintainers.

## Interfaces and Contracts

Publish versioned native archives and checksums through GitHub Releases and installation through `@delino/ccow`. Package dispatch must preserve the native CLI argument, working-directory, stream, and exit-code contracts.

Native targets are macOS 15+ arm64/APFS, Linux x64 with an Ubuntu 22.04-equivalent environment on Btrfs and reflink-enabled XFS, and Windows 11 24H2+ x64/ReFS. These are support targets, not claims of existing release artifacts. The release workflow runs no native acceptance on them. Cargo supports 1.99.1+ within 1.x; Rspack/Rsbuild support 2.3.0+ within 2.x.

Installation exposes the CLI. Shim installation and activation remain explicit user choices. No feature flag or rollout cohorts. Rollback reinstalls an earlier version; incompatible settings/records receive recovery guidance without rewriting. No automatic updater, standalone installer script, or Homebrew in the first release.

Archive naming and package internals are owned by the distribution tooling. The dedicated workflow below owns CacheCow publication; it does not extend the repository's selectable release projects.

## Storage

Artifacts are versioned distribution files. Runtime caches and current-user-private state remain under the command contract. Distribution does not introduce a remote build-cache store.

## Security

Preserve source licenses and notices. Keep release credentials out of source, logs, and artifacts. Installation and shim removal must preserve original tools, unrelated files, caches, and settings under the full requirements.

## Logging

Keep package/release diagnostics actionable and free of secrets. Record source revisions, commands, host/filesystem details, results, and unperformed checks in release/CI records.

## Build and Test

CI and the release workflow run distribution fixtures only. They do not run native acceptance, so they do not verify native archive or npm installation on each OS, actual CoW sharing, process-tree behavior, or storage, time and memory measurements. Cross-builds, fixtures and packaged bytes do not prove native behavior. Preserve existing CI platform rules.

## Dependencies and Integrations

Consumes the Rust CLI and its shared managed lifecycle. Integrates with npm, GitHub Releases, and consolidated public installation/rollback guides. Support is through GitHub Issues with no response-time SLA.

## Change Triggers

Update the project index, complete requirements, affected consumer contracts, and public guides together when interfaces change. Follow the [instruction-update policy](README.md#instruction-update-policy). Keep new validation records in PRs and CI artifacts.

## References

- [Project index](project-cachecow.md)
- [Complete requirements](crates-cachecow-requirements.md)
- [Repository defaults](repository-defaults.md)
- [Workflow](repository-workflow-contract.md)

## Release candidate workflow

`.github/workflows/release-ccow.yml` is a manual, main-only workflow restricted to `delinoio/oss`. Its `dry_run` input defaults to true; dry runs build and accept candidates without publication.
It checks the requested version against `packages/ccow/package.json`. A `build`
matrix compiles each native target on a matching GitHub-hosted runner with the
pinned `rust-toolchain`: `macos-15` for `aarch64-apple-darwin`, `ubuntu-22.04`
for `x86_64-unknown-linux-gnu` (the glibc floor), and `windows-latest` for
`x86_64-pc-windows-msvc`. `build-native.mjs` runs
`cargo build --release --locked -p cachecow --bin ccow --target <triple>` with
`CCOW_VERSION`, `CCOW_BUILD_REVISION` and `CCOW_BUILD_DIRTY`. Each executable
embeds exactly one marker,
`ccow-build-info:{"version":"<v>","revision":"<sha>","target":"<triple>","dirty":false}`.
The candidate job downloads those binaries, and `package.mjs` rejects any binary
whose marker does not match the requested version, exact source revision,
target triple and a clean build. It then packages the exact source revision and
verifies the versioned archives, npm tarballs and checksums. This release-only
macOS build host is not CI allocation. Candidate artifacts
are identified by source SHA. Every job checks out the immutable dispatch SHA.
Validation jobs have read-only contents permission. The shared release project
selector is unchanged. Publication requires a successful candidate job, plus
`dry_run: false`.

The workflow has no native acceptance job, acceptance gate or acceptance
evidence. The owner decided not to run native acceptance in CI. Build and
fixture results do not establish native support.

Normal affected CI runs distribution and release fixtures through the existing
`release-contracts` job and `ci:release-fixtures` task. These checks run on
Linux. PR, main-push and affected manual CI retain their existing macOS
exclusions and have no full manual mode.

## Publication and recovery

The `publish` job uses the protected `ccow-release` environment, restricted to
main with required maintainer approval. Configure the existing
`DELINO_RELEASE_BOT_CLIENT_ID` variable and `DELINO_RELEASE_BOT_PRIVATE_KEY`
secret for the `delino-release-bot` installation on `delinoio/oss`. Its short-lived
repository-scoped token owns tag and GitHub Release writes. `NPM_TOKEN` is an
npm automation credential scoped to the four `@delino/ccow` packages. Only this
job receives production credentials, after it verifies the exact candidate
again. Build and candidate jobs receive no publication credentials. Checkout never
persists authentication. npm configuration references the token through its
standard environment placeholder; no credential enters an artifact or argument.

`scripts/release/ccow-publish.mjs` verifies the clean checkout SHA, requested
version, non-fixture manifests and checksums before contacting publication
services. It creates only
`ccow@v<version>` at that SHA, resolves bounded annotated tags, and never moves
an existing tag. GitHub release discovery and asset discovery are paginated;
duplicate identities, orphan releases, unexpected assets and uncertain reads
fail before writes. Existing release ownership must match the exact source and
candidate checksum digest. Asset operations use the pinned release ID.

A new release starts as a draft. Upload native archives, all four npm tarballs,
`release.json` and `SHA256SUMS`.
Compare existing assets byte-for-byte; upload only absent draft assets, without
clobber or deletion. A public release with missing or conflicting assets fails
closed. Complete asset readback precedes npm publication. Publish the three
native npm packages before the launcher, using version-specific
`ccow-v<version>` dist tags. Existing npm versions must match name, version,
source revision, SHA-512 integrity and downloaded tarball bytes. Never overwrite
or unpublish a version. After every npm package reads back correctly, publish
the draft, then promote npm `latest` for native packages before the launcher.
An older retry never moves `latest` back from a newer stable version. The
monorepo-wide GitHub latest release is unchanged.

GitHub and npm do not provide a shared transaction. A failed run can leave an
exact tag, partial draft, or some version-specific npm packages. Rerun **failed
jobs in the original Actions run** while its candidate artifact remains
retained. This preserves the dispatch SHA and candidate bytes, even if main has
advanced. Readback reconciles writes whose
responses were lost, skips identical immutable content and completes only
missing operations. If artifacts have expired, retain the existing remote
state and investigate recovery from the exact candidate bytes;
do not rebuild under the same version and assume byte identity. Conflicts need
maintainer investigation or a new committed version, never force-pushes or
asset replacement. No release is published as part of local validation.
