# Runmoor Docker Execution

> **Version note:** Runmoor 0.2.0 introduced automatic setup and managed runner updates. Version 0.1.3 uses the explicit pinned configuration and manual image preparation also documented below. Check `runmoor version` before using 0.2.0 commands.

The official `ghcr.io/actions/actions-runner` image is intentionally minimal. With automatic management, omit `image` and `runner_version`: Runmoor downloads the official native-architecture image, verifies its runner version and records its immutable digest. Compatible custom images need a non-root `runner` user with UID/GID 1001, a POSIX shell and standard utilities, and the Docker CLI for DinD. Automatic custom-image preparation requires no declared image volumes and a dedicated `runner_path`; that directory is replaced only inside a disposable candidate. The source image remains unchanged.

For manual mode, pin a custom image by its immutable digest, pre-pull it locally, and set an exact `runner_version` equal to the installed runner. This existing workflow remains supported.

## Ubuntu Docker access and capacity

Runmoor uses a local Unix-socket Docker engine as the user running the manager
or its systemd user service. Runmoor itself should run as that user, not through
`sudo`. The status message `Docker capacity is awaiting verification` means
Runmoor could not confirm the engine's CPU and memory capacity. New Docker work
waits while Runmoor retries the check automatically. Restoring Docker access
allows a later check to clear the wait.

Check Docker from the same user account, without `sudo`:

```sh
docker info
docker context inspect --format '{{.Endpoints.docker.Host}}'
```

The selected endpoint must be a local `unix:///` socket; TCP and SSH Docker
endpoints are unsupported. Runmoor uses `docker_socket` from its configuration
first, then `DOCKER_HOST` if set, then the Docker CLI context. Check those
settings if the context output and Runmoor's result differ. If `docker info`
reports permission denied for `/var/run/docker.sock`, inspect its group and
your current groups:

```sh
ls -l /var/run/docker.sock
id -nG
```

When the socket belongs to the `docker` group, an administrator can add the
Runmoor user to that group with `sudo usermod -aG docker "$USER"`. Log out
completely and log back in, then verify `docker info` again without `sudo` and
restart the Runmoor user service. A `newgrp` shell does not update an already
running user service. Membership in the `docker` group grants root-level Docker
control; review the [official Docker post-installation guidance](https://docs.docker.com/engine/install/linux-postinstall/)
before granting it. If the permission error names a Docker CLI configuration
file instead of the socket, diagnose that file's ownership separately.

To enable Docker builds, container actions and service containers for a pool:

```toml
mode = "dind"
daemon_image = "docker@sha256:REPLACE_WITH_DIGEST"
daemon_resources = { cpu = 1, memory_mib = 1024 }
```

DinD requires cgroup v2 and a privileged daemon container. Its CPU/memory must fit alongside the runner. Omitted daemon resources default to 1 CPU and 1024 MiB; its immutable image remains an explicit choice. Plain mode is still the default. Every execution has its own daemon/socket/storage, matching workspace/externals paths and network namespace. The host Docker socket, personal directories, SSH agents and management credentials are never passed to jobs. Remote Docker endpoints are rejected.

Docker and privileged DinD share a kernel; they are not secure isolation for arbitrary hostile workloads. Run trusted developer/team workflows and control external fork execution through GitHub policy. Job containers, daemon, networks and volumes are destroyed after completion/cancellation. Base images remain reusable; use GitHub Actions cache instead of persistent local job/build-cache volumes.
