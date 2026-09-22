# Runmoor Configuration

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
