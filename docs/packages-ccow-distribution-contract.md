# CacheCow native distribution

## Scope

Own `packages/ccow`: the private repository tooling that builds, packages and verifies the versioned native `ccow` archives published through GitHub Releases. CacheCow is not distributed through npm: there is no `@delino/ccow*` package, launcher, trusted publishing or npm credential. Native runtime remains owned by `crates/cachecow`.

## Runtime and Language

The native CLI is the Rust `ccow` binary from `crates/cachecow`. Native archives and native Cargo use require no Node.js. Node.js 24+ is required only by the repository distribution tooling and by the Rspack/Rsbuild discovery hook (`.ccow/discover.cjs`) owned by the command contract. Repository-owned distribution code and metadata use Apache-2.0.

## Users and Operators

Native CLI users installing from GitHub Releases; release maintainers.

## Interfaces and Contracts

Publish versioned native archives, `SHA256SUMS` and `release.json` through GitHub Releases under the tag `ccow@v<version>`. This is the only distribution channel. The archive names are `ccow-<version>-<target>.tar.gz` for targets `darwin-arm64`, `linux-x64` and `windows-x64`. Each archive holds exactly `LICENSE`, `NOTICE`, the executable (`ccow`, or `ccow.exe` on Windows, mode 0755) and an archive-level `release.json` that records the version, source revision, tag, fixture flag, target and executable SHA-256. The release-level `release.json` lists the three archives with their SHA-256 digests, and `SHA256SUMS` lists the same digests. Users install by downloading an archive, verifying it against `SHA256SUMS` and placing the executable on PATH, so the native CLI argument, working-directory, stream, and exit-code contracts need no dispatch layer.

Native targets are macOS 15+ arm64/APFS, Linux x64 with an Ubuntu 22.04-equivalent environment on Btrfs and reflink-enabled XFS, and Windows 11 24H2+ x64/ReFS. These are support targets, not claims of existing release artifacts. The release workflow runs no native acceptance on them. Cargo supports 1.99.1+ within 1.x; Rspack/Rsbuild support 2.3.0+ within 2.x.

Installation exposes the CLI. Shim installation and activation remain explicit user choices. No feature flag or rollout cohorts. Rollback installs an earlier released archive; incompatible settings/records receive recovery guidance without rewriting. No automatic updater, standalone installer script, or Homebrew in the first release.

Archive internals are owned by the distribution tooling. The dedicated workflow below owns CacheCow publication; it does not extend the repository's selectable release projects.

## Storage

Artifacts are versioned distribution files. Runtime caches and current-user-private state remain under the command contract. Distribution does not introduce a remote build-cache store.

## Security

Preserve source licenses and notices. Keep release credentials out of source, logs, and artifacts. Installation and shim removal must preserve original tools, unrelated files, caches, and settings under the full requirements.

## Logging

Keep package/release diagnostics actionable and free of secrets. Record source revisions, commands, host/filesystem details, results, and unperformed checks in release/CI records.

## Build and Test

CI and the release workflow run distribution fixtures only. They do not run native acceptance, so they do not verify native archive installation on each OS, actual CoW sharing, process-tree behavior, or storage, time and memory measurements. Cross-builds, fixtures and packaged bytes do not prove native behavior. Preserve existing CI platform rules.

## Dependencies and Integrations

Consumes the Rust CLI and its shared managed lifecycle. Integrates with GitHub Releases and consolidated public installation/rollback guides. Support is through GitHub Issues with no response-time SLA.

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
verifies the versioned archives and checksums. This release-only
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
repository-scoped token owns tag and GitHub Release writes and is the only
publication credential. Only this job receives it, after it verifies the exact
candidate again. Build and candidate jobs receive no publication credentials.
Checkout never persists authentication. The workflow uses no npm token, registry
configuration or OIDC identity, and no credential enters an artifact or argument.

`scripts/release/ccow-publish.mjs` verifies the clean checkout SHA, requested
version, non-fixture archives and checksums before contacting publication
services. Candidate verification rejects any file besides the three archives,
`release.json` and `SHA256SUMS`. The script creates only
`ccow@v<version>` at that SHA, resolves bounded annotated tags, and never moves
an existing tag. GitHub release discovery and asset discovery are paginated;
duplicate identities, orphan releases, unexpected assets and uncertain reads
fail before writes. Existing release ownership must match the exact source and
candidate checksum digest. Asset operations use the pinned release ID.

A new release starts as a draft. Upload the three native archives,
`release.json` and `SHA256SUMS`.
Compare existing assets byte-for-byte; upload only absent draft assets, without
clobber or deletion. A public release with missing or conflicting assets fails
closed. Complete asset readback of the pinned release precedes publication of
the draft, and the published release and its assets are read back again. The
monorepo-wide GitHub latest release is unchanged.

A failed run can leave an exact tag or a partial draft. Rerun **failed
jobs in the original Actions run** while its candidate artifact remains
retained. This preserves the dispatch SHA and candidate bytes, even if main has
advanced. Readback reconciles writes whose
responses were lost, skips identical immutable content and completes only
missing operations. If artifacts have expired, retain the existing remote
state and investigate recovery from the exact candidate bytes;
do not rebuild under the same version and assume byte identity. Conflicts need
maintainer investigation or a new committed version, never force-pushes or
asset replacement. No release is published as part of local validation.

## Distribution decision

Version 0.1.0 has never been published to npm, so there is no package to
deprecate or unpublish. The owner decided that CacheCow ships only versioned
native archives, `SHA256SUMS` and `release.json` through GitHub Releases. The
`@delino/ccow` launcher and the three platform packages are not built,
published or supported, and `packages/ccow` keeps only the native target table,
archive builder, packaging and verification scripts. A future npm channel needs a
new owner decision and a new contract.
