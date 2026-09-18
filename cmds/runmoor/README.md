# Runmoor

Runmoor is a **preview** local manager for disposable GitHub Actions runners. One host can manage multiple repository and organization pools with GitHub App or PAT authentication. Each runner executes at most one job.

Linux jobs use an operator-installed local Docker engine. macOS jobs use operator-installed Tart virtual machines. Supported hosts are **macOS 14+ on Apple Silicon** and **Ubuntu 22.04+ on amd64/arm64**. Jobs must match the host CPU architecture. Windows, Intel Macs, emulation, GHES and remote Docker engines are unsupported.

**Verification limits:** automated API/lifecycle tests and local Docker integration are provided. Live GitHub repository/organization and App/PAT combinations and real Tart local execution have not been certified. This is not a promise of GitHub-hosted runner tool inventory, startup speed, throughput, or support response times.

## Install and verify

Download the matching `runmoor-darwin-arm64.tar.gz`, `runmoor-linux-amd64.tar.gz` or `runmoor-linux-arm64.tar.gz` from a [`runmoor@v…` prerelease](https://github.com/delinoio/oss/releases). Download `SHA256SUMS` and the `.sigstore.json` bundles alongside it. A publication dry-run archive is unsigned and is not a public release.

Verify with a separately installed cosign before extraction. For example, for version `0.1.0` on an Apple Silicon Mac:

```sh
cosign verify-blob \
  --bundle runmoor-darwin-arm64.tar.gz.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/delinoio/oss/\.github/workflows/release-runmoor\.yml@refs/(heads/main|tags/runmoor@v0\.1\.0)$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  runmoor-darwin-arm64.tar.gz
cosign verify-blob \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp '^https://github\.com/delinoio/oss/\.github/workflows/release-runmoor\.yml@refs/(heads/main|tags/runmoor@v0\.1\.0)$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  SHA256SUMS
shasum -a 256 runmoor-darwin-arm64.tar.gz
tar -xzf runmoor-darwin-arm64.tar.gz
mkdir -p "$HOME/.local/bin"
install -m 755 runmoor "$HOME/.local/bin/runmoor"
runmoor version
```

Compare the printed hash with the exact archive entry in the verified `SHA256SUMS`. Linux may use `sha256sum` instead. Add your user binary directory to PATH yourself. No Homebrew package, automatic update, system-level service, or bundled Docker/Tart is installed.

## Configure

```sh
runmoor init
runmoor config validate
runmoor doctor
runmoor run
```

`init` writes an annotated skeleton and deliberately leaves image/resource inputs incomplete. Edit it before validation. Existing files are never overwritten. All commands accept `--config PATH`.

Both supported operating systems use:

| Purpose | XDG location | Default |
| --- | --- | --- |
| Configuration | `$XDG_CONFIG_HOME/runmoor/config.toml` | `~/.config/runmoor/config.toml` |
| State | `$XDG_STATE_HOME/runmoor` | `~/.local/state/runmoor` |
| Managed images | `$XDG_DATA_HOME/runmoor` | `~/.local/share/runmoor` |

State/data may be overridden by absolute TOML paths. Configuration and credential files must be owned by your user, regular files, and mode 0600. Directories/socket are private to that user. Very long state paths exceed the Unix socket length limit and are rejected. Storage relocation requires drain, stop, and a complete installation backup; it is not a live reload.

The complete schema v1 example below is a template: replace image digest placeholders with real immutable images and choose budgets for your host and Docker engine. Pre-pull those images with Docker. A local content-addressed `sha256:…` image ID is also accepted for images you build yourself.

```toml
schema_version = 1

[host]
max_runners = 2
cpu = 4
memory_mib = 4096
min_free_disk_mib = 10240

[timeouts]
docker_preparation = "5m"
tart_preparation = "10m"
job = "6h"

[logging]
format = "text"
level = "info"
no_color = false

[[connections]]
name = "project"
target = "https://github.com/OWNER/REPOSITORY"
auth = "pat"
credential = { env = "RUNMOOR_PAT" }

[[pools]]
name = "linux"
connection = "project"
scale_set = "runmoor-linux"
labels = ["runmoor-linux"]
backend = "docker"
mode = "plain"
arch = "arm64"
image = "ghcr.io/actions/actions-runner@sha256:REPLACE_WITH_DIGEST"
runner_version = "2.337.0"
runner_path = "/home/runner"
min_idle = 0
max_runners = 2
resources = { cpu = 1, memory_mib = 1024 }
```

Use `amd64` on an amd64 Ubuntu host. GitHub organization targets omit the repository. Organization pools may set `runner_group`; repository pools cannot. Pool names and target/group/scale-set identities must be unique. Scale-set names and labels control workflow routing; existing sets without this installation's ownership evidence are never adopted.

For an App connection set `auth = "app"`, `client_id`, a positive `installation_id`, and a credential reference to its PEM key. For a file reference use `credential = { file = "/absolute/private/credential" }`. Do not place a PAT or private key in TOML. Service definitions never copy credential values; file references are easier to keep available across login/reboot than environment references.

GitHub App repository registration requires repository Administration read/write and Metadata read; organization registration requires organization Self-hosted runners read/write. Classic PATs require `repo` for repository runners or `admin:org` for organization runners. Fine-grained PATs require the target's documented Administration/Self-hosted runners permissions. Follow the [official permission guide](https://docs.github.com/en/actions/how-tos/manage-runners/use-actions-runner-controller/authenticate-to-the-api). GitHub groups and repository access rules remain authoritative.

Unknown keys/schema versions, literal credentials, invalid limits, incompatible architecture, mutable image tags and impossible minimum-idle allocations are rejected. The default preparation limits are five minutes for Docker and ten for Tart; jobs default to six hours. Duration values must be positive and at most seven days.

## Route workflows and operate pools

```yaml
jobs:
  build:
    runs-on: runmoor-linux
    steps:
      - run: echo "Running on an ephemeral local runner"
```

Use the configured scale-set label, or a matching label array, and applicable GitHub runner-group policy. Runmoor receives demand without a public webhook endpoint. Ordinary NAT networking is sufficient.

```sh
runmoor status
runmoor status --json
runmoor doctor --json
runmoor pause --pool linux
runmoor resume --pool linux
runmoor reload
runmoor drain
runmoor stop
runmoor stop --force
```

Status and doctor JSON use `schema_version: 1`. Errors have stable codes, affected identifiers and a recovery action. English text is usable without color; use `--no-color` or `NO_COLOR` to disable ANSI logs. JSON never uses color.

`pause` stops acquisition/new capacity and preserves running jobs. `drain` additionally waits for jobs/local cleanup. `stop` drains before exiting; `stop --pool NAME` drains that pool while the manager keeps serving other pools. Only explicit `--force` terminates owned work. `resume` revalidates the pool. Pool control commands without `--pool` apply to all pools. Resume a suspended pool after correcting credentials or preparation failures.

Reload validates the entire candidate first. Existing jobs retain their original configuration and timeout. Removed/changed pools drain their previous generation; a new generation with the same GitHub scale-set identity waits until the old one retires. A failed reload leaves the last valid configuration active.

Real demand receives capacity before warm runners. Round-robin allocation shares remaining resources across pools. Minimum idle is best effort; running work is never preempted. CPU/memory reservations include DinD and image setup. Low disk blocks new work without evicting active jobs or sealed images.

## Docker and Docker-in-Docker

The official `ghcr.io/actions/actions-runner` image is intentionally minimal. Compatible images must provide a non-root `runner` user with UID/GID 1001, a POSIX shell and standard utilities, exact runner binaries under `runner_path`, and the Docker CLI for DinD. Pin each image by its immutable digest, pre-pull it locally, and keep the configured `runner_version` equal to the actual runner.

To enable Docker builds, container actions and service containers for a pool:

```toml
mode = "dind"
daemon_image = "docker@sha256:REPLACE_WITH_DIGEST"
daemon_resources = { cpu = 1, memory_mib = 1024 }
```

DinD requires cgroup v2 and a privileged daemon container. Its CPU/memory must fit alongside the runner. Every execution has its own daemon/socket/storage, matching workspace/externals paths and network namespace. The host Docker socket, personal directories, SSH agents and management credentials are never passed to jobs. Remote Docker endpoints are rejected.

Docker and privileged DinD share a kernel; they are not secure isolation for arbitrary hostile workloads. Run trusted developer/team workflows and control external fork execution through GitHub policy. Job containers, daemon, networks and volumes are destroyed after completion/cancellation. Base images remain reusable; use GitHub Actions cache instead of persistent local job/build-cache volumes.

## Prepare macOS images

Install **Tart 2.37.0** on macOS 14+ arm64 yourself. Install **Tart Guest Agent 0.14.2** with RPC enabled in the guest's non-root runner account. Account setup, login, Xcode licensing and tools remain manual. Review the version-specific [Tart license](https://github.com/openai/tart/blob/2.37.0/LICENSE) and [Guest Agent license](https://github.com/openai/tart-guest-agent/blob/v0.14.2/LICENSE), currently FSL-1.1-ALv2, plus the applicable Apple software terms. They are external software, not bundled or relicensed by Runmoor.

```sh
runmoor image create --name xcode --ipsw "$HOME/Downloads/restore.ipsw" --cpu 2 --memory-mib 4096
runmoor image create --name imported --from LOCAL_TART_NAME --cpu 2 --memory-mib 4096
runmoor image create --name imported-oci --from oci://REGISTRY/NAMESPACE/IMAGE:TAG --cpu 2 --memory-mib 4096
runmoor image list
runmoor image open --id IMAGE_UUID
runmoor image seal --id IMAGE_UUID --runner-version 2.337.0 --runner-path /Users/runner/actions-runner
```

Existing local sources must be stopped; `--source-home` selects an external Tart storage directory. Absolute `.tvm` exports and existing sealed revision UUIDs are also accepted by `--from`. OCI references resolve to immutable digests before sealing. Imports never modify the operator's original image.

Use a clean dedicated account, install the exact runner release, enable Guest Agent RPC for that logged-in account, and remove previous runner registration/credentials and nonempty workspaces before sealing. The guest runner account must be non-root. Keep personal credentials, keychains and SSH agents out of the base image. Sealing validates readiness/versions, stops the image, and publishes a local immutable revision.

A Tart pool uses `backend = "tart"`, `mode = "plain"`, `arch = "arm64"`, the sealed UUID as `image`, matching `runner_version` and `runner_path`, and explicit VM resources. Each job boots a new clone; the sealed base is never a job VM. Changing a base requires creating and sealing another revision. `image remove --id IMAGE_UUID` refuses active/referenced images. Close a setup window before removal. Setup and validation share the host budget and maximum **two concurrent Runmoor macOS VMs**.

Runmoor uses private Tart storage with automatic pruning disabled. It does not distribute macOS/Xcode images or retain failed job clones.

## Services, recovery and upgrades

```sh
runmoor service install
runmoor service start
runmoor service stop
runmoor service uninstall
```

These commands manage a launchd or systemd **user** service with the same drain semantics. Install the binary at a persistent location first. Installation does not overwrite an existing service definition. Uninstall preserves data/configuration and refuses to abandon known live executions when the manager cannot be contacted. Run the service in a functioning user session; availability after logout/reboot depends on that OS session, and Runmoor does not change system login policy.

Manager-only restart reconciles SQLite with actual Docker/Tart and GitHub state, resumes verified live work and retries incomplete cleanup. Ambiguous resources are quarantined rather than deleted. Confirmed termination releases resources; unresolved cleanup/ownership records remain durable. Runmoor never automatically reruns a failed GitHub job.

Back up only after `drain` and `stop`. Preserve the complete state and managed-data directories; protect referenced credential files separately. Install the new binary manually and start again. Roll back using a compatible binary and its matching drained state/data backup. Unsupported database versions fail without destructive migration; never reuse an older backup while resources created after that backup are still active.

Jobs retain timeout accounting across restart/sleep. Active work requests OS sleep inhibition; warm idle capacity does not keep the machine awake indefinitely. Failure is a warning and does not change system power settings. Forced sleep, lid closure, shutdown and power loss can still interrupt work.

## Troubleshooting and privacy

- `AUTHENTICATION_FAILED`: correct the referenced credential/permissions, then resume the pool.
- `IMAGE_INVALID` or `RUNNER_VERSION_UNSUPPORTED`: pre-pull a correct digest or seal a compatible image. Runner updates are explicit; GitHub generally requires replacement within 30 days of a new runner release and may require security updates sooner.
- `CAPACITY_EXHAUSTED` or `DISK_LOW`: adjust explicit budgets/free disk, or remove an unused sealed image yourself. Do not delete active execution storage.
- `OWNERSHIP_AMBIGUOUS`: preserve local state and investigate the exact resource. Use a different scale-set name when another installation owns it; restoring ownership requires the original matching backup.
- `CLEANUP_PENDING`: restore Docker/Tart/GitHub connectivity and let reconciliation retry. A stopped manager reports pending cleanup until the next run.
- `SLEEP_INHIBITION_UNAVAILABLE`: check OS utility/session permissions; work continues without a sleep guarantee.
- Repeated preparation failures suspend the affected pool after three attempts. Unrelated healthy pools continue.

Diagnostics are local, sanitized structured metadata bounded by **seven days and 256 MiB**. Completed execution history expires after seven days; unresolved ownership/cleanup remains until reconciliation. Credentials, JIT configuration, workflow secrets and raw job output are excluded. Sealed images remain until explicit deletion. There is no telemetry or Prometheus endpoint.

Report reproducible issues through [GitHub Issues](https://github.com/delinoio/oss/issues). Include version, platform, safe error code and relevant sanitized status. Do not include credentials, JIT data, raw workflow logs, or private VM contents. The public documentation site also provides a Runmoor section with the same supported workflows.
