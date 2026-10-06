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
check whether the systemd user manager stayed alive across logout:

```sh
loginctl show-user "$(id -un)" --property=Linger
```

If this reports `Linger=yes`, the user manager may still have its old group
membership even though `docker info` works in the new login. After active
Runmoor work has drained and stopped, have an administrator run
`sudo loginctl terminate-user RUNMOOR_USER` from a separate administrator
session (replace `RUNMOOR_USER` with the account name), or reboot the host.
Terminating the user ends all of that account's sessions and user services.
Log in again, verify `docker info` without `sudo`, then run
`runmoor service start`. Restarting only `runmoor.service` or entering a
`newgrp` shell does not refresh a still-running user manager's groups. See the
[systemd loginctl reference](https://www.freedesktop.org/software/systemd/man/latest/loginctl.html)
for lingering and user termination. Membership in the `docker` group grants
root-level Docker control; review the [official Docker post-installation guidance](https://docs.docker.com/engine/install/linux-postinstall/)
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

## Docker volume cleanup (unreleased)

The unreleased cleanup correction checks each volume's current ownership
immediately before removal. A conflicting volume is preserved and reported as
`OWNERSHIP_AMBIGUOUS`; cleanup remains incomplete. If inspection fails, restore
Docker access so cleanup can retry. Removal stays non-force, including during
`stop --force`, and a volume confirmed absent needs no removal.

Docker cannot make this ownership check and removal one atomic operation. Avoid
replacing execution volumes while cleanup runs: a replacement after inspection
can still be affected. Check the containing release before relying on this
correction.

## DinD CPU admission (unreleased)

Releases through 0.2.7 reserve both runner and daemon CPU. The unreleased change
reserves only `resources.cpu` for each DinD runner. Memory still reserves
`resources.memory_mib + daemon_resources.memory_mib`. `daemon_resources.cpu`
remains the daemon container's CPU limit and must not exceed the Docker engine's
CPU count. It is not added to the host or engine CPU admission budget. The daemon
can use CPU in addition to the runner, so the reservation does not cap their
combined CPU usage. No TOML changes are needed.

For a host and Docker engine with 32 CPUs and 512 GiB, a runner using 2 CPUs/16 GiB
and a daemon using 2 CPUs/2 GiB reserve 2 CPUs/18 GiB after this change. Twelve idle
runners need 24 CPUs/216 GiB instead of 48 CPUs/216 GiB; `max_runners = 15` allows
up to 15 runners, and an omitted cap calculates 16 from CPU capacity. Smaller
engine budgets and other work can reduce available capacity.

When this change becomes available, existing runner and preparation reservations
retain their previous values across manager restart and reload until those
resources terminate. Newly created runners use the revised CPU reservation.
Check the containing release before relying on this behavior; it is not available
in 0.2.7.
