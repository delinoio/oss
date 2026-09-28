# Runmoor

Runmoor is a local manager for disposable GitHub Actions runners. One host can manage multiple repository and organization pools with GitHub App or PAT authentication. Each runner executes at most one job.

Linux jobs use an operator-installed local Docker engine. macOS jobs use operator-installed Tart virtual machines. Supported hosts are **macOS 14+ on Apple Silicon** and **Ubuntu 22.04+ on amd64/arm64**. Jobs must match the host CPU architecture. Windows, Intel Macs, emulation, GHES and remote Docker engines are unsupported.

**Verification limits:** automated API/lifecycle tests and local Docker integration are provided. Live GitHub repository/organization and App/PAT combinations and real Tart local execution have not been certified. This is not a promise of GitHub-hosted runner tool inventory, startup speed, throughput, or support response times.

## Install and verify

### Homebrew on Apple Silicon Mac

On macOS 14 or newer with Apple Silicon, install from the Delino tap:

```sh
brew install delinoio/tap/runmoor
runmoor version
```

Homebrew installs the prebuilt release and checks its pinned SHA-256. Tart and runner images are installed separately. Installation does not configure runners or register or start a service. Intel Macs and Linux Homebrew are not supported.

Update with `brew update` followed by `brew upgrade delinoio/tap/runmoor`, or remove with `brew uninstall delinoio/tap/runmoor`. If you registered a Runmoor service, stop and uninstall that service before upgrading or removing the package; after upgrading, reinstall the service with the new executable and start it explicitly. See the [operations guide](https://oss.delino.io/runmoor/operations) for service commands. Your configuration and data remain yours.

### Release archives

Download the matching `runmoor-darwin-arm64.tar.gz`, `runmoor-linux-amd64.tar.gz` or `runmoor-linux-arm64.tar.gz` from the [`runmoor@v…` releases](https://github.com/delinoio/oss/releases). Download `SHA256SUMS` and the `.sigstore.json` bundles alongside it. A publication dry-run archive is unsigned and is not a public release.

Choose a published stable `runmoor@v…` release and replace `X.Y.Z` below with its version. Install cosign separately before verification. This example downloads and installs the Apple Silicon Mac archive; macOS 14 or newer is required.

```sh
(
set -eu
RUNMOOR_TAG='runmoor@vX.Y.Z'
RUNMOOR_ARCHIVE='runmoor-darwin-arm64.tar.gz'
RUNMOOR_IDENTITY="https://github.com/delinoio/oss/.github/workflows/release-runmoor.yml@refs/tags/${RUNMOOR_TAG}"
RUNMOOR_DOWNLOAD_DIR=$(mktemp -d)
trap 'rm -rf "$RUNMOOR_DOWNLOAD_DIR"' EXIT
cd "$RUNMOOR_DOWNLOAD_DIR"

for file in "$RUNMOOR_ARCHIVE" "${RUNMOOR_ARCHIVE}.sigstore.json" SHA256SUMS SHA256SUMS.sigstore.json; do
  curl --fail --location --silent --show-error \
    --output "$file" "https://github.com/delinoio/oss/releases/download/${RUNMOOR_TAG}/${file}"
done
cosign verify-blob \
  --bundle "${RUNMOOR_ARCHIVE}.sigstore.json" \
  --certificate-identity "$RUNMOOR_IDENTITY" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "$RUNMOOR_ARCHIVE"
cosign verify-blob \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity "$RUNMOOR_IDENTITY" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  SHA256SUMS
awk -v archive="$RUNMOOR_ARCHIVE" '$2 == archive { print; found++ } END { if (found != 1) exit 1 }' SHA256SUMS > SHA256SUMS.selected
shasum -a 256 -c SHA256SUMS.selected
tar -xzf "$RUNMOOR_ARCHIVE"
mkdir -p "$HOME/.local/bin"
install -m 755 runmoor "$HOME/.local/bin/runmoor"
"$HOME/.local/bin/runmoor" version
)
```

The download tag and signing identity must refer to the same release. Tag-triggered releases use the identity above. For a release explicitly signed by a manual run on `main`, use the exact identity `https://github.com/delinoio/oss/.github/workflows/release-runmoor.yml@refs/heads/main` after checking its release run.

Linux users select `runmoor-linux-amd64.tar.gz` or `runmoor-linux-arm64.tar.gz` and may use `sha256sum -c` instead of `shasum -a 256 -c`. Add your user binary directory to PATH yourself. Direct archive installation does not register the executable with Homebrew or install a Runmoor binary auto-updater, system-level service, or bundled Docker/Tart.

## Configure

> **Version note:** Runmoor 0.2.0 introduced automatic setup and managed runner updates. Version 0.1.3 uses the explicit pinned configuration and manual image preparation also documented below. Check `runmoor version` before using 0.2.0 commands.

```sh
runmoor init
runmoor config validate
runmoor run
```

`init` creates the minimal configuration described below. Existing files are never overwritten. All commands accept `--config PATH`. Run `doctor` from another terminal after the manager has prepared its images.

Both supported operating systems use:

| Purpose | XDG location | Default |
| --- | --- | --- |
| Configuration | `$XDG_CONFIG_HOME/runmoor/config.toml` | `~/.config/runmoor/config.toml` |
| State | `$XDG_STATE_HOME/runmoor` | `~/.local/state/runmoor` |
| Managed images | `$XDG_DATA_HOME/runmoor` | `~/.local/share/runmoor` |

State/data may be overridden by absolute TOML paths. Configuration and credential files must be owned by your user, regular files, and mode 0600. Directories/socket are private to that user. Very long state paths exceed the Unix socket length limit and are rejected. Storage relocation requires drain, stop, and a complete installation backup; it is not a live reload.

`RUNMOOR_PAT` is an example environment variable name for a GitHub personal access token, not a token issued by Runmoor or a one-hour runner registration token. Create a fine-grained PAT in GitHub under **Settings → Developer settings → Personal access tokens → Fine-grained tokens**. Repository runners require repository Administration read/write; organization runners require organization Administration read and Self-hosted runners read/write. The token owner must be allowed to manage the target's runners, and an organization may require token approval. See the [PAT creation guide](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens) and [runner permissions](https://docs.github.com/en/actions/how-tos/manage-runners/use-actions-runner-controller/authenticate-to-the-api).

For an Ubuntu systemd user service, prefer a user-owned mode 0600 credential file and `credential = { file = "REPLACE_WITH_ABSOLUTE_PAT_FILE" }`. A terminal's exported variable is not automatically inherited by the user service; `runmoor service install` never embeds its value. After rotating a PAT in the same file, run `runmoor resume --pool NAME` to refresh the connection and clear authentication suspension. An environment-backed service already running without its PAT needs a stop/start after importing the variable, followed by `resume`. The [public Ubuntu service guide](https://oss.delino.io/runmoor/operations#github-pat-for-an-ubuntu-user-service) gives the token entry, file, startup and recovery steps. Do not put a PAT value in TOML or a service definition.

## Automatic configuration

In a terminal, `runmoor init` asks for the GitHub target, execution backend and
credential reference. On Linux the suggested backend is Docker; on Apple Silicon
macOS it is Tart. Supply the referenced credential separately. Init does not
install Docker/Tart or start a service.

For scripts, provide the inputs directly:

```sh
runmoor init --backend docker \
  --target https://github.com/OWNER/REPOSITORY \
  --credential-env RUNMOOR_PAT
runmoor config validate
runmoor run
```

The generated configuration needs no resource limits, architecture, runner
version or Docker image digest:

```toml
schema_version = 1

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
```

The manager detects host capacity and the local Docker engine limit. Defaults
are 2 CPUs/4096 MiB per Docker job and 4 CPUs/8192 MiB per Tart job. Concurrency is
calculated from both CPU and memory, with at most two concurrent Tart VMs.
Smaller machines reduce omitted allocations, down to 1 CPU/1024 MiB for Docker
and 2 CPUs/4096 MiB for Tart. Explicit values are never silently reduced. All
pools, preparation work and DinD share the applicable budgets; warm runners
default to zero. Disk reserves default to 10240 MiB, or 20480 MiB with Tart.
There is no automatic CPU/memory reserve for other applications; set a smaller
`[host]` budget when sharing the computer with other work.

Omit `runner_version`, or set it to `"latest"`, for automatic management. An
exact version keeps a pin. Docker image omission chooses the official minimal
image. Explicit custom Docker images still require immutable digests; in latest
mode Runmoor replaces only the dedicated runner directory in a new image.
Language toolchains remain your workflow's responsibility.

Use `runmoor config show --resolved` and `runmoor status` to inspect calculated
capacity and the committed runner version. Before the first run, a version can
still be unresolved. These inspection commands do not download images. Use the
`runs-on` value printed by init in your workflow.

For GitHub App authentication, init accepts `--auth app`, `--client-id`,
`--installation-id` and a credential reference. `--credential-file` is the
alternative to `--credential-env`; pass a path or variable name, never a token.
Existing files are never overwritten, and noninteractive init fails with guidance
when required inputs are missing.

To migrate an existing pool, omit its resource/architecture/concurrency fields
where you want automatic defaults and change its `runner_version` to `"latest"`.
Keep its routing labels and credential references. Keep a custom Docker digest
or omit `image` to use the official image; retain a Tart image UUID to use it as
the immutable base. Reload after saving. Existing exact version pins continue to
work until you explicitly change them.

## Explicit pinned configuration

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

Status and doctor JSON use `schema_version: 1`. Status includes image revisions and pending recovery. Doctor also reports the current state and briefly acquires/releases OS sleep inhibition to check permissions. Errors have stable codes, affected identifiers and a recovery action. English text is usable without color; use `--no-color` or `NO_COLOR` to disable ANSI logs. JSON never uses color.

`pause` stops acquisition/new capacity and preserves running jobs. `drain` additionally waits for jobs/local cleanup. `stop` drains before exiting and waits for open image setup and pending image removal, including with `--force`. Finish setup by shutting down its VM or sealing the revision, and retry pending removal as needed. New image work requires restarting the manager after stop; `stop --pool NAME` drains that pool while the manager keeps serving other pools. Only explicit `--force` terminates owned work. `resume` revalidates the pool. Pool control commands without `--pool` apply to all pools. Resume a suspended pool after correcting credentials or preparation failures.

Reload validates the entire candidate first. Existing jobs retain their original configuration and timeout. Removed/changed pools drain their previous generation; a new generation with the same GitHub scale-set identity waits until the old one retires. A failed reload leaves the last valid configuration active.

Real demand receives capacity before warm runners. Round-robin allocation shares remaining resources across pools. Minimum idle is best effort; running work is never preempted. CPU/memory reservations include DinD and image setup. Low disk blocks new work without evicting active jobs or sealed images.

## Docker and Docker-in-Docker

The official `ghcr.io/actions/actions-runner` image is intentionally minimal. With automatic management, omit `image` and `runner_version`: Runmoor downloads the official native-architecture image, verifies its runner version and records its immutable digest. Compatible custom images need a non-root `runner` user with UID/GID 1001, a POSIX shell and standard utilities, and the Docker CLI for DinD. Automatic custom-image preparation requires no declared image volumes and a dedicated `runner_path`; that directory is replaced only inside a disposable candidate. The source image remains unchanged.

For manual mode, pin a custom image by its immutable digest, pre-pull it locally, and set an exact `runner_version` equal to the installed runner. This existing workflow remains supported.

To enable Docker builds, container actions and service containers for a pool:

```toml
mode = "dind"
daemon_image = "docker@sha256:REPLACE_WITH_DIGEST"
daemon_resources = { cpu = 1, memory_mib = 1024 }
```

DinD requires cgroup v2 and a privileged daemon container. Its CPU/memory must fit alongside the runner. Omitted daemon resources default to 1 CPU and 1024 MiB; its immutable image remains an explicit choice. Plain mode is still the default. Every execution has its own daemon/socket/storage, matching workspace/externals paths and network namespace. The host Docker socket, personal directories, SSH agents and management credentials are never passed to jobs. Remote Docker endpoints are rejected.

Docker and privileged DinD share a kernel; they are not secure isolation for arbitrary hostile workloads. Run trusted developer/team workflows and control external fork execution through GitHub policy. Job containers, daemon, networks and volumes are destroyed after completion/cancellation. Base images remain reusable; use GitHub Actions cache instead of persistent local job/build-cache volumes.

## Prepare macOS images

Install **Tart 2.37.0** on macOS 14+ arm64 yourself. Install **Tart Guest Agent 0.14.2** with RPC enabled in the guest's non-root runner account. Account setup, login, Xcode licensing and tools remain manual. Review the version-specific [Tart license](https://github.com/openai/tart/blob/2.37.0/LICENSE) and [Guest Agent license](https://github.com/openai/tart-guest-agent/blob/v0.14.2/LICENSE), currently FSL-1.1-ALv2, plus the applicable Apple software terms. They are external software, not bundled or relicensed by Runmoor.

## Managed runners from a prepared Mac image

Once an operator-owned image has a clean non-root account and the required Guest
Agent RPC, provide it directly to init:

```sh
runmoor init --backend tart \
  --target https://github.com/OWNER/REPOSITORY \
  --credential-env RUNMOOR_PAT \
  --image-source LOCAL_TART_NAME
runmoor run
```

Use `--source-home PATH` for an external Tart home, or select a `.tvm` export,
`oci://` reference or existing sealed UUID. The corresponding TOML setting is
`image_source = { from = "LOCAL_TART_NAME" }`, optionally with `source_home`.
It cannot be combined with `image`. Runmoor imports and freezes a separate base,
then installs the verified latest runner in a clone, validates it and seals the
result. Failed installations and future runner updates reuse that fixed base.
Source tags are not
followed for OS or Xcode upgrades; select a new source explicitly for those.

With an existing sealed UUID, use `image = "IMAGE_UUID"` and omit
`runner_version`. For a fixed environment, specify the image's exact installed
version instead. CPU, memory and architecture can be omitted in either mode.
The dedicated runner directory must not contain personal files, credentials or
previous job workspaces. Automatic installation replaces that directory only in
a preparation image, never in a running job or the original imported source.

For a new Mac image that still needs interactive setup, `runmoor init
--image-only` creates an empty manager configuration with automatic host budgets.
Use the manual preparation workflow below. `image create` can omit `--cpu` and
`--memory-mib`; `image seal --id IMAGE_UUID` installs the latest runner by default.
Keep `--runner-version VERSION` to validate a manually installed exact version.

## Manual image preparation

Image changes (`create`, `open`, `seal`, `remove`) require a running manager. Keep `runmoor run` or the user service running until setup and validation finish, so sleep inhibition covers a setup VM after `image open` returns. `image list` remains available offline. For the first image, start with a configuration containing no pools or connections. `schema_version = 1` alone uses automatic budgets; the following explicit example is also supported:

```toml
schema_version = 1

[host]
max_runners = 2
cpu = 4
memory_mib = 8192
min_free_disk_mib = 20480
```

Choose budgets appropriate for your Mac, start `runmoor run`, and issue image commands from a second terminal using the same configuration. After sealing, add the connection and Tart pool, then run `runmoor reload`.

```sh
runmoor image create --name xcode --ipsw "$HOME/Downloads/restore.ipsw" --cpu 2 --memory-mib 4096
runmoor image create --name imported --from LOCAL_TART_NAME --cpu 2 --memory-mib 4096
runmoor image create --name imported-oci --from oci://REGISTRY/NAMESPACE/IMAGE:TAG --cpu 2 --memory-mib 4096
runmoor image list
runmoor image open --id IMAGE_UUID
runmoor image seal --id IMAGE_UUID --runner-version 2.337.0 --runner-path /Users/runner/actions-runner
```

Existing local sources must be stopped; `--source-home` selects an external Tart storage directory. Absolute `.tvm` exports and existing sealed revision UUIDs are also accepted by `--from`. OCI references resolve to immutable digests before sealing. Imports never modify the operator's original image.

For manual pinning, use a clean dedicated account, install the exact runner release, enable Guest Agent RPC for that logged-in account, and remove previous runner registration/credentials and nonempty workspaces before sealing. The guest runner account must be non-root. Keep personal credentials, keychains and SSH agents out of the base image. Sealing validates readiness/versions, stops the image, and publishes a local immutable revision.

A Tart pool uses `backend = "tart"`, `mode = "plain"`, `arch = "arm64"`, the sealed UUID as `image`, matching `runner_version` and `runner_path`, and optional VM resource overrides. Each job boots a new clone; the sealed base is never a job VM. Changing a base requires creating and sealing another revision. `image remove --id IMAGE_UUID` refuses active/referenced images. Close a setup window before removal. Setup and validation share the host budget and maximum **two concurrent Runmoor macOS VMs**.

`image seal --runner-path` and TOML `runner_path` must use the same absolute guest directory, beginning with `/` and containing no `..`, NUL or line breaks. Paths are preserved exactly; resolve parent-directory traversal before sealing. The default Tart location is the `actions-runner` directory inside the runner account's home.

Runmoor uses private Tart storage with Tart automatic pruning disabled. Runmoor separately collects unreferenced revisions that it generated for runner updates; user-created revisions require explicit removal. It does not distribute macOS/Xcode images or retain failed job clones.

## Services, recovery and upgrades

```sh
runmoor service install
runmoor service start
runmoor service stop
runmoor service uninstall
```

These commands manage a launchd or systemd **user** service with the same drain semantics. Install the binary at a persistent location first. Installation does not overwrite an existing service definition. Uninstall preserves data/configuration and refuses to abandon known live executions when the manager cannot be contacted. Run the service in a functioning user session; availability after logout/reboot depends on that OS session, and Runmoor does not change system login policy.

Manager-only restart reconciles SQLite with actual Docker/Tart and GitHub state, resumes verified live work and retries incomplete cleanup. Ambiguous resources are quarantined rather than deleted. Confirmed termination releases resources; unresolved cleanup/ownership records remain durable. Runmoor never automatically reruns a failed GitHub job.

A recorded job completion continues through cleanup even if GitHub has already removed its ephemeral runner registration. Capacity becomes available once the owned execution is confirmed stopped, while any remaining cleanup is retried. An upgrade does not automatically recover existing quarantines. For a previously affected completed job, confirm completion in GitHub and verify the exact ownership and stopped state of its local resources before recovering the affected pool with `runmoor stop --pool NAME --force`.

Back up only after `drain` and `stop`. Preserve the complete state and managed-data directories; protect referenced credential files separately. Install the new binary manually and start again. Roll back using a compatible binary and its matching drained state/data backup. Version 0.2.0 upgrades existing state automatically; back up before upgrading and use the matching backup to return to 0.1.3. Unsupported database versions fail without destructive migration; never reuse an older backup while resources created after that backup are still active.

Jobs retain timeout accounting across restart/sleep. Active work requests OS sleep inhibition; warm idle capacity does not keep the machine awake indefinitely. Failure is a warning and does not change system power settings. Forced sleep, lid closure, shutdown and power loss can still interrupt work.

## Managed runner updates

While the manager runs, automatic pools check for a new stable runner release at
startup and every hour. Request an immediate check with:

```sh
runmoor runner update
runmoor runner update --pool linux
runmoor status
runmoor config show --resolved
```

Download and validation finish before the replacement activates. Existing jobs
finish with their original environment, and replacements using the same scale
set wait for the previous generation to drain. An update gets the next available
preparation slot without terminating busy jobs. Pause, drain and stop remain in
effect during updates.

If GitHub, the image registry or validation is temporarily unavailable, Runmoor
keeps the last verified environment and retries with backoff. Status reports the
current version, candidate, last check, next retry and failure or capacity wait.
A failed first installation waits because no fallback exists. If the known
GitHub update deadline passes, new work waits until a supported image is ready;
active jobs still finish. Unknown freshness is shown explicitly. GitHub can
require urgent security updates earlier than its usual 30-day window.

Current, previous and actively referenced generated images are retained.
Unreferenced generated artifacts are cleaned up without deleting user source
images or force-removing externally referenced Docker images. Official pulled
base layers remain available in Docker. Runmoor itself, Docker, Tart, Guest Agent,
macOS and Xcode still use their existing manual/package-manager update workflows.

## Troubleshooting and privacy

- `AUTHENTICATION_FAILED`: correct the referenced credential/permissions, then resume the pool.
- `IMAGE_INVALID` or `RUNNER_VERSION_UNSUPPORTED`: pre-pull a correct digest or seal a compatible image. Managed pools retry runner preparation automatically; inspect status or request `runner update`. Exact pins require explicit replacement. GitHub generally requires replacement within 30 days and may require security updates sooner.
- `CAPACITY_EXHAUSTED` or `DISK_LOW`: adjust explicit budgets/free disk, or remove an unused sealed image yourself. Do not delete active execution storage.
- `OWNERSHIP_AMBIGUOUS`: preserve local state and investigate the exact resource. Use a different scale-set name when another installation owns it; restoring ownership requires the original matching backup.
- `CLEANUP_PENDING`: restore Docker/Tart/GitHub connectivity and let reconciliation retry. A stopped manager reports pending cleanup until the next run.
- `SLEEP_INHIBITION_UNAVAILABLE`: check OS utility/session permissions; work continues without a sleep guarantee.
- Repeated preparation failures suspend the affected pool after three attempts. Unrelated healthy pools continue.

### Ubuntu startup checks

Use `runmoor init` for a missing configuration; `runmoor config init` is not a
command. Most commands load the configuration before state. If
`Cannot securely open a private file` occurs during `config validate`, inspect
the selected configuration path (`~/.config/runmoor/config.toml` by default,
or the path passed with `--config`). It may be missing, inaccessible, or a
symlink. Restore the matching configuration for an existing installation
rather than deleting its state.
See the [configuration guide](https://oss.delino.io/runmoor/configuration)
for owner-only file requirements and path checks.

`Docker capacity is awaiting verification` means new Docker work is paused
until Runmoor can query the local engine; it retries automatically. Run
`docker info` without `sudo` as the Runmoor user. If the Unix socket denies
access, check its group and the user's active groups. Granting `docker` group
access gives root-level Docker control and requires a new login session before
the user service receives the new membership. Runmoor itself remains a user
service. See the [Docker guide](https://oss.delino.io/runmoor/docker).

An existing service definition prevents `runmoor service install` from
overwriting it. On Ubuntu, inspect `systemctl --user cat runmoor.service` for
the current binary and configuration paths, then run
`systemctl --user daemon-reload` in the service user's login session. If
`runmoor service start` reports `User service command failed`, run
`systemctl --user enable --now runmoor.service` directly and inspect
`systemctl --user status runmoor.service --no-pager -l` plus
`journalctl --user -u runmoor.service -n 50 --no-pager` for the original
failure. Preserve the service definition and state until any owned executions
and cleanup have been drained. See the
[operations guide](https://oss.delino.io/runmoor/operations).

Diagnostics are local, sanitized structured metadata bounded by **seven days and 256 MiB**. Completed execution history expires after seven days; unresolved ownership/cleanup remains until reconciliation. Credentials, JIT configuration, workflow secrets and raw job output are excluded. User-created sealed images remain until explicit deletion; generated runner revisions follow managed retention. There is no telemetry or Prometheus endpoint.

Report reproducible issues through [GitHub Issues](https://github.com/delinoio/oss/issues). Include version, platform, safe error code and relevant sanitized status. Do not include credentials, JIT data, raw workflow logs, or private VM contents. The [Runmoor documentation site](https://oss.delino.io/runmoor) covers the same supported workflows.
