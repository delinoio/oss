# Runmoor

## Goal

Runmoor manages disposable, single-job GitHub Actions runners on one developer or small-team computer. Linux execution uses local Docker; macOS execution uses operator-installed Tart or explicitly selected same-account host processes. Releases use the stable channel, while live GitHub compatibility certification, throughput guarantees, and a support SLA are not provided. Issue [#893](https://github.com/delinoio/oss/issues/893) defines the original product scope; issue [#1312](https://github.com/delinoio/oss/issues/1312) adds opt-in macOS host execution with automated acceptance.

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

- Runmoor public guides are owned directly by `apps/public-docs/docs/runmoor` and published under `/runmoor`. Stable routes are `/runmoor/`, `/runmoor/install`, `/runmoor/configuration`, `/runmoor/commands`, `/runmoor/docker`, `/runmoor/tart`, `/runmoor/host`, and `/runmoor/operations`.
- Documentation validation preserves the credential and private-path publication safeguards previously applied by public-docs, while retaining public configuration placeholders and user-facing storage guidance. The operations guide also documents the installed service configuration-path requirement and mismatch recovery. See `docs/apps-runmoor-docs-foundation.md`.
- The shared site selector is present on every Runmoor page. After the consolidated publication is verified, operators decommission the former standalone hosting and DNS configuration without adding redirects.
- `pnpm --filter public-docs dev` serves the Runmoor section on the consolidated loopback server at `127.0.0.1:46302` and does not require the DevHud team environment.
- Platforms: macOS 14+ Apple Silicon; Ubuntu 22.04+ amd64/arm64. Host and execution CPU architectures must match. Windows, Intel Macs, emulation, GHES, Kubernetes, cloud/remote Docker, and multi-computer management are excluded.
- The manager runs on the host. Host execution is explicit, macOS 14+ arm64-only and restricted to trusted workflows. No reusable completed runners, public webhook server, dashboard, remote control API, telemetry, Prometheus, plugin API, or Runmoor binary auto-update service exists. Runner images have manager-owned automatic updates. Explicit `reload` may advance an older owned launchd/systemd manager to the already installed CLI while preserving independent executions; it never downloads Runmoor releases, downgrades a manager or starts a stopped service. This service reload behavior is unreleased.
- Linux systemd service actions preserve only caller-supplied `XDG_RUNTIME_DIR` and `DBUS_SESSION_BUS_ADDRESS` for `systemctl --user`; unrelated subprocesses keep their minimal environment.
- After an interrupted service replacement completes Stop, one explicit `service start` resumes the verified inactive target once the reload initiator is confirmed finished and cleanup is complete. Retire exact private recovery intent before spawning under exclusive service/state ownership; preserve uncertain ownership, legacy intent, reservations and pauses. Automatic replacement retains Stop. This recovery remains part of the unreleased service reload workflow.
- Committed completion and cleanup survive stale missing-registration inspection results. Actual ownership mismatches remain quarantined, and capacity is released only after confirmed termination; unresolved cleanup remains durable. Existing quarantines require ownership-verified operator recovery, not automatic upgrade-time reclassification.
- DinD admission reserves runner CPU and combined runner/daemon memory. Daemon CPU remains an independent container limit checked against physical Docker engine capacity. All admission and status surfaces use this cost for new work; existing execution and preparation reservations retain their published values across restart/reload until termination or cleanup confirms release. This policy is unreleased and must not be described as available in 0.2.7; public fields and storage schemas do not change.
- Host startup is bounded by preparation time; idle runners have no job deadline. Assignment or busy-aware removal establishes the durable job timeout, which survives manager restart.
- Paired drained host backups preserve installation, artifact and distribution identities. Copied directory inode identities may be rebound only at exclusive completed-stop or drained-relocation startup after ownership and digest verification; live resources remain protected by strict identity checks.
- Tart VMs are operated on only when the durable Runmoor ownership record matches an owner-only marker inside the current VM directory. Inspection, configuration, boot, guest execution and stop use aliases backed by the already-open verified VM directory, and the per-entity alias used to run a VM stays until termination is confirmed. Persisted detached Tart PIDs are paired with OS-reported process-start identities so a reused PID does not keep a reservation alive for an unrelated process; legacy records without that identity remain conservative while the numeric PID exists. If the canonical VM path disappears while its detached Tart process may still be live, preserve the record and reservation until that process is confirmed exited; retry removal of a stale run alias before discarding the record. Before deletion, the verified directory is atomically moved with no-replace semantics to a stable per-entity name and Tart deletes only that staged name; interrupted staged deletion is recoverable from its embedded identity. Missing or mismatching proof preserves the VM, record and reservation, including during force-stop; legacy or interrupted markerless VMs require a paired marker-bearing backup or reimport under a new identity.
- UUID-v7 identities, TOML schema 1, SQLite schema 4 with ordered atomic v1-to-v2-to-v3-to-v4 migrations, and JSON schema 1 are shared contracts. Credentials are environment/file references, never literal TOML values, SQLite values, or guest management credentials.
- New `init` configurations include the scale-set, execution-platform and native-architecture routing labels. Existing and manually authored pool labels are unchanged; omitted labels stay omitted.
- Local configuration/state/image storage is an explicit exception to the repository R2 default: the product is an offline-capable local controller and must reconcile owned resources without a hosted storage dependency.
- GitHub authentication/access policy remains authoritative. Public repository use requires operator-controlled fork execution. Docker and privileged DinD are not secure boundaries for arbitrary hostile workloads.
- Image mutations run through the manager, which owns the setup VM lifetime and sleep inhibition; initial image preparation can run with no pools or connections.
- Interactive Tart initialization can create a new image from a host-supported Apple IPSW under a temporary manager, wait for operator guest setup, verify Guest Agent readiness after a headless reboot, seal it and publish a new final configuration. Interrupted setup resumes only its journaled owned image; noninteractive and prepared-source routes remain available.
- Requested runner versions may follow latest stable releases; effective execution images remain digest-pinned or immutable sealed revisions. Automatic preparation preserves operator sources, old execution generations and pause/stop decisions. The host accepts stable Tart 2.x.x releases with complete SemVer triplets; Guest Agent remains pinned to 0.14.2. Runmoor never bundles Tart or redistributes macOS/Xcode images. External Tart and Guest Agent version-specific licenses remain separate from Runmoor's license.
- Release identity is `runmoor@v<MAJOR.MINOR.PATCH>`, starting at `0.1.0`. Only darwin-arm64, linux-amd64, and linux-arm64 binary archives are published through the stable release channel, with checksums and Sigstore verification material. Homebrew distributes the same verified darwin-arm64 archive through `delinoio/tap/runmoor`, starting with `0.1.3`, on macOS 14+ Apple Silicon only. It does not install Tart or register a service.
- Publication discovers an existing draft through paginated release listings and pins its numeric ID through the final download, complete signed-asset verification and publication. Published or conflicting releases are never overwritten; an uncertain publication result requires remote inspection before recovery.
- The implementation task explicitly omits live GitHub verification and local real Tart execution. Issue #1312 also excludes actual host execution, unsigned Xcode builds and live GitHub jobs. Automated mocks, local Docker validation, and opt-in Tart tests must not be described as full GitHub/Tart certification.

## Change Policy

Update this index, the command contract, relevant AGENTS files, CLI/config/JSON tests, README and public documentation together when behavior or compatibility changes. Preserve all existing root development entry points, fixed ports, and unrelated CI boundaries. Keep generated `dist` ignored and remove generated directories from the final worktree.

## References

- [Repository defaults](repository-defaults.md).
- [Project template](project-template.md).
- [Issue #893](https://github.com/delinoio/oss/issues/893).
- [Official scale-set client](https://github.com/actions/scaleset/tree/v0.4.0).

## Native Linux packages

Follow `docs/repository-linux-packages-contract.md` for APT/DNF release publication, supported systems, signatures, and recovery. Runmoor uses stable and package installation never registers or starts a service.
