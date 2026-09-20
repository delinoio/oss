# Runmoor

## Goal

Runmoor manages disposable, single-job GitHub Actions runners on one developer or small-team computer. Linux execution uses local Docker; macOS execution uses operator-installed Tart. Releases use the stable channel, while live GitHub compatibility certification, throughput guarantees, and a support SLA are not provided. Issue [#893](https://github.com/delinoio/oss/issues/893) defines the complete product scope.

## Project ID

`runmoor` (`ProjectId.Runmoor`).

## Domain Ownership Map

- Commands: `cmds/runmoor`, using the repository Go module.
- Public documentation: `apps/runmoor-docs` at `https://runmoor.delino.io`.
- Release integration: `.github/workflows/release-runmoor.yml` and Runmoor-specific assets under `scripts/release`.

## Domain Contract Documents

- [Command foundation](cmds-runmoor-foundation.md).
- [Runmoor documentation app](apps-runmoor-docs-foundation.md).

## Cross-Domain Invariants
- Manual version selection and bot-owned release orchestration follow `docs/repository-workflow-contract.md`; the `Release Project` workflow supports patch, minor, and major increments while preserving this project’s existing distribution channels.

- Runmoor public guides are owned by the standalone Rspress app and deployed as Cloudflare Pages static output. Stable routes are `/`, `/install`, `/configuration`, `/commands`, `/docker`, `/tart`, and `/operations`. The old public-docs `/runmoor` routes are removed without redirects or compatibility pages.
- Standalone documentation validation preserves the credential and private-path publication safeguards previously applied by public-docs, while retaining public configuration placeholders and user-facing storage guidance. See `docs/apps-runmoor-docs-foundation.md`.
- Package-local `pnpm dev` and root `pnpm dev:runmoor-docs` bind to `127.0.0.1:46309`; preview binds to `127.0.0.1:46271`. Both enforce the fixed address and fail on conflicts. The root entry point does not require the DevHud team environment.
- Platforms: macOS 14+ Apple Silicon; Ubuntu 22.04+ amd64/arm64. Host and execution CPU architectures must match. Windows, Intel Macs, emulation, GHES, Kubernetes, cloud/remote Docker, and multi-computer management are excluded.
- The manager runs on the host. No host job execution, reusable completed runners, public webhook server, dashboard, remote control API, telemetry, Prometheus, plugin API, or automatic update service exists.
- Committed completion and cleanup survive stale missing-registration inspection results. Actual ownership mismatches remain quarantined, and capacity is released only after confirmed termination; unresolved cleanup remains durable. Existing quarantines require ownership-verified operator recovery, not automatic upgrade-time reclassification.
- UUID-v7 identities, TOML schema 1, SQLite schema 1, and JSON schema 1 are shared contracts. Credentials are environment/file references, never literal TOML values, SQLite values, or guest management credentials.
- Local configuration/state/image storage is an explicit exception to the repository R2 default: the product is an offline-capable local controller and must reconcile owned resources without a hosted storage dependency.
- GitHub authentication/access policy remains authoritative. Public repository use requires operator-controlled fork execution. Docker and privileged DinD are not secure boundaries for arbitrary hostile workloads.
- Image mutations run through the manager, which owns the setup VM lifetime and sleep inhibition; initial image preparation can run with no pools or connections.
- Images are digest-pinned or immutable sealed revisions. Runmoor never bundles Tart or redistributes macOS/Xcode images. External Tart and Guest Agent version-specific licenses remain separate from Runmoor's license.
- Release identity is `runmoor@v<MAJOR.MINOR.PATCH>`, starting at `0.1.0`. Only darwin-arm64, linux-amd64, and linux-arm64 binary archives are published through the stable release channel, with checksums and Sigstore verification material. No Homebrew packaging is added.
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
