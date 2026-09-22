# Runmoor Docker Execution

The official `ghcr.io/actions/actions-runner` image is intentionally minimal. Compatible images must provide a non-root `runner` user with UID/GID 1001, a POSIX shell and standard utilities, exact runner binaries under `runner_path`, and the Docker CLI for DinD. Pin each image by its immutable digest, pre-pull it locally, and keep the configured `runner_version` equal to the actual runner.

To enable Docker builds, container actions and service containers for a pool:

```toml
mode = "dind"
daemon_image = "docker@sha256:REPLACE_WITH_DIGEST"
daemon_resources = { cpu = 1, memory_mib = 1024 }
```

DinD requires cgroup v2 and a privileged daemon container. Its CPU/memory must fit alongside the runner. Every execution has its own daemon/socket/storage, matching workspace/externals paths and network namespace. The host Docker socket, personal directories, SSH agents and management credentials are never passed to jobs. Remote Docker endpoints are rejected.

Docker and privileged DinD share a kernel; they are not secure isolation for arbitrary hostile workloads. Run trusted developer/team workflows and control external fork execution through GitHub policy. Job containers, daemon, networks and volumes are destroyed after completion/cancellation. Base images remain reusable; use GitHub Actions cache instead of persistent local job/build-cache volumes.
