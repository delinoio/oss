# Runmoor Configuration

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

## Recover private configuration access

The setup command is `runmoor init`, not `runmoor config init`. Most other
commands, including `config validate`, `status`, and `service`, load the selected
configuration first. `init`, `version`, and help can run without an existing
configuration. If the file was removed, restore the original configuration from
a backup before using an existing installation; do not delete its state to make
the error disappear. `init` creates a new file only when the selected path does
not already exist.

`Cannot securely open a private file` means a private file could not be opened;
the message does not identify the file or the operating-system error. If
`runmoor config validate` reports it, inspect the selected configuration path
first. A missing file, an inaccessible path, or a symlink at the file itself
can produce this message. On Ubuntu, inspect the default path with:

```sh
config_path="${XDG_CONFIG_HOME:-$HOME/.config}/runmoor/config.toml"
namei -l "$config_path"
```

If you passed `--config PATH`, inspect that path instead. Restore a missing
configuration from its matching backup. An existing configuration must be a
regular file owned by the Runmoor user with mode 0600, without a symlink at the
file itself. Changing file permissions cannot restore a missing file.

If configuration validation succeeds but `run` or `status` reports the same
message, inspect the configured state directory and its existing files. The
default state location is shown above; `[storage].state` can override it. A
missing lock or database is normal before the first run because Runmoor creates
those files. Preserve existing state and managed data when diagnosing access.

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
min_idle = 0
max_runners = 2
resources = { cpu = 1, memory_mib = 1024 }
```

Use `amd64` on an amd64 Ubuntu host. GitHub organization targets omit the repository. Organization pools may set `runner_group`; repository pools cannot. Pool names and target/group/scale-set identities must be unique. Scale-set names and labels control workflow routing; existing sets without this installation's ownership evidence are never adopted.

For an App connection set `auth = "app"`, `client_id`, a positive `installation_id`, and a credential reference to its PEM key. For a file reference use `credential = { file = "REPLACE_WITH_ABSOLUTE_PRIVATE_FILE" }`. Do not place a PAT or private key in TOML. Service definitions never copy credential values; file references are easier to keep available across login/reboot than environment references.

GitHub App repository registration requires repository Administration read/write and Metadata read; organization registration requires organization Self-hosted runners read/write. Classic PATs require `repo` for repository runners or `admin:org` for organization runners. Fine-grained PATs require the target's documented Administration/Self-hosted runners permissions. Follow the [official permission guide](https://docs.github.com/en/actions/how-tos/manage-runners/use-actions-runner-controller/authenticate-to-the-api). GitHub groups and repository access rules remain authoritative.

Unknown keys/schema versions, literal credentials, invalid limits, incompatible architecture, mutable image tags and impossible minimum-idle allocations are rejected. The default preparation limits are five minutes for Docker and ten for Tart; jobs default to six hours. Duration values must be positive and at most seven days.

Optional `[storage]` fields `state` and `data` take absolute user-controlled directory paths. `tart_executable` and `docker_socket` are optional top-level executable/socket overrides. The Docker runner path defaults to the official image account home; the Tart path defaults to the actions-runner directory inside the runner account home. Set `runner_path` explicitly when your compatible image uses another absolute guest path. No shell expansion occurs in TOML.

For App authentication, use the same connection structure with these replacements:

```toml
auth = "app"
client_id = "REPLACE_WITH_APP_CLIENT_ID"
installation_id = 12345
credential = { file = "REPLACE_WITH_ABSOLUTE_PRIVATE_PEM_FILE" }
```

See [Docker execution](./docker) and [Tart images](./tart) for backend requirements.
