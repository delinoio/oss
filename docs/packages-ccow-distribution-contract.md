# CacheCow npm and native distribution

## Scope

Own `packages/ccow`: distribution tooling and npm package `@delino/ccow`, exposing `ccow`. Native runtime remains owned by `crates/cachecow`.

## Runtime and Language

The native CLI is the Rust `ccow` binary from `crates/cachecow`. The npm launcher requires Node.js 24+; native Cargo users must be able to use the native archive without Node.js. Repository-owned distribution code and metadata use Apache-2.0.

## Users and Operators

Native CLI and npm users; release maintainers.

## Interfaces and Contracts

Publish versioned native archives and checksums through GitHub Releases and installation through `@delino/ccow`. Package dispatch must preserve the native CLI argument, working-directory, stream, and exit-code contracts.

Native targets are macOS 15+ arm64/APFS, Linux x64 with an Ubuntu 22.04-equivalent environment on Btrfs and reflink-enabled XFS, and Windows 11 24H2+ x64/ReFS. These are acceptance requirements, not claims of existing release artifacts. Cargo supports 1.99.1+ within 1.x; Rspack/Rsbuild support 2.3.0+ within 2.x.

Installation exposes the CLI. Shim installation and activation remain explicit user choices. No feature flag or rollout cohorts. Rollback reinstalls an earlier version; incompatible settings/records receive recovery guidance without rewriting. No automatic updater, standalone installer script, or Homebrew in the first release.

Archive naming and package internals are owned by the distribution tooling. The dedicated workflow below owns CacheCow publication; it does not extend the repository's selectable release projects.

## Storage

Artifacts are versioned distribution files. Runtime caches and current-user-private state remain under the command contract. Distribution does not introduce a remote build-cache store.

## Security

Preserve source licenses and notices. Keep release credentials out of source, logs, and artifacts. Installation and shim removal must preserve original tools, unrelated files, caches, and settings under the full requirements.

## Logging

Keep package/release diagnostics actionable and free of secrets. Record source revisions, commands, host/filesystem details, results, and unperformed checks in release/CI records.

## Build and Test

Validate native archive and npm installation on every supported minimum OS/architecture, Node.js 24+ launcher/frontend use, native Cargo without Node.js, and reinstall-based rollback. Actual CoW and process-tree behavior require native tests from the full requirements. Cross-builds, fixtures, and packaged bytes alone do not pass those gates. Measure physical storage, preparation/build time, and memory with environment details and limitations; no fixed reduction target. Preserve existing CI platform and release gates.

## Dependencies and Integrations

Consumes the Rust CLI and its shared managed lifecycle. Integrates with npm, GitHub Releases, and consolidated public installation/rollback guides. Support is through GitHub Issues with no response-time SLA.

## Change Triggers

