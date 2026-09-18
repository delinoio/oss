# Runmoor command foundation

## Scope

`cmds/runmoor` owns the CLI, local controller, GitHub adapter, Docker and Tart backends, image lifecycle, service integration, power inhibition, diagnostics, and reconciliation for issue #893. Internal implementation boundaries are files in `internal/runmoor`; adapters expose interfaces for deterministic tests without substituting mocks in production.

## Runtime and Language

Use the root Go module and Go version. Dependencies include `actions/scaleset v0.4.0`, `go-toml/v2 v2.4.3`, the Docker v28.5.2 client with API negotiation, existing `modernc.org/sqlite`, and `google/uuid`. Unsupported host operations return typed diagnostics while portable code remains buildable in Windows Go CI.

## Users and Operators

Trusted developers and small-team operators install the binary, Docker/Tart, credential files, and their own images. They maintain GitHub authorization, untrusted-fork policy, OS user sessions, image toolchains, and manual upgrades. GitHub Issues provides community support without a response SLA.

## Interfaces and Contracts

### CLI and configuration

- Commands: `init`, `config validate`, `run`, `status`, `doctor`, `reload`, `pause`, `resume`, `drain`, `stop`, `version`; `service install/start/stop/uninstall`; `image create/open/seal/list/remove`.
- Global and command-local `--config` select a TOML file. `--no-color` or `NO_COLOR` disables default text-log ANSI colors. JSON is uncolored. All product output is English.
- `status --json` and `doctor --json` have `schema_version: 1`; errors contain stable `code`, `message`, `recovery`, and relevant pool/runner identifiers. Status includes image revisions and unresolved preparation; doctor includes the current status snapshot plus persistent pool/execution problems and probes actual sleep-inhibition acquisition. The local Unix HTTP control protocol is `/v1/control`, available only through an owner-only filesystem socket. It is not a remote API.
- `init` exclusively creates an annotated skeleton with explicit incomplete resource/image/credential inputs. Configuration loading rejects unknown keys/versions, unsafe paths, duplicate identities, unsupported targets/architecture, contradictory backend fields, and impossible budgets before activation.
- TOML v1 has `storage`, `host`, `timeouts`, `logging`, `connections`, and `pools`; optional Docker socket/Tart executable settings select local dependencies. Connections contain a GitHub.com repository or organization URL, PAT/App enum, one `credential.env` or `credential.file` reference, and App client/installation IDs when applicable. Pools specify connection, explicit scale-set name, routing labels, optional organization group, backend/mode/architecture, pinned image/runner version/path, min-idle/max-runners, runner resources, and DinD image/resources when enabled.
- Host concurrency, CPU, memory MiB and minimum free disk MiB are explicit positive limits. Aggregate minimum idle reservations, including daemon resources, must fit. Image setup consumes the same limits and counts toward the two-macOS-VM maximum.
- Default timeouts are Docker preparation 5 minutes, Tart preparation 10 minutes, and a job 6 hours. Transient retries use exponential jittered backoff of 1–60 seconds, extended for provider rate-limit instructions. Three consecutive preparation failures suspend only the affected pool. Authentication/ownership failures require correction and resume.

### Scheduling and lifecycle

