# Runmoor

## Goal

Runmoor manages disposable, single-job GitHub Actions runners on one developer or small-team computer. Linux execution uses local Docker; macOS execution uses operator-installed Tart. Releases use the stable channel, while live GitHub compatibility certification, throughput guarantees, and a support SLA are not provided. Issue [#893](https://github.com/delinoio/oss/issues/893) defines the complete product scope.

## Project ID

`runmoor` (`ProjectId.Runmoor`).

## Domain Ownership Map

- Commands: `cmds/runmoor`, using the repository Go module.
- Public documentation: `apps/public-docs/docs/runmoor`, published at `https://oss.delino.io/runmoor`.
- Release integration: `.github/workflows/release-runmoor.yml`, `.github/workflows/release-runmoor-homebrew.yml`, the Runmoor Homebrew template and Runmoor-specific assets under `scripts/release`.

## Domain Contract Documents

- [Command foundation](cmds-runmoor-foundation.md).
- [Runmoor public documentation contract](apps-runmoor-docs-foundation.md).

## Cross-Domain Invariants
- Manual version selection and bot-owned release orchestration follow `docs/repository-workflow-contract.md`; the `Release Project` workflow supports patch, minor, and major increments while preserving this project’s existing distribution channels.

- Runmoor public guides are owned directly by `apps/public-docs/docs/runmoor` and published under `/runmoor`. Stable routes are `/runmoor/`, `/runmoor/install`, `/runmoor/configuration`, `/runmoor/commands`, `/runmoor/docker`, `/runmoor/tart`, and `/runmoor/operations`.
- Documentation validation preserves the credential and private-path publication safeguards previously applied by public-docs, while retaining public configuration placeholders and user-facing storage guidance. See `docs/apps-runmoor-docs-foundation.md`.
- The shared site selector is present on every Runmoor page. After the consolidated publication is verified, operators decommission the former standalone hosting and DNS configuration without adding redirects.
- `pnpm --filter public-docs dev` serves the Runmoor section on the consolidated loopback server at `127.0.0.1:46302` and does not require the DevHud team environment.
- Platforms: macOS 14+ Apple Silicon; Ubuntu 22.04+ amd64/arm64. Host and execution CPU architectures must match. Windows, Intel Macs, emulation, GHES, Kubernetes, cloud/remote Docker, and multi-computer management are excluded.
- The manager runs on the host. No host job execution, reusable completed runners, public webhook server, dashboard, remote control API, telemetry, Prometheus, plugin API, or Runmoor binary auto-update service exists. Runner images have manager-owned automatic updates.
- Committed completion and cleanup survive stale missing-registration inspection results. Actual ownership mismatches remain quarantined, and capacity is released only after confirmed termination; unresolved cleanup remains durable. Existing quarantines require ownership-verified operator recovery, not automatic upgrade-time reclassification.
- UUID-v7 identities, TOML schema 1, SQLite schema 2 with an atomic schema-1 migration, and JSON schema 1 are shared contracts. Credentials are environment/file references, never literal TOML values, SQLite values, or guest management credentials.
- New `init` configurations include the scale-set, execution-platform and native-architecture routing labels. Existing and manually authored pool labels are unchanged; omitted labels stay omitted.
- Local configuration/state/image storage is an explicit exception to the repository R2 default: the product is an offline-capable local controller and must reconcile owned resources without a hosted storage dependency.
- GitHub authentication/access policy remains authoritative. Public repository use requires operator-controlled fork execution. Docker and privileged DinD are not secure boundaries for arbitrary hostile workloads.
- Image mutations run through the manager, which owns the setup VM lifetime and sleep inhibition; initial image preparation can run with no pools or connections.
- Interactive Tart initialization can create a new image from a host-supported Apple IPSW under a temporary manager, wait for operator guest setup, verify Guest Agent readiness after a headless reboot, seal it and publish a new final configuration. Interrupted setup resumes only its journaled owned image; noninteractive and prepared-source routes remain available.
- Requested runner versions may follow latest stable releases; effective execution images remain digest-pinned or immutable sealed revisions. Automatic preparation preserves operator sources, old execution generations and pause/stop decisions. Runmoor never bundles Tart or redistributes macOS/Xcode images. External Tart and Guest Agent version-specific licenses remain separate from Runmoor's license.
- Release identity is `runmoor@v<MAJOR.MINOR.PATCH>`, starting at `0.1.0`. Only darwin-arm64, linux-amd64, and linux-arm64 binary archives are published through the stable release channel, with checksums and Sigstore verification material. Homebrew distributes the same verified darwin-arm64 archive through `delinoio/tap/runmoor`, starting with `0.1.3`, on macOS 14+ Apple Silicon only. It does not install Tart or register a service.
- Publication discovers an existing draft through paginated release listings and pins its numeric ID through the final download, complete signed-asset verification and publication. Published or conflicting releases are never overwritten; an uncertain publication result requires remote inspection before recovery.
- The implementation task explicitly omits live GitHub verification and local real Tart execution. Automated mocks, local Docker validation, and opt-in Tart tests must not be described as full GitHub/Tart certification.

## Change Policy

Update this index, the command contract, relevant AGENTS files, CLI/config/JSON tests, README and public documentation together when behavior or compatibility changes. Preserve all existing root development entry points, fixed ports, and unrelated CI boundaries. Keep generated `dist` ignored and remove generated directories from the final worktree.

## References

- [Repository defaults](repository-defaults.md).
- [Project template](project-template.md).
- [Issue #893](https://github.com/delinoio/oss/issues/893).
- [Official scale-set client](https://github.com/actions/scaleset/tree/v0.4.0).

## Native Linux packages

Follow `docs/repository-linux-packages-contract.md` for APT/DNF release publication, supported systems, signatures, and recovery. Runmoor uses stable and package installation never registers or starts a service.