Update the project index, complete requirements, affected consumer contracts, and public guides together when interfaces change. Follow the [instruction-update policy](README.md#instruction-update-policy). Keep new validation records in PRs and CI artifacts.

## References

- [Project index](project-cachecow.md)
- [Complete requirements](crates-cachecow-requirements.md)
- [Repository defaults](repository-defaults.md)
- [Workflow](repository-workflow-contract.md)

## Release candidate and native acceptance workflow

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
macOS build host is not CI allocation and does not establish native acceptance. Candidate artifacts
are identified by source SHA. Every job checks out the immutable dispatch SHA.
Validation jobs have read-only contents permission. The shared release project
selector is unchanged. Publication requires successful candidate, native and
gate jobs, plus `dry_run: false`. No build or fixture result substitutes for a
native gate.

The `native` job owns the release platform exception. It uses the protected
`ccow-native-acceptance` environment and four dedicated self-hosted labels:
`ccow-macos15-apfs`, `ccow-ubuntu22-btrfs`, `ccow-ubuntu22-xfs`, and
`ccow-windows24h2-refs`. Runner labels do not prove acceptance. The entrypoint
probes the OS, architecture and filesystem of the disposable checkout volume.
Minimum-version runs require macOS 15 arm64, Ubuntu 22.04 x64, or Windows 11
24H2 build 26100 x64. Windows Server and generic hosted runners do not satisfy
these gates. Linux runs cover Btrfs and XFS separately; actual physical-sharing
checks must prove XFS reflink capability. Hosts supply Cargo 1.99.1, Node.js 24,
Bash (including Windows), PowerShell 7 where required, and disposable native
storage. The workflow never formats a runner volume. On macOS the probe resolves
the checkout directory to its mount point before reading the volume filesystem
type. The Cargo probe runs with `RUSTUP_TOOLCHAIN=1.99.1` and
`RUSTUP_AUTO_INSTALL=0`, so the checkout's toolchain override cannot change the
probed version and a missing 1.99.1 toolchain fails the probe without being
downloaded onto the runner.

The Linux disk-full scenario runs on a non-root runner, because the permission
denial scenarios require a normal user, and a loop mount needs privilege. The
scenario therefore uses two exact commands through `sudo -n`, and no other
privileged operation:

```
sudo -n /usr/bin/mount -o loop,nosuid,nodev <image> <mount>
sudo -n /usr/bin/umount <mount>
```

`<image>` and `<mount>` are fixed driver-owned paths under the realpath of the
workspace argument: `<workspace>/.ccow-acceptance/diskfull/failure-volume.img`
and `<workspace>/.ccow-acceptance/diskfull/failure-volume`. The driver creates
the image with `mkfs.ext4 -E root_owner=<uid>:<gid>`, so the runner user owns the
volume root and needs no further privilege. Before it creates anything, the
scenario checks both commands with `sudo -n -l <command>`, which succeeds only
for an exact, password-less match. If either check fails, the evidence record
reports `replacement-recovery` with the fixed reason
`linux-disk-full-mount-privilege-required`; the driver never copies command
output into evidence. Host operators provision one rule per Linux lane, with the
absolute workspace path substituted. Sudoers requires `\,` for commas in
arguments and `\ ` for spaces, so use a workspace path without whitespace:

```
<runner-user> ALL=(root) NOPASSWD: \
  /usr/bin/mount -o loop\,nosuid\,nodev <workspace>/.ccow-acceptance/diskfull/failure-volume.img <workspace>/.ccow-acceptance/diskfull/failure-volume, \
  /usr/bin/umount <workspace>/.ccow-acceptance/diskfull/failure-volume
```

The `nosuid,nodev` options stop an image that the runner user controls from
introducing setuid binaries or device nodes through the privileged mount. The
mount point is also user-controlled, so grant the rule only on dedicated,
disposable runners. Hosts also supply `e2fsprogs` (`mkfs.ext4`) and the `sudo`
package. macOS and Windows disk-full paths use no sudo.

`scripts/release/ccow-acceptance.mjs native` takes lane, candidate directory,
workspace, evidence output and exact source revision. It verifies non-fixture
artifacts and probes the host before invoking the repository-owned
`ccow-native-driver.mjs` interface. The driver must be committed and must export
`execute({lane, artifacts, workspace, revision})`. Missing drivers or incomplete
scenarios fail closed and retain a blocked record. Provisioning a host, compiling
binaries or creating packages cannot satisfy native acceptance.

The driver must run the complete native scenarios from the command requirements,
including packaged native use without Node.js, npm installation and rollback,
all three minimum tool versions, CoW sharing and write/clean isolation, freshness,
replacement recovery, locking, process-tree ownership, shims, shell activation
and privacy. For Cargo 1.99.1 and Rspack/Rsbuild 2.3.0 it must measure cold,
warm-source, cloned-worktree and changed-worktree runs. Each measurement records
physical allocated bytes, preparation milliseconds, build milliseconds and peak
memory bytes. Logical file sizes and successful clone APIs alone are insufficient.
The driver must document its concrete commands and native measurement methods
in source before the gate can be completed. Arbitrary uploaded evidence or an
external runner-provided script cannot replace this driver.

Evidence artifacts contain the source revision, version, exact candidate checksum
digest, probed environment, fixed command/check identifiers, typed measurements,
unperformed checks and limitations. Project only these safe fields; exclude
raw native output, source contents, user paths, environment values and private
arguments. Failed runs retain a blocked record and unperformed checks without
invented measurements. The final `gate` job requires successful records for all
four lanes against the same candidate bytes and source revision. It uploads an
accepted candidate only after all gates pass; that artifact is not a publication.

Normal affected CI runs distribution and acceptance-schema fixtures through the
existing `release-contracts` job and `ci:release-fixtures` task. These checks run
on Linux and do not establish native acceptance. PR, main-push and affected
manual CI retain their existing macOS exclusions and have no full manual mode.

## Publication and recovery

The `publish` job uses the protected `ccow-release` environment, restricted to
main with required maintainer approval. Configure the existing
`DELINO_RELEASE_BOT_CLIENT_ID` variable and `DELINO_RELEASE_BOT_PRIVATE_KEY`
secret for the `delino-release-bot` installation on `delinoio/oss`. Its short-lived
repository-scoped token owns tag and GitHub Release writes. `NPM_TOKEN` is an
npm automation credential scoped to the four `@delino/ccow` packages. Only this
job receives production credentials, after revalidation of all four native
records. Native runners receive no publication credentials. Checkout never
persists authentication. npm configuration references the token through its
standard environment placeholder; no credential enters an artifact or argument.

`scripts/release/ccow-publish.mjs` verifies the clean checkout SHA, requested
version, non-fixture manifests and checksums, and complete exact-candidate
native acceptance before contacting publication services. It creates only
`ccow@v<version>` at that SHA, resolves bounded annotated tags, and never moves
an existing tag. GitHub release discovery and asset discovery are paginated;
duplicate identities, orphan releases, unexpected assets and uncertain reads
fail before writes. Existing release ownership must match the exact source and
candidate checksum digest. Asset operations use the pinned release ID.

A new release starts as a draft. Upload native archives, all four npm tarballs,
`release.json`, `SHA256SUMS`, and a projected `native-acceptance.json` report.
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
jobs in the original Actions run** while its candidate and evidence artifacts
remain retained. This preserves the dispatch SHA, candidate bytes and native
measurements, even if main has advanced. Readback reconciles writes whose
responses were lost, skips identical immutable content and completes only
missing operations. Do not rerun successful native jobs to replace accepted
measurements during publication recovery. If artifacts have expired, retain the
existing remote state and investigate recovery from the exact accepted bytes;
do not rebuild under the same version and assume byte identity. Conflicts need
maintainer investigation or a new committed version, never force-pushes or
asset replacement. No release is published as part of local validation.
