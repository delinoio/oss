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

Archive naming, package internals, and release workflow wiring will be specified with their implementation; this onboarding layer does not extend the repository's selectable release projects or authorize publication.

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
