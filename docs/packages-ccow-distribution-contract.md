# CacheCow npm and native distribution

## Scope

Own `packages/ccow`: planned distribution tooling and npm package `@delino/ccow`, exposing `ccow`. Native runtime remains owned by `cmds/cachecow`.

## Runtime and Language

The native CLI is Go. The npm launcher requires Node.js 24+; native Cargo users must be able to use the native archive without Node.js. Repository-owned distribution code and metadata use Apache-2.0.

## Users and Operators

Native CLI and npm users; release maintainers.

## Interfaces and Contracts

Publish versioned native archives and checksums through GitHub Releases and installation through `@delino/ccow`. Package dispatch must preserve the native CLI argument, working-directory, stream, and exit-code contracts.

Native targets are macOS 15+ arm64/APFS, Linux x64 with an Ubuntu 22.04-equivalent environment on Btrfs and reflink-enabled XFS, and Windows 11 24H2+ x64/ReFS. These are acceptance requirements, not claims of existing release artifacts. Cargo supports 1.99.1+ within 1.x; Rspack/Rsbuild support 2.3.0+ within 2.x.

Installation exposes the CLI. Shim installation and activation remain explicit user choices. No feature flag or rollout cohorts. Rollback reinstalls an earlier version; incompatible settings/records receive recovery guidance without rewriting. No automatic updater, standalone installer script, or Homebrew in the first release.

Archive naming and package internals are owned by the distribution tooling. The candidate workflow below does not extend the repository's selectable release projects or authorize publication.

## Storage

Artifacts are versioned distribution files. Runtime caches and current-user-private state remain under the command contract. Distribution does not introduce a remote build-cache store.

## Security

Preserve source licenses and notices. Keep release credentials out of source, logs, and artifacts. Installation and shim removal must preserve original tools, unrelated files, caches, and settings under the full requirements.

## Logging

Keep package/release diagnostics actionable and free of secrets. Record source revisions, commands, host/filesystem details, results, and unperformed checks in release/CI records.

## Build and Test

Validate native archive and npm installation on every supported minimum OS/architecture, Node.js 24+ launcher/frontend use, native Cargo without Node.js, and reinstall-based rollback. Actual CoW and process-tree behavior require native tests from the full requirements. Cross-builds, fixtures, and packaged bytes alone do not pass those gates. Measure physical storage, preparation/build time, and memory with environment details and limitations; no fixed reduction target. Preserve existing CI platform and release gates.

## Dependencies and Integrations

Consumes the Go CLI and its shared managed lifecycle. Integrates with npm, GitHub Releases, and consolidated public installation/rollback guides. Support is through GitHub Issues with no response-time SLA.

## Change Triggers

Update the project index, complete requirements, affected consumer contracts, and public guides together when interfaces change. Follow the [instruction-update policy](README.md#instruction-update-policy). Keep new validation records in PRs and CI artifacts.

## References

- [Project index](project-cachecow.md)
- [Complete requirements](cmds-cachecow-requirements.md)
- [Repository defaults](repository-defaults.md)
- [Workflow](repository-workflow-contract.md)

## Release candidate and native acceptance workflow

`.github/workflows/release-ccow.yml` is a manual, main-only candidate workflow.
It checks the requested version against `packages/ccow/package.json`, builds
native binaries with `main.version`, packages the exact source revision, and
verifies the versioned archives, npm tarballs and checksums. Candidate artifacts
are identified by source SHA. This workflow has read-only contents permission;
it does not create tags, GitHub Releases or npm publications. The shared release
project selector is unchanged. Publication remains blocked until native
acceptance and the owning publication/recovery implementation are complete.

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
storage. The workflow never formats a runner volume.

`scripts/release/ccow-acceptance.mjs native` takes lane, candidate directory,
workspace, evidence output and exact source revision. It verifies non-fixture
artifacts and probes the host before invoking the repository-owned
`ccow-native-driver.mjs` interface. The driver must be committed and must export
`execute({lane, artifacts, workspace, revision})`. Until that driver is implemented,
the entrypoint fails closed and writes a blocked record. Provisioning a host,
compiling binaries or creating packages cannot satisfy this missing gate.

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
