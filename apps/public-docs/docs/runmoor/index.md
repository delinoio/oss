# Runmoor

Runmoor is a local manager for disposable GitHub Actions runners. One host can manage multiple repository and organization pools with GitHub App or PAT authentication. Each runner executes at most one job.

Linux jobs use an operator-installed local Docker engine. macOS jobs use operator-installed Tart virtual machines or explicitly selected host processes. Host execution is unreleased; see the [host guide](./host) for its setup and trust limits. Supported hosts are **macOS 14+ on Apple Silicon** and **Ubuntu 22.04+ on amd64/arm64**. Jobs must match the host CPU architecture. Windows, Intel Macs, emulation, GHES and remote Docker engines are unsupported.

**Verification limits:** automated API/lifecycle tests and local Docker integration are provided. Live GitHub repository/organization and App/PAT combinations and real Tart local execution have not been certified. Actual Mac host execution, unsigned Xcode builds and live host jobs have not been validated. This is not a promise of GitHub-hosted runner tool inventory, startup speed, throughput, or support response times.

## Guides

- [Install and verify](./install) release archives and signatures.
- [Configure](./configuration) credentials, targets and automatic or explicit resource budgets.
- [Commands and routing](./commands) covers pool control and workflow labels.
- [Docker execution](./docker) covers plain and isolated Docker-in-Docker modes.
- [Tart images](./tart) covers initial Mac preparation, managed runners, sealing and clones.
- [macOS host runners](./host) covers explicit selection, same-account trust and disposable execution.
- [Operations](./operations) covers services, recovery, backup, updates, privacy and troubleshooting.

Runmoor stores ownership and execution metadata locally and has no hosted coordinator or telemetry. Management credentials stay on the host; each disposable runner receives fresh job registration credentials. GitHub remains responsible for workflow permissions and fork policies. A shared Docker kernel and privileged DinD require trusted workloads; a personal computer is not a public hostile-code execution service.