- The official client handles App/PAT token exchange. A custom session loop persists effects before acknowledgement; duplicate and stale messages do not become incremental demand counters. Demand comes from `TotalAssignedJobs`; lifecycle events identify started/completed runners.
- Round-robin allocation satisfies real demand before idle targets. Reservations include preparation, active jobs, uncertain live resources, setup VMs and DinD. Running work is never preempted to satisfy another pool's demand. Low disk blocks new provisioning without evicting jobs or images. Insufficient host/pool capacity is a stable visible wait reason, logged when it changes.
- Scale-set ownership requires the installation marker and durable ownership journal; names alone are insufficient. Unknown existing sets are rejected. Every new runner gets fresh JIT credentials and can execute at most one job.
- Idle retirement removes the GitHub registration before terminating local resources. GitHub's busy rejection preserves a concurrently assigned job. Ambiguous preparation also performs this busy-aware check before cleanup.
- `pause` stops acquisition/provisioning, preserves busy work, and retires idle runners. `drain` pauses and waits. `stop` drains and exits after local termination/cleanup; unresolved remote cleanup remains durable and visible. `stop --pool NAME` drains just that pool, including its older generations; offline drain/stop waits use the same pool selection if the manager becomes unavailable; `stop --pool NAME --force` restricts forced termination to it. `stop --force` immediately cancels in-flight preparation and terminates only verified owned work. It never permits deletion of unrelated resources.
- Resume revalidates credentials, backend/image and resource conditions. Reload validates the candidate before accepting one new generation. Existing jobs retain original generation/configuration/deadlines. Changed or removed pools drain; a replacement using the same remote identity waits for old ownership to retire. Storage relocation is not a live reload operation. Reload serializes with image operations and cannot clear an already requested manager stop.
- Lifecycle transitions, reservation publication, GitHub ownership, and cleanup progress are persisted. Restart reconciles actual Docker/Tart resources and GitHub registrations, preserves verified live work, resumes cleanup idempotently, and quarantines ambiguous state. Reservations are not released until termination is confirmed. No automatic GitHub job rerun is performed.
- Persisted job deadlines continue across manager restart, sleep and connectivity loss. Active preparation/jobs request OS sleep inhibition; idle warm capacity alone does not. Failure warns and continues without changing system policy or promising protection from lid closure, forced sleep, shutdown or power loss.

### Docker

- Require a local Unix-socket Linux engine on the host's native architecture. Reject remote endpoints even when selected by ambient Docker settings. Validate engine resources and immutable image identities (`repository@sha256:…` or a local content-addressed `sha256:…` image ID). Operators pre-pull the configured images.
- The official minimal runner image is supported. Compatible images need the `runner` account with UID/GID 1001, shell/utilities, exact runner binaries, and Docker CLI for DinD. No GitHub-hosted image tool inventory is promised. Auto-update is disabled at scale-set creation; `doctor` checks release freshness and guides explicit image replacement.
- Docker preparation observes a running container for one second after its execution-specific bootstrap marker appears. Immediate launcher exits remain preparation failures and trigger the same three-failure suspension as guest startup failures; `run.sh` stays PID 1 for normal cancellation signals.
- Per execution, create labelled named volumes and a network. DinD adds a privileged daemon with private cgroup v2 namespace, its own socket and disposable Docker storage. Shared workspace/externals paths and network namespace support container actions and service containers. Both runner and daemon have CPU/memory limits.
- Never mount the host socket, personal host directories, SSH agents, configuration, or credentials into jobs. JIT is sent through stdin rather than Docker configuration/environment. All execution containers disable Docker log retention; the nested DinD daemon also defaults to the `none` log driver so container actions and service output are not retained in its storage. Clean up job/daemon/init containers, networks and volumes; retain only base images. Build caches belong in GitHub Actions cache.

### Tart images and jobs

- Image mutations require a running manager; the CLI never executes them offline. The manager retains sleep inhibition for open setup/validation revisions after the request returns. Whole-manager stop, including force-stop and service stop/uninstall, waits for open setup and pending image removal; pool-scoped waits do not. Shutdown rejects new image operations but permits sealing an already open revision and retrying pending removal. Close the setup VM normally or seal it before expecting stop to finish. Offline image listing remains available. Initial setup accepts explicit host budgets with no pools/connections; add the Tart pool and reload after sealing.

