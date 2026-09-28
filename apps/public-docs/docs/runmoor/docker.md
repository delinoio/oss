# Runmoor Docker Execution

> **Version note:** Automatic setup and managed runner updates are available in Runmoor 0.2.0. Version 0.1.3 uses the explicit pinned configuration and manual image preparation also documented below.

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
