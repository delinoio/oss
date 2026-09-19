# Runmoor

## Goal

Runmoor manages disposable, single-job GitHub Actions runners on one developer or small-team computer. Linux execution uses local Docker; macOS execution uses operator-installed Tart. The initial release is a preview without live GitHub compatibility certification, throughput guarantees, or a support SLA. Issue [#893](https://github.com/delinoio/oss/issues/893) defines the complete product scope.

## Project ID

`runmoor` (`ProjectId.Runmoor`).

## Domain Ownership Map

- Commands: `cmds/runmoor`, using the repository Go module.
- Public documentation: the `/runmoor` section of `apps/public-docs`.
- Release integration: `.github/workflows/release-runmoor.yml` and Runmoor-specific assets under `scripts/release`.

## Domain Contract Documents

- [Command foundation](cmds-runmoor-foundation.md).
- [Public documentation app](apps-public-docs-foundation.md).

## Cross-Domain Invariants

- Platforms: macOS 14+ Apple Silicon; Ubuntu 22.04+ amd64/arm64. Host and execution CPU architectures must match. Windows, Intel Macs, emulation, GHES, Kubernetes, cloud/remote Docker, and multi-computer management are excluded.
- The manager runs on the host. No host job execution, reusable completed runners, public webhook server, dashboard, remote control API, telemetry, Prometheus, plugin API, or automatic update service exists.
- Committed completion and cleanup survive stale missing-registration inspection results. Actual ownership mismatches remain quarantined, and capacity is released only after confirmed termination; unresolved cleanup remains durable. Existing quarantines require ownership-verified operator recovery, not automatic upgrade-time reclassification.
- UUID-v7 identities, TOML schema 1, SQLite schema 1, and JSON schema 1 are shared contracts. Credentials are environment/file references, never literal TOML values, SQLite values, or guest management credentials.
- Local configuration/state/image storage is an explicit exception to the repository R2 default: the product is an offline-capable local controller and must reconcile owned resources without a hosted storage dependency.
- GitHub authentication/access policy remains authoritative. Public repository use requires operator-controlled fork execution. Docker and privileged DinD are not secure boundaries for arbitrary hostile workloads.
- Image mutations run through the manager, which owns the setup VM lifetime and sleep inhibition; initial image preparation can run with no pools or connections.
- Images are digest-pinned or immutable sealed revisions. Runmoor never bundles Tart or redistributes macOS/Xcode images. External Tart and Guest Agent version-specific licenses remain separate from Runmoor's license.
- Release identity is `runmoor@v<MAJOR.MINOR.PATCH>`, starting at `0.1.0`. Only darwin-arm64, linux-amd64, and linux-arm64 binary archives are published, with checksums and Sigstore verification material. Initial releases are prereleases; no Homebrew packaging is added.
- The implementation task explicitly omits live GitHub verification and local real Tart execution. Automated mocks, local Docker validation, and opt-in Tart tests must not be described as full GitHub/Tart certification.

## Change Policy

Update this index, the command contract, relevant AGENTS files, CLI/config/JSON tests, README and public documentation together when behavior or compatibility changes. Preserve all existing root development entry points, fixed ports, and unrelated CI boundaries. Keep generated `dist` ignored and remove generated directories from the final worktree.

## References

- [Repository defaults](repository-defaults.md).
- [Project template](project-template.md).
- [Issue #893](https://github.com/delinoio/oss/issues/893).
- [Official scale-set client](https://github.com/actions/scaleset/tree/v0.4.0).