- Initial compatibility pins: Tart 2.37.0, Guest Agent 0.14.2, macOS 14+ arm64. Require functional Guest Agent RPC and an exact runner version in the prepared non-root guest account. A reachable guest returns a bounded ready/invalid marker; invalid accounts, versions, registration or workspace state fail immediately with `IMAGE_INVALID`, while unavailable RPC retries within the preparation deadline.
- `image create --name NAME --cpu N --memory-mib N` accepts exactly one `--ipsw PATH` or `--from SOURCE`. Sources are a stopped local Tart name (optional `--source-home`), absolute `.tvm`, sealed Runmoor revision UUID, or `oci://` input resolved to a digest. Imports never modify the operator's source image.
- `image open --id UUID` opens a mutable setup revision for account/Xcode/tool installation. `image seal --id UUID --runner-version VERSION [--runner-path PATH]` validates guest readiness, clean runner registration/workspace and exact versions, stops the VM, hashes the base files and publishes an immutable local revision. Editing requires a new revision. `image remove` refuses referenced or active images.
- Every Tart invocation uses argv/stdin and an allowlisted environment containing private `TART_HOME` and `TART_NO_AUTO_PRUNE=1`. No external automatic pruning is permitted. Each job clones a sealed base, configures limits and boots that clone only. Pool validation and every sealed-base clone (including a new setup revision) recompute the recorded digest and reject missing or altered files before cloning.
- The same arm64 Runmoor binary provides a private guest supervisor, transferred by stdin without management credentials. Its detached runner and the detached native Tart process inherit real null output descriptors, not parent-owned pipes, and survive a manager-only restart. Guest status contains bounded execution metadata, never JIT credentials or job output. Bootstrap checks matching supervisor identity and unfinished readiness after the runner survives a one-second startup observation; missing/non-executable launchers and immediate exits are preparation failures and do not reset the pool circuit breaker. The guest's private state root is fixed alongside the uploaded helper, independent of inherited temporary-directory settings. Reacquisition checks the live supervisor command as well as its persisted identity and startup readiness; rebooted or missing guest state remains uncertain until reconciliation or the original deadline.
- Ownership markers and durable records constrain all VM cleanup. Finished/failed clones and workspaces are destroyed; bases persist until explicit deletion. Tart and Guest Agent are external, version-specific FSL-1.1-ALv2 dependencies; Runmoor neither bundles them nor distributes macOS/Xcode images.

### Service operation

launchd and systemd user services invoke the same foreground manager and drain control path. Definitions contain executable/config references only, never copied credential values. File credential references are recommended for restart persistence. A manager-only failure must not implicitly kill detached live work. Install does not overwrite existing definitions; uninstall preserves configuration, images and unresolved state.

## Storage

- Config: `$XDG_CONFIG_HOME/runmoor/config.toml`, otherwise `~/.config/runmoor/config.toml`.
- State: `$XDG_STATE_HOME/runmoor`, otherwise `~/.local/state/runmoor`.
- Data: `$XDG_DATA_HOME/runmoor`, otherwise `~/.local/share/runmoor`.
- TOML may override absolute state/data directories. The Unix socket path must fit macOS's length limit. Directories are mode 0700; files/socket are 0600. Reject symlinks and foreign ownership at sensitive file boundaries.
- SQLite schema v1 stores an atomic snapshot row under WAL/FULL durability: installation identity, configuration generations/references, pool/session metadata without tokens, runner lifecycle and reservations, image revisions, and cleanup progress. A nonblocking file lock excludes concurrent manager or offline state access.
- Delete completed execution history after seven days. Preserve unresolved cleanup and ownership indefinitely. Sealed images remain until explicit deletion. Unsupported state versions are rejected without conversion.
- Storage relocation is accepted only after all executions complete cleanup and all image setup/removal operations close; it atomically rebinds retained generations while preserving installation ownership. Backup only after drain and stop: preserve the complete state and managed data directories, protect referenced credentials separately, and pair backups with a compatible binary. Updates and rollback are manual; never open a newer state schema with an older binary.

## Security

Trust is limited to developer/team workflows. GitHub runner-group/repository access policy is never replaced. Management credentials remain host-only environment or permission-restricted file references. No credential/JIT values enter SQLite or logs. Unknown upstream errors are converted to bounded safe classifications; raw errors are not wrapped into public messages. Cleanup requires exact owned identity, and unresolved ambiguity remains visible rather than being guessed away.

## Logging

Use `log/slog` text or JSON for lifecycle transitions, preparation duration, retries, capacity/dependency failures, cleanup results and power warnings. Include only safe identifiers and stable codes. Preserve a structured diagnostic record before destroying an execution; never copy raw workflow output or raw runner logs. Enforce both seven-day and 256 MiB retention across local diagnostics. No remote telemetry is emitted.

## Build and Test

