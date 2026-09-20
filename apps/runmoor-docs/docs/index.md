# Runmoor

Runmoor is a local manager for disposable GitHub Actions runners. One host can manage multiple repository and organization pools with GitHub App or PAT authentication. Each runner executes at most one job.

Linux jobs use an operator-installed local Docker engine. macOS jobs use operator-installed Tart virtual machines. Supported hosts are **macOS 14+ on Apple Silicon** and **Ubuntu 22.04+ on amd64/arm64**. Jobs must match the host CPU architecture. Windows, Intel Macs, emulation, GHES and remote Docker engines are unsupported.

**Verification limits:** automated API/lifecycle tests and local Docker integration are provided. Live GitHub repository/organization and App/PAT combinations and real Tart local execution have not been certified. This is not a promise of GitHub-hosted runner tool inventory, startup speed, throughput, or support response times.

## Guides

- [Install and verify](/install) release archives and signatures.
- [Configure](/configuration) credentials, targets and explicit resource budgets.
- [Commands and routing](/commands) covers pool control and workflow labels.
- [Docker execution](/docker) covers plain and isolated Docker-in-Docker modes.
- [Tart images](/tart) covers manual preparation, sealing and clones.
- [Operations](/operations) covers services, recovery, backup, updates, privacy and troubleshooting.

Runmoor stores ownership and execution metadata locally and has no hosted coordinator or telemetry. Management credentials stay on the host; each disposable runner receives fresh job registration credentials. GitHub remains responsible for workflow permissions and fork policies. A shared Docker kernel and privileged DinD require trusted workloads; a personal computer is not a public hostile-code execution service.