- Run formatting, `go vet ./cmds/runmoor/...`, `go test ./cmds/runmoor/...`, and supported-host `go test -race ./cmds/runmoor/...`.
- Ordinary tests use an in-process TLS GitHub API substitute with the real scale-set SDK, deterministic backend/session adapters, temporary private state, and no live credentials or user-service installation.
- Opt-in Docker tests exercise real local containers, DinD builds/actions/service connectivity, quotas, restart adoption, cancellation and ownership-scoped cleanup without requesting GitHub jobs. Opt-in Tart tests require explicitly supplied operator-owned clean input and are never automatic.
- Preserve root Ubuntu/macOS/Windows Go CI. Generate ignored administrator assets with `pnpm --filter devhud-admin build:embedded` before root Go tests/vet. Public docs changes run `pnpm test` in `apps/public-docs`.
- `scripts/release/runmoor.mjs` builds deterministic `runmoor-darwin-arm64.tar.gz`, `runmoor-linux-amd64.tar.gz`, and `runmoor-linux-arm64.tar.gz`, each containing the executable, English README, and license. SHA256SUMS covers exactly those three archives. Publication signs each archive and the checksum file with separate Sigstore bundles and verifies the exact executing workflow identity before upload.
- `.github/workflows/runmoor.yml` adds read-only opt-in-path Docker integration without altering existing CI jobs or development ports. `.github/workflows/release-runmoor.yml` supports `runmoor@v<MAJOR.MINOR.PATCH>` tags and manual dispatch. Dry-run stages contain no signing or publication authority; initial releases are prereleases.
- Release fixtures run without installed workspace packages; YAML workflow assertions run under the dependency-installed `pnpm ci:contracts` suite in `scripts/ci/runmoor-release.test.mjs`.
- Release validation builds three architectures and verify archive inventory/checksums, strict tag parsing, prerelease marking, secret-free dry run and publication isolation. Real keyless signing occurs only in publication jobs with job-scoped permissions.

### Implementation validation (2026-09-18)

- Passed root `go test ./...` and `go vet ./...` after generating the administrator embed bundle; passed Runmoor unit/lifecycle/API tests and native macOS arm64 race tests.
- Passed real local Docker plain and DinD integration: nested build, container-action workspace/externals mounts, service-container network and localhost access, CPU/memory configuration, fresh-adapter recovery, cancellation, repeated cleanup, and preservation of unrelated resources. No GitHub job was assigned in this validation.
- Built and inspected all three native release archives, verified their exact inventory and SHA256SUMS, and ran the packaged macOS arm64 and Linux arm64 version commands. Windows amd64 command and test binaries cross-compiled successfully; Windows execution was not performed locally.
- Passed public-docs `pnpm test`, workflow lint, 13 CI contract tests, and all 150 release fixture tests in a local Linux container. The Linux run supplies the Debian packaging utilities and GNU tar required by existing unrelated release fixtures.
- Live GitHub authentication/job assignment, real local Tart execution, actual user-service installation, real signing, tag pushes and public release publication were intentionally not performed. Their implementation and test seams remain available; the preview must continue to disclose those live verification limits.

## Dependencies and Integrations

Use GitHub.com scale-set APIs, a local Docker engine, external Tart/Guest Agent, launchd/systemd, and native sleep inhibition utilities. No public inbound endpoint is required; Docker/Tart use ordinary NAT. Public docs and release workflows remain in their existing repository domains.

## Change Triggers

Update this document, the project index, scoped command policies, README/public docs and contract tests together for CLI/schema/lifecycle/security/platform/dependency/release changes. Update root and domain AGENTS when project ownership or repository-wide rules change.

## References

- [Runmoor project](project-runmoor.md).
- [Repository defaults](repository-defaults.md).
- [Issue #893](https://github.com/delinoio/oss/issues/893).
- [GitHub runner authentication](https://docs.github.com/en/actions/how-tos/manage-runners/use-actions-runner-controller/authenticate-to-the-api).
- [Runner update requirements](https://docs.github.com/en/actions/reference/runners/self-hosted-runners).
- [Tart 2.37.0](https://github.com/openai/tart/tree/2.37.0), [Guest Agent 0.14.2](https://github.com/openai/tart-guest-agent/tree/v0.14.2).
