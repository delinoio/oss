# Runmoor command foundation

## Scope

`cmds/runmoor` owns the CLI, local controller, GitHub adapter, Docker, Tart and opt-in host backends, image lifecycle, service integration, power inhibition, diagnostics, and reconciliation for issues #893 and #1312. Internal implementation boundaries are files in `internal/runmoor`; adapters expose interfaces for deterministic tests without substituting mocks in production.

## Runtime and Language

Use the root Go module and Go version. Dependencies include `actions/scaleset v0.4.0`, `go-toml/v2 v2.4.3`, the official split `github.com/moby/moby/client v0.6.0` and `github.com/moby/moby/api v1.56.0` modules, existing `modernc.org/sqlite`, and `google/uuid`. Moby negotiates the daemon API automatically (client minimum API 1.40); the adapter retains local Unix-socket selection without ambient API-version overrides. Inspect/create/list/wait results use the SDK's explicit result envelopes. The legacy monolithic Docker module is no longer a dependency. Unsupported host operations return typed diagnostics while portable code remains buildable in Windows Go CI.

## Users and Operators

Trusted developers and small-team operators install the binary, Docker/Tart, credential files, and their own images. They maintain GitHub authorization, untrusted-fork policy, OS user sessions, image toolchains, and manual upgrades. GitHub Issues provides community support without a response SLA.

## Interfaces and Contracts

### CLI and configuration

- Commands: `init`, `config validate`, `config show --resolved`, `runner update`, `run`, `status`, `doctor`, `reload`, `pause`, `resume`, `drain`, `stop`, `version`; `service install/start/stop/uninstall`; `image create/open/seal/list/remove`.
- Global and command-local help display the current CLI version from the same `Version` constant used by `runmoor version`.
- Global and command-local `--config` select a TOML file. `--no-color` or `NO_COLOR` disables default text-log ANSI colors. JSON is uncolored. All product output is English.
- `status --json` and `doctor --json` have `schema_version: 1`; errors contain stable `code`, `message`, `recovery`, and relevant pool/runner identifiers. Status includes image revisions and unresolved preparation; doctor includes the current status snapshot plus persistent pool/execution problems and probes actual sleep-inhibition acquisition. The local Unix HTTP control protocol is `/v1/control`, available only through an owner-only filesystem socket. It is not a remote API.
- `init` exclusively creates a minimal valid configuration using prompts or flags and omission-aware defaults. Configuration loading rejects unknown keys/versions, unsafe paths, duplicate identities, unsupported targets/architecture, contradictory backend fields, and impossible budgets before activation.
- New `init` pools write `runmoor-linux`, `linux`, `x64` on Docker amd64; `runmoor-linux`, `linux`, `ARM64` on Docker arm64; `runmoor-macos`, `macOS`, `ARM64` on Tart arm64; and `runmoor-macos-host`, `macOS`, `ARM64` on host arm64. The existing scale-set name remains the first label. These labels are generated only by `init`: loading, validating or reloading existing and manually authored pools never adds routing labels, and omitted labels remain empty.
- TOML v1 has `storage`, `host`, `timeouts`, `logging`, `connections`, and `pools`; optional Docker socket/Tart executable settings select local dependencies. Connections contain a GitHub.com repository or organization URL, PAT/App enum, one `credential.env` or `credential.file` reference, and App client/installation IDs when applicable. Pools specify connection, explicit scale-set name, routing labels, optional organization group, backend/mode, optional native architecture, image or image source, latest or pinned runner version/path, min-idle/max-runners, runner resources, and DinD image/resources when enabled.
- Host concurrency, CPU, memory MiB and minimum free disk MiB may be omitted for automatic defaults; explicit values must be positive limits. Aggregate minimum idle reservations, including daemon resources, must fit. Image setup consumes the same limits and counts toward the two-macOS-VM maximum.
- Default timeouts are Docker and host preparation 5 minutes, Tart preparation 10 minutes, and a job 6 hours. Transient retries use exponential jittered backoff of 1–60 seconds, extended for provider rate-limit instructions. Three consecutive preparation failures suspend only the affected pool. Authentication/ownership failures require correction; an unchanged configuration requires explicit resume.
- Forced cancellation preserves the existing preparation-failure counter and pool suspension state, including when an adapter returns success after cancellation. It is logged as cancellation instead of an image/bootstrap failure.
- A scheduled preparation must still be unforced and in the preparing phase when its worker starts. This closes the force-stop gap between reservation and worker registration; cancellation is also checked before JIT and backend work. After journaling a returned JIT registration ID, the worker re-reads the durable forced flag and preparing phase immediately before backend preparation. A stop or lifecycle transition during registration prevents provisioning even before context cancellation arrives, preserves the new phase and failure counters, and retains the registration ID for cleanup.

### Scheduling and lifecycle

- The official client handles App/PAT token exchange. A custom session loop persists effects before acknowledgement; duplicate and stale messages do not become incremental demand counters. Demand comes from `TotalAssignedJobs`; lifecycle events identify started/completed runners.
- Round-robin allocation satisfies real demand before idle targets. Reservations include preparation, active jobs, uncertain live resources and setup VMs. New DinD execution reservations include runner CPU and combined runner/daemon memory; daemon CPU is not separately reserved. Running work is never preempted to satisfy another pool's demand. Low disk blocks new provisioning without evicting jobs or images. Insufficient host/pool capacity is a stable visible wait reason, logged when it changes.
- Idle retirement uses the same logical pool cap across generations as allocation. Excess queued demand at that cap does not evict minimum-idle runners in other pools; newly admissible demand can still displace idle capacity. Tart demand blocked by the shared two-VM ceiling does not evict warm Docker runners; a warm Tart VM can still yield its VM slot. Retirement planning uses the allocator with only demand targets and accounts for available capacity, candidates already selected, and deregistered executions pending cleanup. Once that capacity satisfies demand it preserves remaining minimum-idle runners, while additional CPU/memory needs can still require multiple retirements. These planning reservations never enter durable state or release actual capacity before confirmed termination.
- Scale-set ownership requires the installation marker and durable ownership journal; names alone are insufficient. Unknown existing sets are rejected. Every new runner gets fresh JIT credentials and can execute at most one job.
- Per-pool initialization and retirement share a serialization boundary through remote calls and durable publication. Reload can drain immediately, but new creation intent rechecks the current phase. Draining generations resolve journaled creation by lookup only: delete verified owned results, clear confirmed absence, and preserve unresolved outcomes before releasing the remote identity. Late initialization failures cannot resurrect retired generations.
- When a validated reload supplies a connection for the same remote scale-set identity, its verified connection replaces any earlier durable cleanup authority for each draining generation, including a rollback to the generation's original credential reference. Each validation refreshes the cached client for old-generation lookup, busy-aware runner removal and scale-set deletion. Existing owner labels, recorded scale-set IDs and pending-creation checks remain mandatory; a failed ownership check keeps the old generation draining and blocks same-identity creation.
- Successful durable retirement drops the pool's cached GitHub client and serialization mutex and cancels its session loop. Stale generation references cannot recreate either cache, and late session creation closes its result instead of publishing into retired or pruned state. Failed durable retirement retains the caches for retry. A final message may commit before the retired pool is pruned; the subsequent eligibility lookup treats a missing pool as ineligible, skips job acquisition and cannot interrupt unrelated pools.
- Idle retirement removes the GitHub registration before terminating local resources. GitHub's busy rejection preserves a concurrently assigned job. Ambiguous preparation also performs this busy-aware check before cleanup.
- An expired preparation after restart remains ambiguous and follows busy-aware cleanup. A busy rejection preserves the job and derives an unobserved start from the persisted creation time, never a new recovery-time deadline. Forced termination is reserved for an explicit force-stop or an established busy-job timeout.
- A delayed busy-removal response can restore `Busy` only if the runner still exists in the exact lifecycle phase that initiated removal and no completion, force, remote removal, confirmed termination, or quarantine outcome committed while the request was in flight. Late responses preserve later phases and absent records. When no job start was observed, the original creation-time-based deadline remains authoritative; capacity stays reserved until termination is confirmed.
- `pause` stops acquisition/provisioning, preserves busy work, and retires idle runners. `drain` pauses and waits. `stop` drains and exits after local termination/cleanup; unresolved remote cleanup remains durable and visible. `stop --pool NAME` drains just that pool, including its older generations; offline drain/stop waits use the same pool selection if the manager becomes unavailable; `stop --pool NAME --force` restricts forced termination to it. `stop --force` immediately cancels in-flight preparation and terminates only verified owned work. It never permits deletion of unrelated resources.
- The existing `Paused` phase owns the scoped operator decision for manually pinned pools. Late session or preparation failures retain that phase while recording safe problems, counting non-forced preparation failures and preserving busy-aware runner cleanup. A validated correction may replace the generation but retains `Paused`; only explicit Resume clears the decision. Managed/global pause flags remain independent, and unpaused pools retain their existing failure-specific reload recovery. This fix adds no TOML, SQLite or JSON schema change.
- Resume revalidates credentials, backend/image and resource conditions. Reload validates the candidate before accepting one new generation. A validated reload resumes a suspended replacement only when the prior failure's relevant input changed: connection or runner group for authentication; target, group or scale-set identity for a recorded remote scale-set ownership failure; image, runner version, platform or startup environment for their matching failures. Local execution ownership conflicts and legacy ownership failures without a recorded source stay suspended until ownership is resolved and the operator explicitly resumes them. Runner-version recovery requires a change to the runner-bearing image, source, path, version or platform; a DinD daemon image alone is unrelated. For a preparation failure, only the affected backend's preparation timeout and its Docker socket or Tart executable count as relevant global settings; the job timeout and the other backend's settings do not. A relevant global setting change alone replaces the suspended generation. Other suspensions carry their exact safe problem and preparation-failure count into the replacement. Rejected configuration leaves the committed configuration unchanged; unchanged configuration, missing prior reason, operator pause and stop never auto-resume. Existing jobs retain original generation/configuration/deadlines. Changed or removed pools drain; a replacement using the same remote identity waits for old ownership to retire and still verifies ownership before creation. Storage relocation is not a live reload operation. Reload serializes with image operations and cannot clear an already requested manager stop.
- Status and doctor report a safe `DEPENDENCY_RETRY` diagnostic when legacy state has a suspended pool with no recorded problem, without guessing the original cause or automatically resuming it. A verified managed image replacement may clear only a matching image/version suspension or a three-failure startup preparation circuit breaker; unrelated authentication/ownership failures, operator pause, drain and stop remain authoritative.
- Lifecycle transitions, reservation publication, GitHub ownership, and cleanup progress are persisted. Restart reconciles actual Docker/Tart resources and GitHub registrations, preserves verified live work, resumes cleanup idempotently, and quarantines ambiguous state. Reservations are not released until termination is confirmed. No automatic GitHub job rerun is performed.
- Missing-registration inspection revalidates the lifecycle before starting backend work and inside the atomic state update after an absent GitHub lookup. Only an active preparing/idle/busy execution without completion, forced cleanup, remote removal or confirmed termination may be quarantined by absence. An older lookup cannot overwrite cleanup/completion, replace an existing quarantine diagnostic or recreate a pruned record. Discarded absence results emit no quarantine warning; a failed commit logs only a safe state-storage error. Actual Docker/Tart or GitHub identity errors still quarantine through the existing ownership checks, including errors returned by in-flight work after completion. Ordinary cleanup confirms termination before releasing capacity and retains unfinished local/remote cleanup for retry. This issue #903 fix prevents new stale transitions; it does not migrate or automatically recover existing quarantines.
- Persisted job deadlines continue across manager restart, sleep and connectivity loss. Active preparation/jobs request OS sleep inhibition; idle warm capacity alone does not. Failure warns and continues without changing system policy or promising protection from lid closure, forced sleep, shutdown or power loss. Inhibitor failure backoff retains its warning while work remains active; becoming idle clears both the warning and retry deadline so new runner/image activity immediately retries acquisition.
- Image removal retains sleep inhibition through external cleanup and its durable completion, including interrupted removal pending recovery.
- A quarantined execution remains potentially active and retains sleep inhibition until termination is confirmed; uncertainty never enables automatic termination or releases its reservation.

### Docker

- Require a local Unix-socket Linux engine on the host's native architecture. Reject remote endpoints even when selected by ambient Docker settings. Validate engine resources and immutable image identities (`repository@sha256:…` or a local content-addressed `sha256:…` image ID). Manual pins require pre-pulled images. Managed images are prepared by the manager before activation.
- The official minimal runner image is supported. Compatible images need the `runner` account with UID/GID 1001, shell/utilities, exact runner binaries, and Docker CLI for DinD. No GitHub-hosted image tool inventory is promised. Auto-update is disabled at scale-set creation; the manager updates automatic pools, while `doctor` checks freshness and reports managed progress or manual-pin recovery.
- Runner freshness requests the latest 100 GitHub runner releases under the existing ten-second probe context. Response acquisition reads at most 8 MiB plus one detection byte, rejects an exceeded 8 MiB ceiling before decoding, and parses the complete bounded response. Oversize responses report the limit with manual release-verification and retry guidance; read/transport failures, non-200 responses and malformed JSON remain safe `DEPENDENCY_RETRY` warnings without upstream bodies or raw errors. Stable-release selection and the 30-day update window are unchanged: unknown or expired pins return `RUNNER_VERSION_UNSUPPORTED`, and a newer release within the window prompts replacement for manually pinned pools. Doctor itself never updates a binary, image or running job. The manager owns automatic image updates.
- Docker preparation observes a running container for one second after its execution-specific bootstrap marker appears. Immediate launcher exits remain preparation failures and trigger the same three-failure suspension as guest startup failures; `run.sh` stays PID 1 for normal cancellation signals.
- Per execution, create labelled named volumes and a network. DinD adds a privileged daemon with private cgroup v2 namespace, its own socket and disposable Docker storage. Shared workspace/externals paths and network namespace support container actions and service containers. Both runner and daemon have CPU/memory limits.
- DinD admission CPU equals `resources.cpu`; admission memory equals `resources.memory_mib + daemon_resources.memory_mib`. `daemon_resources.cpu` remains positive and bounded, defaults to one CPU, and controls the daemon container's `NanoCPUs`. Reject a daemon CPU limit above the physical engine's reported CPU count before provisioning; do not add it to host or engine admission usage. Pool costs, minimum-idle validation, automatic concurrency, GitHub advertised/acquired capacity, new execution reservations and status use the same admission policy. Daemon CPU can run in addition to the reserved runner CPU, so reservations no longer bound the sum of Docker container CPU limits. Plain, Tart and host allocations keep their existing semantics.
- Releases through 0.2.7 reserve both runner and daemon CPU. The runner-only CPU change is unreleased and adds no configuration field or storage migration. Preserve persisted execution and in-flight preparation reservations exactly across restart/reload; do not lower their CPU costs under the new policy. They release only through existing confirmed-termination/cleanup boundaries. Newly created reservations use the new policy. Docker image preparation continues to reserve and limit its actual preparation container resources.
- DinD reconciliation inspects the daemon's recorded identity and ownership as well as the runner. Missing/stopped/paused/restarting daemons make the execution unavailable for busy-aware cleanup without claiming termination; ownership changes quarantine it, and transient inspection errors preserve reservations for retry.
- Termination checks deterministic runner/daemon/init names and recorded container IDs independently of label-filtered discovery, before stopping resources and again before confirming termination. Foreign replacements or identity mismatches quarantine the execution and retain its reservation even during force-stop; an empty filtered list alone never proves termination.
- Volume cleanup treats label-filtered discovery as candidate metadata only. Immediately before each non-force removal, inspect the exact current name and require matching installation and runner labels, a supported `work`, `socket`, `externals` or `docker` role, and the corresponding deterministic execution name. The inspection response must identify the requested name. Confirmed absence is idempotent. A mismatch returns `OWNERSHIP_AMBIGUOUS` without DELETE and quarantines the execution while preserving its unfinished local-cleanup record. Inspection errors return a safe dependency failure without DELETE and retain cleanup for retry. Confirmed termination and remote removal remain committed independently of local cleanup. Docker provides no immutable volume ID or conditional delete, so another replacement after the fresh check remains possible; this boundary does not establish atomic race protection. This correction is unreleased.
- Every cleanup attempt repeats that identity, ownership-label and stopped-state verification before any destructive processing, even after durable termination allows the manager to skip Stop. A different-ID replacement with copied labels remains untouched; identity mismatches quarantine with `OWNERSHIP_AMBIGUOUS` and keep local cleanup incomplete. Transient verification failure permits no deletion and retains retry state. Matching stopped originals and confirmed absence remain cleanable. Previously confirmed original termination and its released reservation remain authoritative; unrelated replacements never restore that reservation. This cleanup fix is unreleased and changes no public schema or migration.
- Paused or restarting runner containers are also unavailable, including in plain mode. They enter the same busy-aware cleanup path, retaining their capacity reservation until confirmed termination permits a replacement.
- Never mount the host socket, personal host directories, SSH agents, configuration, or credentials into jobs. JIT is sent through stdin rather than Docker configuration/environment. All execution containers disable Docker log retention; the nested DinD daemon also defaults to the `none` log driver so container actions and service output are not retained in its storage. Clean up job/daemon/init containers, networks and volumes; retain only base images. Build caches belong in GitHub Actions cache.

### Tart images and jobs

- Prepared-guest validation accepts an absent or successfully listed empty runner workspace. An existing workspace whose enumeration fails returns the bounded invalid-image result, even when listing stdout is empty. Automatic preparation rejects that image after one validation call and never invokes runner installation or directory replacement. Actual Guest Agent RPC failures retain preparation retries and redacted diagnostics.

- Image mutations require a running manager; the CLI never executes them offline. The manager retains sleep inhibition for open setup/validation revisions after the request returns. Whole-manager stop, including force-stop and service stop/uninstall, waits for open setup and pending image removal; pool-scoped waits do not. Shutdown rejects new image operations but permits sealing an already open revision and retrying pending removal. Close the setup VM normally or seal it before expecting stop to finish. Offline image listing reads the committed snapshot in image-ID order without reconciliation, archive cleanup, staged VM promotion or lifecycle changes. Manager reconciliation retains ownership of those recovery operations. Initial setup accepts automatic or explicit host budgets with no pools/connections; add the Tart pool and reload after sealing.

- Host compatibility accepts stable Tart 2.x.x releases with complete SemVer major.minor.patch numbers and optional valid build metadata; reject prereleases, shorthand versions, malformed versions, and other major versions. Guest Agent remains pinned to 0.14.2 on macOS 14+ arm64. Tart guest metadata must report the supported `darwin` OS: sealing checks it before reserving or booting a mutable revision, and sealed-image validation checks it before pool acceptance or any sealed-base clone/helper transfer. Empty, Linux, and unknown guest OS values fail as `IMAGE_INVALID` with macOS guidance; existing unsupported records remain available for diagnosis and ownership-checked removal. Preserve the existing owner, stopped-state, digest, runner-version and guest-readiness checks. Require functional Guest Agent RPC and an exact runner version in the prepared non-root guest account. A reachable guest returns a bounded ready/invalid marker; invalid accounts, versions, registration or workspace state fail immediately with `IMAGE_INVALID`, while unavailable RPC retries within the preparation deadline.
- `image create --name NAME [--cpu N --memory-mib N]` accepts exactly one `--ipsw latest|PATH` or `--from SOURCE`. `latest` delegates selection of the newest host-supported Apple restore image to Tart; a path is an absolute local `.ipsw`. Sources are a stopped local Tart name (optional `--source-home`), absolute `.tvm`, sealed Runmoor revision UUID, or `oci://` input resolved to a digest. Imports never modify the operator's source image.
- Local-name imports use a temporary `import-<image UUID>.tvm` archive in private managed data. The image UUID is durable before export begins; normal completion/failure, restart reconciliation across all image phases, and image removal clean that exact artifact. Cleanup errors remain visible in image diagnostics and prevent successful removal from discarding the ownership record. Unjournaled archives and unexpected symlink/directory targets are preserved.
- `image open --id UUID` opens a mutable setup revision for account/Xcode/tool installation. `image seal --id UUID --runner-version VERSION [--runner-path PATH]` validates guest readiness, clean runner registration/workspace and exact versions, stops the VM, hashes the base files and publishes an immutable local revision. Sealing and sealed-image verification hash with bounded, cancellation-aware reads under the operation context, so preparation deadlines and force-stop do not wait for a whole disk read; cancellation closes the file and discards the partial digest. Editing requires a new revision. `image remove` refuses referenced or active images.
- Image open polls the owned VM until Tart confirms it running, bounded by the Tart preparation deadline. A spawned process alone is insufficient. Persist detached setup and validation Tart PIDs with OS-reported process-start identities in private SQLite state and keep that image process state out of status JSON. An image retry confirms the prior process exited before replacing its run alias or launching another Tart process. Compare both PID and process-start identity before treating a recorded Tart process as alive; older state without an identity remains conservatively reserved while its PID exists. Unconfirmed startup records a safe preparation error and holds its reservation until reconciliation confirms both the VM and Tart process stopped. Pending image removal also retains capacity while a recorded Tart process may still be alive.
- Owned-image sealing and validation retain the verified VM directory descriptor while hashing `config.json`, `nvram.bin`, and `disk.img`; open each file relative to that descriptor so a Tart rename cannot redirect a digest to a replacement VM path.
- Seal runner paths use the same validation as TOML pools: a leading `/`, no `..` substring, NUL or line breaks. Reject invalid paths before reservation or guest preparation; store accepted strings unchanged so the pool can match sealed metadata exactly.
- Automatic sealing with an omitted or `latest` runner version also requires a dedicated, already clean directory with at least two `/` separators. Root, shallow directories, redundant separators, `.` segments and trailing separators are invalid. Reject these known invalid paths as `CONFIG_INVALID` before VM metadata inspection, reservation or boot, without changing the image phase, diagnostic or recorded process identity. Manual exact-version sealing retains the basic path rules above; accepted automatic and manual path strings remain unchanged.
- Every Tart invocation uses argv/stdin and an allowlisted environment containing private `TART_HOME` and `TART_NO_AUTO_PRUNE=1`. No external automatic pruning is permitted. Each job clones a sealed base, configures limits and boots that clone only. Pool validation and every sealed-base clone (including a new setup revision) recompute the recorded digest and reject missing or altered files before cloning.
- The same arm64 Runmoor binary provides a private guest supervisor, transferred by stdin without management credentials. Its detached runner and the detached native Tart process inherit real null output descriptors, not parent-owned pipes, and survive a manager-only restart. Guest status contains bounded execution metadata, never JIT credentials or job output. Bootstrap checks matching supervisor identity and unfinished readiness after the runner survives a one-second startup observation; missing/non-executable launchers and immediate exits are preparation failures and do not reset the pool circuit breaker. The guest's private state root is fixed alongside the uploaded helper, independent of inherited temporary-directory settings. Reacquisition checks the live supervisor command as well as its persisted identity and startup readiness; rebooted or missing guest state remains uncertain until reconciliation or the original deadline.
- Tart ownership requires an exact match between the owner-only durable Runmoor record and an owner-only marker inside the current VM directory, including installation, image/execution identity, and VM name. Creation reserves the durable record first and runs create, import, or clone in a private per-entity staging `TART_HOME` that shares only Tart's private content cache. A per-entity lock spans the Tart operation and publication. Once Tart succeeds, Runmoor opens the staged VM directory, publishes the marker atomically through that descriptor, then exposes the canonical name by a no-replace rename of that same directory. The canonical destination remains absent until marker publication, so a concurrent VM at that name is never stamped as Runmoor-owned. Before treating the canonical VM as absent, inspection and recovery promote a staged VM only when its embedded marker and durable record match. An active creation retains its reservation; a markerless interrupted stage remains ambiguous and is preserved. Never overwrite an existing VM marker. For inspection, configuration, boot, guest execution and stop, open the verified VM directory without following symlinks and pass that descriptor through a one-command Tart name alias; Tart resolves the alias through the already-open directory even if the recorded name changes. Keep the per-entity alias used by `tart run` until termination is confirmed. Persist each detached Tart PID with its OS-reported process-start identity in private SQLite state, and keep image process state out of status JSON. Compare both values before treating the recorded Tart process as alive; state without an identity remains conservatively reserved while its PID exists. If the canonical path disappears, preserve ownership and its reservation while the run alias or recorded Tart process may still be live; only treat absence as confirmed after the process exits, then remove a stale alias before dropping the owner record. When Tart confirms an owned VM is stopped, cleanup polls its recorded PID and process-start identity within the caller context. Keep the run alias and reservation while that exact process remains alive; a deadline returns retryable cleanup, and a PID/start mismatch confirms the recorded process exited. The caller context flows through the owned start precheck, and cancellation is checked again immediately before detached Tart start. A missing, malformed, symlinked, unsafe-permission, or mismatching marker on an existing VM is `OWNERSHIP_AMBIGUOUS`; preserve the VM, durable record and reservation, including on force-stop. Known VM absence remains idempotent after liveness reconciliation. Before delete, hold Tart's config lock while atomically moving the verified directory to a stable per-entity name with a no-replace rename, and keep that same lock open until Tart confirms deletion. When recovering a staged deletion after restart, reopen and verify the staged VM, acquire its config lock, and keep it through the delete attempt. Invoke Tart's name-based delete only on that staged name, so a replacement at the recorded VM name cannot become the target. A retry can verify and finish a staged deletion after a manager restart. Legacy record-only state and crashes before marker publication are ambiguous and are never adopted by name; recovery requires a paired backup that retains the embedded marker or reimporting the source as a new revision/identity while preserving the uncertain VM and its records. Clones receive a fresh execution identity and leave the sealed source marker unchanged. Finished/failed owned clones and workspaces are destroyed; bases persist until explicit deletion. Tart and Guest Agent are external dependencies with version-specific licenses. The previously pinned Tart 2.37.0 and Guest Agent 0.14.2 sources use FSL-1.1-ALv2; operators must check the license of their installed Tart release. Runmoor neither bundles them nor distributes macOS/Xcode images.

### Service operation

launchd and systemd user services invoke the same foreground manager and drain control path. Definitions contain executable/config references only, never copied credential values. File credential references are recommended for restart persistence. A manager-only failure must not implicitly kill detached live work. Install does not overwrite existing definitions; uninstall preserves configuration, images and unresolved state.

Every Linux `systemctl --user` invocation, including installation's `daemon-reload` and the validated reload before service start, stop, or uninstall, receives the normal minimal command environment plus `XDG_RUNTIME_DIR` and `DBUS_SESSION_BUS_ADDRESS` only when the caller supplied them. Runmoor does not invent session selectors or inherit other caller variables for service commands. Missing or unreachable user-session context remains a safe dependency failure without exposing environment values or subprocess stderr. The shared `minimalEnv` remains unchanged for Tart, guests, and unrelated subprocesses.

A launchd start refreshes an already-loaded job by booting it out and bootstrapping the validated on-disk plist; it does not kickstart a potentially stale cached definition. A failed bootout prevents bootstrap. Before launchd stop/uninstall drains or boots out the job, query `launchctl list` with no label and select the exact service row from its documented PID/status/label table. Compare an active process's exact kernel-provided argument vector with the validated plist `ProgramArguments`. A loaded but inactive job has no verifiable process arguments and must fail closed before draining; recheck immediately before bootout. Do not use label-specific list output or parse `launchctl print` output, which are detailed/diagnostic formats rather than the documented table interface. A launchd stop/uninstall bootout failure is returned as a safe dependency error and leaves the plist intact unless a separate exact-service query returns service-not-found and the GUI domain remains reachable. An arbitrary query failure or a still-loaded definition cannot count as a successful stop or uninstall.

Before a Linux service start reloads/enables or stop/uninstall drains/disables the unit, inspect `MainPID` and compare an active process's `/proc/<pid>/cmdline` with the validated `ExecStart`. A mismatch or unreadable identity fails closed before `daemon-reload`, drain, or disable, preserving the active manager's loaded stop command and preventing control of a different manager. Check stop/uninstall before draining. After draining, revalidate the unchanged definition snapshot, reload systemd so cached `ExecStop` matches that validated definition, revalidate the snapshot again, and check the active process identity immediately before disabling. For an inactive or matching process, reload systemd and revalidate the unchanged service-definition snapshot before enabling/starting. Recover a mismatched active manager by gracefully stopping it with its original `--config` path, then retrying the service action. Diagnostics do not disclose either path.

Before start, stop, or uninstall, securely read and structurally parse the generated user-service definition. Require its absolute `--config` path to match the requested path. For a requested path containing `..`, resolve both the original spelling and its lexical normalization, rejecting them if symlink traversal changes the resolved path; the caller must not load one file while matching the installed service against another. Executable and configuration arguments in generated definitions must already be canonical absolute paths before comparison; for systemd, `ExecStart` and `ExecStop` must agree on both. Enumerate the full standard user unit search path; any competing `runmoor.service` file outside the installed definition, or any Runmoor-specific or service-type drop-in directory, makes the effective definition ambiguous and fails closed. Missing, symlinked, foreign-owned, malformed, duplicate, or ambiguous definitions fail with `CONFIG_INVALID` before contacting a manager, opening offline state, invoking an OS service command, or deleting the definition. Stop/uninstall retain the validated file identity and contents, revalidate them after draining before contacting the service manager, and revalidate again immediately before deletion; a changed or replaced definition is preserved. Parse the plist and systemd argument syntax as structured data; never interpret these definitions through a shell or substring search. Diagnostics do not disclose either configuration path.

### Service version reload (unreleased)

`reload` obtains the running version from a live `/v1/control` response. Offline
status reports the CLI version and cannot authorize an upgrade. Linux
`SO_PEERCRED` and macOS `LOCAL_PEERPID`/`LOCAL_PEERCRED` identify the response's
same-user process without changing JSON schema v1. A service main PID matching
that peer must also match the securely parsed definition's exact invocation.
Foreground managers retain ordinary reload behavior, including when another
configuration has an installed service. An unreachable manager does not start a
stopped service. Stable SemVer triplets compare numerically; equal/newer managers
reload configuration without changing their service definition or downgrading.

Without a pending recovery journal, failed native PID inspection falls back to
ordinary reload of the authenticated socket peer. Bind that request to the PID
observed by status and return its result unchanged, including candidate-validation
and Stop errors. This fallback acquires no service lock, writes no definition or
journal, and performs no native mutation. A changed or unreachable peer remains
an error. Log only a structured diagnostic with the platform and safe error code;
omit raw native errors and output. Pending-journal recovery retains its native
inspection requirements and fails closed when inspection is unavailable.

Before replacement, the newer CLI reads state without migration and applies the
same whole-candidate capacity, backend and remote validation as manager reload.
Managed pools defer image preparation as before. Verify the installed executable
reports this CLI's version/revision. Validate the original definition's file
identity and contents, and the original manager's PID/start identity. Stage an
owner-only target definition with a recorded file identity. A private
`.runmoor-service.lock` beside the unit serializes install/start/stop/uninstall
and service reload. A private `.runmoor-service-reload.json` journals the
installation, configuration/storage references, original/target definitions and
file identities, native process identity, UUID-v7 token and typed operation stage.
It contains no credentials and adds no public field or SQLite migration.


Publication has its own private typed stages: prepared, claim-pending, claimed,
publish-pending, published, restore-pending and restored. Derive the claim name
from the journal's UUID-v7 token beside the canonical definition. Persist claim
intent before moving the canonical file with a no-replace rename. Sync the moved
file and directory, then record its exact identity and SHA-256 digest. Unknown
external bytes remain in the private claimed file, outside journal content. Only
the captured original may authorize publishing the verified staged target, also by a
no-replace rename into the vacant canonical path. Darwin uses `RENAME_EXCL`;
Linux uses `RENAME_NOREPLACE`. Unsupported operations fail closed; no replacing
rename or exchange fallback is allowed. Sync each rename's directory before
recording its observed outcome. Verify the complete target at the canonical
path before native replacement on either platform.

A changed claim or an occupied publication destination aborts before native
replacement. Record restoration intent and restore the captured definition only
with a no-replace rename into a vacant canonical path. A later writer remains
authoritative; retain its canonical file, the claim, staged bytes and journal on
conflict. Interrupted claim/publication/restoration resumes from the token,
recorded identities and verified files, including when the canonical path is
temporarily absent. Symlink, permission, identity, byte or sync uncertainty
retains private intent for retry or operator reconciliation. Legacy private
journals remain supported conservatively: a published target permits only a
missing staging file or the exact displaced original; unknown staging bytes
block native replacement. Successful completion retains the displaced original
as a private claim file and retires the journal. Unjournaled retained claims
grant no authority to publish, restore or delete a definition; automatic
pathname-based deletion could race another writer.

The journal records the reload CLI's PID and process-start identity as its
completion owner. While holding the service-operation lock, an authenticated
retry atomically replaces that identity with its own verified identity through
the exact-record private-file checks. Transfer ownership before resumed native
actions, replacement readiness checks or completion, including when the target
is already ready. Identity or journal-update failure preserves recovery intent
and permits no native mutation. Replacement startup retains the journal while
the current owner is live. A confirmed exited owner permits reclamation; legacy
records without an initiator remain conservative until recovery claims them.
The retry removes its journal only after readiness and final normal reload
acceptance succeed.


Linux atomically publishes the target definition, performs `daemon-reload`,
revalidates the original active invocation against its captured definition, and
uses `systemctl --user kill --kill-who=main --signal=SIGKILL runmoor.service`.
Existing `Restart=on-failure` starts the replacement; `KillMode=process` remains
unchanged. This path does not issue service restart, manager Stop, or drain.
Session selectors remain limited to the documented `systemctl` environment.
macOS uses `launchctl debug --program` with an exact one-run private
`__service-reload-handoff` invocation, then `kickstart -k`. The harmless helper
reads only its private authority record, keeps no manager state and waits for
unload or user-session termination. It remains available if the initiating CLI
is interrupted. Verify its exact arguments before `bootout`, confirm a reachable
GUI domain, reverify the already published target plist, then `bootstrap`. This
replaces launchd's cached executable as well as its on-disk reference;
`AbandonProcessGroup=true` continues to protect independent executions. An inactive unverified loaded job
or a changed native/definition identity remains an error.

The actual replacement service and a verified restart of the exact previous
service invocation/version use the journal-owned snapshot to load the last
committed requested configuration before reading the candidate TOML. Require
matching journal platform, unit, configuration path and snapshot installation;
an unavailable or mismatched snapshot fails before startup acceptance. Changed,
malformed or relocated candidate TOML cannot authorize this startup. Both
startup paths preserve durable Stop and pool/global pause decisions. The previous
manager retains the handoff journal and receives no retirement authority; only
replacement startup returns a retirement handle. The initiating CLI keeps its
existing completion authority. Foreground runs and no-journal explicit Start
retain ordinary candidate loading and completed-Stop recovery.

Complete startup acceptance and session reset before exposing control; entering
the manager loop must not overwrite a reload or Stop accepted after readiness.
Independent executions, original generations, reservations, deadlines, image
operations and uncertain cleanup reconcile through their existing ownership
boundaries. A final normal reload revalidates and atomically accepts the candidate.
The existing two-minute CLI context covers preflight, native replacement,
readiness and configuration acceptance. Success requires the new native
invocation, matching live socket peer/version, and non-stopping status.

After an interrupted replacement honors Stop and exits, one later explicit
`service start` must resume the inactive target service. Under the existing
service-operation lock, verify the private journal, matching installed CLI,
installation, configuration/storage references and exact target definition
contents/file identity. Confirm the reload initiator has exited or its PID now
has a different nonempty process-start identity; permission errors, unreadable
identities and legacy journals without initiator authority do not confirm exit.
Acquire exclusive manager-state ownership without creating or migrating the
database, then require durable Stop and complete runner, image, artifact and
host cleanup through the existing completed-stop boundaries. Recheck native
inactivity, initiator and definition authority before exact-record journal
retirement. Retire before native Start so ordinary startup acceptance clears
Stop on the first attempt while retaining pool/global pauses. Active services,
unknown ownership, changed definitions and pending cleanup fail closed before
native mutation and preserve recovery intent and reservations. Generic manager
startup must never clear Stop merely because the initiating CLI disappeared.
Structured recovery logs use safe event names and codes without private paths
or native output. This recovery behavior is part of the unreleased service
version reload workflow and adds no public field or migration.

Persist intent before native actions. After interruption, observe a matching
replacement or the journaled helper before continuing; never blindly repeat a
kill/bootstrap or operate on another generation. Failures retain the last
committed configuration and private recovery intent, even if the service
executable has already changed. Retry with the same installed CLI/configuration
after inspecting status and the user service. Unknown identities require
operator reconciliation. Never automatically restore an older binary after
state migration. Structured logs record versions, stages and safe error codes;
they omit private paths, raw arguments, environment values and native stderr.

Tests inject native services and dependencies, exercise real Unix peer identity,
and preserve busy execution state across the replacement fixture. Supported
target builds and native child-process fixtures do not certify actual launchd,
systemd user-session replacement or live GitHub/Docker/Tart/host jobs.

## Storage

- Config: `$XDG_CONFIG_HOME/runmoor/config.toml`, otherwise `~/.config/runmoor/config.toml`.
- State: `$XDG_STATE_HOME/runmoor`, otherwise `~/.local/state/runmoor`.
- Data: `$XDG_DATA_HOME/runmoor`, otherwise `~/.local/share/runmoor`.
- TOML may override absolute state/data directories. The Unix socket path must fit macOS's length limit. Directories are mode 0700; files/socket are 0600. Reject symlinks and foreign ownership at sensitive file boundaries.
- SQLite schema v1 stores an atomic snapshot row under WAL/FULL durability: installation identity, configuration generations/references, pool/session metadata without tokens, runner lifecycle and reservations, image revisions, and cleanup progress. A nonblocking file lock excludes concurrent manager or offline state access.
- SQLite opens the exact absolute `state.sqlite` filename through an escaped `file:` URI, preserving literal `?`, `#`, `%`, spaces, and Unicode in valid filesystem paths. For paths containing `?`, if the intended database is absent or empty while the prefix used by older raw-DSN opens is a regular file, startup fails before creating the state directory, data directory, manager lock, or database. The error omits private paths and requires stopping all affected managers and preserving complete backups of both locations before explicit recovery; Runmoor never adopts, moves, or deletes the ambiguous legacy database automatically.
- Delete completed execution history after seven days. Preserve unresolved cleanup and ownership indefinitely. User-created sealed images remain until explicit deletion; generated revisions use reference-aware managed retention. SQLite upgrades atomically in v1-to-v2-to-v3-to-v4 order; other unsupported versions are rejected.
- Storage relocation is accepted only after all executions complete cleanup and all image setup/removal operations close; it atomically rebinds retained generations while preserving installation ownership. Backup only after drain and stop: preserve the complete state and managed data directories, protect referenced credentials separately, and pair backups with a compatible binary. Installing Runmoor binaries and rollback remain manual; explicit service reload may advance a running owned manager to that installed binary; never open a newer state schema with an older binary.

## Security

Trust is limited to developer/team workflows. GitHub runner-group/repository access policy is never replaced. Management credentials remain host-only environment or permission-restricted file references. No credential/JIT values enter SQLite or logs. Unknown upstream errors are converted to bounded safe classifications; raw errors are not wrapped into public messages. Cleanup requires exact owned identity, and unresolved ambiguity remains visible rather than being guessed away.

## Logging

Use `log/slog` text or JSON for lifecycle transitions, preparation duration, retries, capacity/dependency failures, cleanup results and power warnings. Include only safe identifiers and stable codes. Preserve a structured diagnostic record before destroying an execution; never copy raw workflow output or raw runner logs. Enforce both seven-day and 256 MiB retention across local diagnostics. No remote telemetry is emitted.

## Build and Test

- Run formatting, `go vet ./cmds/runmoor/...`, `go test ./cmds/runmoor/...`, and supported-host `go test -race ./cmds/runmoor/...`.
- Ordinary tests use an in-process TLS GitHub API substitute with the real scale-set SDK, deterministic backend/session adapters, temporary private state, and no live credentials or user-service installation. Systemd user unit search-path tests run only on Linux. Archive download/repack fixtures and the opt-in official archive download run only on Darwin/Linux because private file creation requires Unix ownership checks. Windows CI still runs portable release metadata and asset-selection validation; unsupported-host storage continues returning `PLATFORM_UNSUPPORTED`.
- Guest-validation shell fixtures allow 15 seconds for real process startup on loaded hosts. Invalid-image cases still assert exactly one validation call, and the dedicated transport-timeout case retains its 50 ms deadline; production readiness deadlines are unchanged.
- Opt-in Docker tests exercise real local containers, DinD builds/actions/service connectivity, quotas, restart adoption, cancellation and ownership-scoped cleanup without requesting GitHub jobs. Opt-in Tart tests require explicitly supplied operator-owned clean input and are never automatic.
- Preserve root Ubuntu/macOS/Windows Go CI. Generate ignored administrator assets with `pnpm --filter devhud-admin build:embedded` before root Go tests/vet. Runmoor public guide changes run `pnpm --filter public-docs test`, which is also the validation command for the shared public site.
- `scripts/release/runmoor.mjs` builds deterministic `runmoor-darwin-arm64.tar.gz`, `runmoor-linux-amd64.tar.gz`, and `runmoor-linux-arm64.tar.gz`, each containing the executable, English README, and license. SHA256SUMS covers exactly those three archives. Publication signs each archive and the checksum file with separate Sigstore bundles and verifies the exact executing workflow identity before upload.
- `.github/workflows/runmoor.yml` adds read-only opt-in-path Docker integration without altering existing CI jobs or development ports. `.github/workflows/release-runmoor.yml` supports `runmoor@v<MAJOR.MINOR.PATCH>` tags and manual dispatch. Dry-run stages contain no signing or publication authority; releases use the stable channel.
- Draft discovery enumerates every releases-list page rather than using GitHub's published-release-by-tag endpoint. More than one matching release, failed/malformed pagination or a conflicting source/channel fails before signing. Enumeration is bounded at 1,000 full pages; exhausting that bound is an error, never proof of absence.
- Pin the numeric ID returned by discovery or draft creation. Every subsequent draft verification reads by ID and rechecks tag, source revision, stable channel and draft state. Final verification downloads all eight assets, checks exact names/sizes/SHA-256 digests and uploaded state, verifies checksums and all four Sigstore bundles, and retains the verified manifest outside the upload directory. Immediately before the ID-based publication PATCH, enumerate all release pages again and require the pinned ID to remain the sole same-tag candidate, then recheck its draft and manifest. Restore canonical release notes and require the publication response to confirm the same release and inventory. Never automatically retry an uncertain publication write.
- Structured release logs identify stage, tag, release ID and stable failure classification without logging credentials or upstream error bodies. README/public installation examples bind the download and signing identity to the selected tag and verify the checksum before extraction.
- Release fixtures run without installed workspace packages; YAML workflow assertions run under the dependency-installed `pnpm ci:contracts` suite in `scripts/ci/runmoor-release.test.mjs`.
- Release validation builds three architectures and verifies archive inventory/checksums, strict tag parsing, stable-channel marking, exact draft asset names/sizes/SHA-256 digests, prior signed-bundle reuse, partial-draft completion, canonical release-note restoration, secret-free dry run and publication isolation. Real keyless signing occurs only in publication jobs with job-scoped permissions.

### Homebrew distribution

- `delinoio/homebrew-tap` owns the published `Formula/runmoor.rb`; users install `delinoio/tap/runmoor`. The repository template is `packaging/homebrew/templates/runmoor.rb.tmpl`, rendered through `scripts/release/update-homebrew.sh`. It installs the existing prebuilt macOS archive, README and license with `depends_on arch: :arm64` and `depends_on macos: :sonoma`. No Intel/Linux Homebrew support, source compilation, Tart dependency, configuration mutation or Homebrew service definition is introduced.
- `.github/workflows/release-runmoor-homebrew.yml` runs after successful GitHub release publication and can be dispatched on `main` for an already-published exact version and 40-hex source revision. Manual dispatch defaults to verification-only `dry_run=true`. This allows initial `0.1.3` publication and later recovery without a new version, tag movement, archive rebuilding, re-signing or changing the existing release.
- `scripts/release/runmoor-homebrew.mjs` resolves lightweight or annotated tags, checks the version at the tagged source commit and requires a public stable release bound to that commit. It downloads all eight assets anonymously from canonical version URLs, checks exact names, uploaded state, sizes, API digests, archive inventory and SHA256SUMS, and verifies all four Sigstore bundles. The certificate must use the owning release workflow's exact tag or `main` identity, GitHub Actions issuer, repository and exact workflow source SHA. Failed or ambiguous verification stops publication.
- The ephemeral macOS 15 ARM64 runner installs the rendered Formula in a temporary validation tap, runs `brew test` (including the installed Apache license text), `brew audit --strict`, and checks the exact version/revision. The Formula retains its macOS 14 minimum independently of the CI image. Only after validation does it mint a fresh `homebrew-tap`-only bot token. Before pushing it repeats public release verification and requires identical Formula bytes. The shared publisher rejects version downgrades and same-version byte changes; identical retries do not commit. After pushing, install and test again from the public tap. User Homebrew installations and services are not used for these checks.
- The ordinary release dry run renders the Formula from verified unsigned local archives without publication credentials. Homebrew-only dry runs verify already-public signatures but cannot obtain tap credentials or sign anything. Logs identify the tag, verification/publication stage, release ID and safe failure code.
- Run `node --test scripts/release/runmoor-homebrew.test.mjs scripts/release/runmoor.test.mjs scripts/ci/runmoor-release.test.mjs`, `pnpm ci:contracts`, `pnpm ci:workflows` and the public-docs tests. Fixtures cover wrong source/channel, lookup failures, missing/altered assets, signature failure, unsupported URLs/platforms and idempotent/downgrade/conflict tap retries.

### Implementation validation (2026-09-18)

- Passed root `go test ./...` and `go vet ./...` after generating the administrator embed bundle; passed Runmoor unit/lifecycle/API tests and native macOS arm64 race tests.
- Passed real local Docker plain and DinD integration: nested build, container-action workspace/externals mounts, service-container network and localhost access, CPU/memory configuration, fresh-adapter recovery, cancellation, repeated cleanup, and preservation of unrelated resources. No GitHub job was assigned in this validation.
- Built and inspected all three native release archives, verified their exact inventory and SHA256SUMS, and ran the packaged macOS arm64 and Linux arm64 version commands. Windows amd64 command and test binaries cross-compiled successfully; Windows execution was not performed locally.
- Passed public-docs `pnpm test`, workflow lint, 13 CI contract tests, and all 150 release fixture tests in a local Linux container. The Linux run supplies the Debian packaging utilities and GNU tar required by existing unrelated release fixtures.
- Live GitHub authentication/job assignment, real local Tart execution, actual user-service installation, real signing, tag pushes and public release publication were intentionally not performed. Their implementation and test seams remain available; the stable release must continue to disclose those live verification limits.

### Completion/registration race validation (2026-09-19)

- Reproduced issue [#903](https://github.com/delinoio/oss/issues/903) with failing deterministic tests before the fix: completion before worker start or during lookup was overwritten by missing-registration quarantine, including its obsolete warning.
- Passed `go test ./cmds/runmoor/...`, native macOS arm64 `go test -race ./cmds/runmoor/...`, and `go vet ./cmds/runmoor/...`. The new inspection regressions also passed ten repetitions under the race detector with channel-controlled interleavings and no timing sleeps.
- Coverage includes completion/absence event ordering, terminal and forced cleanup preservation, pruned records, commit-failure diagnostics, actual ownership-error classifications after completion, conservative live absence/transient-error handling, GitHub busy rejection, and host-concurrency-one handoff to a second pool while local or remote cleanup remains pending. Cleanup retries finish without force-stop or job reruns.
- Passed public-docs `pnpm test`, including the production build and clean-URL/content validation. This fix's validation used temporary state and deterministic adapters; no live GitHub job assignment, real Docker/Tart execution, user-service installation, signing or release publication was performed. Existing compatibility limits remain in force.
- Merge validation with the documentation move (#899) and runner freshness fix (#904) preserved both validation records and the recovery guidance in `apps/public-docs/docs/runmoor`. The ordinary inspection regressions, full Runmoor race tests and vet, consolidated `public-docs` validation, and all 25 CI contract tests passed. Ordinary Go suite retries and an isolated guest-validation run hit the unchanged fixture's two-second deadline on different cases; that fixture and its production validation code match `main` and were not modified for this repair.

### Runner freshness validation (2026-09-19, issue #902)

- Reproduced rejection of the valid 4,144,596-byte latest-100 response before the fix. Deterministic in-process HTTP transport tests now accept the compact fixture, 2 MiB, 2 MiB + 1, 4,144,596 bytes and exactly 8 MiB; 8 MiB + 1 and 9 MiB responses return an explicit size-limit warning while reading at most 8 MiB + 1 bytes.
- Verified stable-release filtering, latest pins, 29-day update warnings, 31-day/unknown-pin rejection, and the oldest newer release's update window. Malformed/truncated JSON, trailing data, multiple JSON values, read failure after a valid prefix, non-200 responses, transport failure, cancellation and expired deadlines return safe diagnostics. Tests assert request identity/context, response-body closure and secret-free error/JSON output.
- Passed Go formatting checks, `go vet ./cmds/runmoor/...`, `go test ./cmds/runmoor/...`, and `go test -race ./cmds/runmoor/...` with Go 1.25.7 on macOS arm64. Initial concurrent ordinary/race runs exceeded the existing guest-validation fixture's two-second readiness timeout; both complete suites passed when rerun sequentially without changing that fixture.
- This validation uses no live GitHub request, credentials, Docker/Tart execution or user-service installation and does not expand the stable release's compatibility certification.

### Release draft recovery validation (2026-09-28)

- Release fixtures cover GitHub's draft-by-tag 404 behavior, paginated discovery, duplicate candidates, failed/malformed lookups, numeric-ID pinning, concurrent duplicate/replacement/disappeared drafts before publication, source/channel/publication-state changes, missing/altered/unfinished assets, exact-ID publication and uncertain write outcomes without retries. Signature fixtures continue to reject invalid identity and signature verification failures.
- Passed the Runmoor release fixtures and workflow contract, all 93 CI contract tests, workflow syntax validation, public-docs tests, Runmoor unit/race tests and vet. Installation examples pass Bash/Zsh syntax checks and workflow inline JavaScript passes Node syntax checks.
- Built all three unsigned platform archives, verified inventory and checksums, and executed the packaged macOS arm64 version command. Temporary build output was removed. A read-only live discovery using the corrected implementation found the existing `runmoor@v0.1.2` draft (ID `392298946`) and its eight uploaded assets without changing it.
- These checks do not certify live GitHub runner assignment or real Tart execution. Public release/download/signature evidence is recorded separately after publication.

### Public release recovery evidence (2026-09-28)

- [PR #1001](https://github.com/delinoio/oss/pull/1001) merged the draft lookup and ID-bound publication repair after all required CI checks and automated review passed. [Release Project run 36362584715](https://github.com/delinoio/oss/actions/runs/36362584715) created `runmoor@v0.1.3` at `514ff0a2af4c79a9448b3148c512e416e0b00cf6` through the release bot.
- [Release Runmoor run 36362626105](https://github.com/delinoio/oss/actions/runs/36362626105) successfully built, signed, downloaded, verified and published [Runmoor 0.1.3](https://github.com/delinoio/oss/releases/tag/runmoor%40v0.1.3), release ID `397873289`, with `draft=false`, `prerelease=false` and exactly eight assets.
- Independently downloaded all eight public assets without authentication. Their sizes and SHA-256 digests matched the release metadata; archive inventory and `SHA256SUMS` verification passed. Cosign `v3.1.3` verified all four bundles against the exact `release-runmoor.yml@refs/tags/runmoor@v0.1.3` identity and GitHub Actions OIDC issuer. The temporary cosign binary's digest was checked against its upstream release metadata.
- Extracted the public macOS arm64 archive in a temporary directory and executed `runmoor version`, which returned `runmoor 0.1.3 (514ff0a2af4c79a9448b3148c512e416e0b00cf6)`. Temporary downloaded archives and extracted executables were removed; no user installation or service was changed.
- The same release workflow completed all 60 jobs successfully, including native stable-repository publication and all 26 public Linux installation checks. Native package availability and evidence are recorded in `docs/repository-linux-packages-contract.md`.
- Confirmed preservation of the immutable, asset-free `runmoor@v0.1.1` release (ID `392272909`) and the unpublished `runmoor@v0.1.2` draft with eight assets (ID `392298946`). This release verification does not certify live runner assignment or real Tart execution.

### Linux user-session service environment validation (2026-09-29, issue #1010)

- Service environment fixtures preserve each supplied selector exactly, including spaces and additional `=` characters in the bus address; cover both selectors, each selector alone, and both absent. They verify unrelated fixture credentials stay out of the child environment and selectors are limited to Linux `systemctl` commands.
- The service lifecycle fixture captures child environments across install, start, stop and uninstall, including both `daemon-reload` calls. A Linux-only failed-start fixture checks the safe dependency diagnostic, selector preservation, credential exclusion and preservation of the installed definition.
- Passed `go test ./cmds/runmoor/...`, `go vet ./cmds/runmoor/...`, targeted race tests for the service environment/lifecycle fixtures, and Linux amd64 test-binary cross-compilation. The full race suite passed once on retry, but a later full run again hit the existing guest-bootstrap readiness timing checks in `TestGuestBootstrapConfirmsRunnerBeforeResettingPreparationFailures`; no service test failed.
- Validation ran on macOS, so the Linux-only service lifecycle/failure fixtures and a read-only connection check against a logged-in Ubuntu user manager were not executed locally. No service was installed or changed on the host.

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
- [Tart releases](https://github.com/openai/tart/releases), [Guest Agent 0.14.2](https://github.com/openai/tart-guest-agent/tree/v0.14.2).

## Automatic defaults and runner management

Runmoor 0.2.0 extends TOML v1 with optional resource and concurrency
fields. Only omitted values receive defaults; explicit zero and negative values
remain invalid. Host CPU and physical memory are detected at startup and reload.
Docker has an additional engine CPU/memory ceiling, shared by all Docker jobs and
preparation containers without restricting unrelated Tart capacity. The default
Docker runner uses 2 CPUs and 4096 MiB; Tart uses 4 CPUs and 8192 MiB. Omitted
allocations shrink to the available budget on smaller machines, with automatic
minimums of 1 CPU/1024 MiB and 2 CPUs/4096 MiB respectively. Existing explicit
allocations retain their meaning. Pool concurrency is the minimum of the CPU and
memory quotients, bounded by the two-VM Tart limit; global reservations include
all pools and image preparation. DinD uses runner-only admission CPU and combined
runner/daemon admission memory. Omitted runner CPU uses the full applicable host
and engine CPU budget without subtracting or adding back daemon CPU. Disk reserves default to 10240 MiB, or
20480 MiB when Tart is configured. Warm capacity defaults to zero.
GitHub session polling and job acquisition apply the same host and Docker engine
CPU/memory ceilings with the same DinD admission cost, even when an explicit pool concurrency is
larger. Acquisition rechecks current eligibility and capacity after polling so a
concurrent capacity reduction cannot admit work under the previous ceiling.

`init` creates a minimal valid configuration through terminal prompts or flags.
It accepts target, backend, authentication and credential *references*, plus an
image/source when needed. It never overwrites an existing configuration, accepts
credential values, installs host dependencies or starts a service. `--image-only`
creates a configuration for manual initial macOS setup. `config show --resolved`
reads committed versions and resolved capacity without preparing images.
Offline `doctor` also inspects each managed pool's committed current image and
connection. Requested `latest` or image-source settings do not imply pending
preparation when that environment exists. It reports one managed-state check,
retains genuine pending/expired/invalid-image failures, and never downloads or
replaces an image during diagnosis.

Omitted `runner_version` and `runner_version = "latest"` select automatic runner
management. An exact version remains a pin. Docker image omission selects the
official image; explicit Docker sources remain immutable digests. Tart supports
an existing sealed UUID or mutually exclusive `image_source = { from = "...",
source_home = "..." }`, using the existing import sources. Imported sources are
resolved once and retained as immutable local bases; OS/toolchain refresh is
not implied by runner refresh. Custom Docker sources and Tart bases are cloned
before replacing the dedicated runner directory. User sources are never edited.

The first successful Tart import commits an immutable `imported` source revision
before any guest mutation. It is a clone source, not a runnable sealed revision;
open/seal in place is prohibited. Installation retries and restarts reuse its
recorded digest even if the external name/export changes. Import and candidate
preparation transfer a single reservation atomically instead of consuming two VM
slots. Explicit source changes reset the source selection.

The manager checks official stable releases at startup and hourly. It shares
bounded latest-100 metadata, verifies architecture and installed version, pins
Docker pulls to digests, and verifies the SHA-256 of downloaded runner archives.
Repacking rejects traversal, escaping/chained/directory links, special files and oversized archives; validated relative links to regular in-tree files are emitted after every regular entry. Image
preparation is credential-free and never registers a runner. `runner update
[--pool NAME]` requests an immediate check. `image create` resources are optional;
`image seal` with an omitted version installs the latest runner before sealing.

Requested configuration and effective generations are separate. Verified
candidates activate atomically through the existing drain/retirement boundary;
old jobs keep their original images, resources and deadlines. Pause, drain and
stop remain authoritative. A waiting update gets the next available preparation
slot without interrupting busy jobs. Failures retain the last verified image and
retry with backoff. Known support deadlines block new acquisition after expiry;
unknown release freshness is reported without pretending a cached image is
current. Initial preparation failure has no runnable fallback. For an already
managed suspended pool, a validated reload retains its failure-specific recovery
decision until the matching desired candidate is verified and published. A later
unrelated reload may keep that decision while the original correction remains;
the first decision compares the request with the failed committed pool even when
an earlier managed request is still pending. Managed image/version fields are
resolved during candidate preparation, so their committed values alone do not
count as a configuration correction; verified image replacement handles them.
Stale candidates, changed failures and operator pause/stop cannot use it. This
durable decision is private state and does not change status or doctor JSON.
An unchanged release still requires bounded read-only validation of its committed
image. Missing or invalid images are prepared again and the candidate is validated
before activation. Verified repair clears only a matching image/version suspension
or a three-failure startup preparation circuit breaker;
authentication, ownership and other failures remain suspended, and operator
pause/drain/stop or concurrent reload cannot be undone. Transient validation
failures preserve the current artifact and retry with backoff.

SQLite v2 added managed-pool state, immutable artifact identities, release
metadata and preparation/cleanup reservations. SQLite v3 adds private Tart
process-start identities alongside persisted process IDs. Opening v1 migrates
its snapshot and `user_version` atomically to v2, then applies the atomic v2 to
v3 migration without rewriting installation ownership or execution history.
Opening v2 atomically adds the v3 identity fields while preserving legacy
numeric-only Tart records; those records remain conservatively reserved while
their recorded PIDs exist. SQLite v4 adds private host directory and execution journals with an atomic v3-to-v4 migration. Earlier binaries reject v4. Read-only configuration
inspection does not migrate state. Public
status/doctor JSON remains schema v1 with additive managed runner and artifact
fields. Generated artifacts retain current, previous and referenced bases;
cleanup never force-removes externally referenced Docker images or deletes user
sources. Official pulled base layers remain Docker-owned.

An unavailable Docker capacity check at startup leaves only Docker acquisition
pending until a bounded retry succeeds; cached capacity does not authorize new
work before verification. Successful capacity resolution stays fixed until the
next start or reload. Paused pools continue hourly release metadata checks while
preparation remains deferred. Scoped resume preserves other pending pool pauses.
Known expiry retires idle capacity through busy-aware removal and never interrupts
busy work. Provider rate-limit deadlines are shared across pools. Failed owned
artifact cleanup retains reservations with backoff, and collection runs even
after all managed pools have been removed. Storage relocation rejects pending
managed preparation and cleanup.

### Automatic management validation evidence (2026-09-28)

- Passed `go test ./cmds/runmoor/...`, macOS arm64 race tests and
  `go vet ./cmds/runmoor/...`. Deterministic fixtures cover omissions/explicit
  zero, small hosts, partial overrides, independent Docker ceilings, mixed
  pools/DinD, TTY/non-TTY init, migration preserving ownership and executions,
  release ordering/rate limits, missing publication, checksum and archive
  validation, cancellation, stale activation, scoped pause/resume, source reuse,
  known expiry, interrupted cleanup and current/previous/live retention.
- Passed real local Docker plain/DinD execution-adapter integration and managed
  image preparation on the native arm64 engine. Managed integration pulled and
  verified official runner 2.337.0 by digest, then replaced only its runner with
  a checksummed fixture archive in a separate owned image; the source stayed
  unchanged and preparation containers were removed. No live GitHub job or JIT
  registration was performed.
- Downloaded the official runner 2.337.0 macOS arm64 archive (127,732,571 bytes),
  verified its published SHA-256, and passed safe tar reconstruction including
  its internal Node executable symlinks. This does not certify guest boot or
  Guest Agent compatibility.
- Tart import/clone/install/seal/cancellation/cleanup use deterministic command
  fixtures. Real opt-in Tart validation was unavailable because this host has no
  Tart installation; live GitHub and Tart execution remain unverified.
- Windows amd64 and Linux arm64 CLI cross-compilation passed; these are build
  checks, not runtime certification. Public documentation `pnpm test` passed
  the consolidated build, route/content checks and validator fixtures.

### Guided macOS first setup

- Interactive Tart `init` offers creation from `latest`, meaning Tart's
  host-supported Apple IPSW, or an absolute local `.ipsw`, alongside the
  existing prepared-source choice. `image create --ipsw latest` uses the same
  Tart option. Noninteractive init still requires an image/source reference or
  explicit `--image-only`. Tart remains operator-installed at a stable 2.x.x
  release.
- Guided init stages a private image-only configuration and a private atomic
  setup journal keyed to the requested final config path and UUID-v7 image.
  It allocates distinct state and data directories for that UUID and records
  them in the final configuration, so starting the setup manager cannot load
  or mutate a stopped installation's default database. Existing directories
  at those setup paths are rejected before journal publication.
  Tart image-only setup keeps the 20480 MiB free-disk reserve even before a
  pool exists.
  The normal manager owns image mutations and sleep inhibition. After the
  operator finishes the non-root runner account, Guest Agent 0.14.2 login RPC
  and optional tools, Runmoor validates the guest, cleanly stops and boots it
  headless to verify automatic Agent readiness, installs the checksummed
  current runner and seals the image. It creates the final TOML exclusively
  after manager shutdown, using that sealed UUID with latest runner management.
- The operator explicitly confirms guest setup completion so Agent startup
  cannot seal an image before optional tool/Xcode preparation finishes.
  Repeating interactive init resumes the journaled owned revision. Ambiguous
  VM creation or ownership, altered staged configuration and conflicting
  manager/storage state fail visibly; neither a source image nor an existing
  final config is overwritten or adopted. Aborting a wizard cleanly stops an
  open setup VM and retains the journal for retry.
  On resume, the complete normalized staged final and bootstrap configurations
  must match configurations regenerated from the private journal; changes to
  paths, resource budgets, labels or other fields are not adopted.
  A durable successful image-creation marker is required before reopening a
  journaled VM; a failed or interrupted Tart create cannot be cleared merely
  by a later open request.

## Opt-in macOS host execution

Issue #1312 extends the closed backend enum with `host` for macOS 14+ arm64.
`init --backend host` creates `macos-host` with scale set `runmoor-macos-host`
and labels `runmoor-macos-host`, `macOS`, `ARM64`. Defaults still select Tart on
macOS and Docker on Linux. Loading and reload never add or replace authored labels.
Host configuration rejects image, image-source, guest runner-path, DinD and
image-only setup options. `timeouts.host_preparation` extends TOML v1 and defaults
to `5m`. Effective internal host image references are generated distribution IDs;
operators cannot configure execution paths or select an arbitrary distribution.

Host admission uses the shared scheduler, reservations, low-disk checks and fair
allocation. Omitted resources use 2 CPUs/4096 MiB, shrinking to 1 CPU/1024 MiB.
Explicit allocations are never reduced. Host concurrency uses CPU/memory quotients
without the Tart two-VM ceiling. Minimum idle defaults to zero; disk reserve is
10240 MiB, or the existing higher 20480 MiB when Tart is configured. CPU/memory
reservations govern admission and do not enforce process usage limits.

Official macOS arm64 runner archives use the existing stable release selection,
SHA-256 verification and bounded safe tar reconstruction. Downloads and extraction
use opened, confined distribution directories. Preparation checks the installed
version before sealing a digest. Latest and exact pins share managed generation
publication, support expiry, retry and reference-aware retention. Each execution
copies its original verified distribution into a fresh installation, creates
separate HOME, temporary and work directories, and receives fresh JIT registration.
The manager never edits an active installation or distribution. Host-only
configurations do not probe Docker, Tart or Guest Agent.

The detached native supervisor is launched through an inherited directory
handle. Darwin private child entrypoints validate that descriptor's private
owner marker and device/inode identity, use `fchdir`, and open the confined root
from their resulting cwd. `/dev/fd` is never used as a traversal or cwd path;
the manager's cwd never changes. A private runner exec helper opens the runner
directory without following a symlink, rejects linked fixed entrypoints, and
replaces itself with the fixed version-check or worker executable. Imported
dependency symlinks remain subject to the existing archive/digest confinement.
Execution HOME/temp derive from the physical descriptor-selected root, including
after a rename. The helper retains PID/start/group identity through exec and
rechecks durable unforced Preparing authority immediately before worker exec.
JIT data travels through private stdin pipes and remains memory-only.
A bounded anonymous close-on-exec outcome pipe carries only closed failure
classifications. Subprocess stderr is discarded; structured failure logs retain
only the action and safe reason. Execution/setup failures use existing
`PREPARATION_FAILED` (or ownership/stale authority errors); a successful version
command reporting a different version uses `RUNNER_VERSION_UNSUPPORTED`.
Public CLI, TOML, SQLite and versioned JSON shapes remain unchanged.
Each launch checks cancellation and the durable unforced Preparing phase. The
supervisor rechecks that phase before starting the runner. Preparation retries
a confirmed absent initial supervisor status within its existing deadline.
Malformed, mismatched or unsafe status files fail immediately with ownership
errors; a missing status after readiness is also an ownership failure. These
failures preserve reservations and authorize no signal or deletion. Confirmed
immediate exits still fail. It observes startup before publishing readiness,
owns a separate runner process group, and continues
across a manager-only restart. Process identity is the kernel PID, group and OS
start time. The bootstrap deadline bounds preparation only. An empty successful
Darwin PID sysctl result confirms process absence; actual inspection errors
remain conservative. Idle host runners
have no job deadline. Assignment establishes the persisted busy-job deadline;
busy-aware removal uses the existing creation-time bound for an unknown start.
An established job deadline remains authoritative across manager restarts. Process
supervision reads deadline state at startup confirmation and at most once every
five seconds, with an additional authority check when its cached deadline timer
fires. The timer enforces an observed deadline independently of the polling cadence.
Process names and command arguments confer no ownership. Completed,
cancelled and timed-out executions terminate only verified group members. Missing
supervisors, changed process identity and uncertain termination retain reservations
and actionable recovery. Daemons escaping the managed group are unsupported.

SQLite v4 keeps directory creation intent, installation/execution identity,
owner-only markers, device/inode identity, supervisor/worker identity and cleanup
progress in private journals. Ownership checks reject foreign, replaced and
escaping paths. Markerless interrupted publication is preserved. Cleanup moves
the verified directory without replacement, removes content through the opened
root, and commits final removal intent before deleting its marker. Repeated
cleanup validates the private host parent and each direct ancestor before any
committed-absence proof. It binds child verification, no-replace staging and final
removal to that same opened parent, rejects symlinks and unsafe or foreign-owned
parents without repairing them, and preserves ownership on failure. Confirmed
absence in a verified parent remains idempotent. Public status/doctor remain JSON
v1 and do not expose these private journals. Seven-day completed history and existing
seven-day/256 MiB redacted diagnostic limits remain unchanged.

Copied distribution directories may receive new device/inode identities only
under the exclusive startup lock after a completed stop or drained storage
relocation. Require all execution cleanup and image operations to be complete,
every artifact to be ready and unreserved, and matching installation/token,
artifact/version and runner digest before rebinding the marker and SQLite record.
A marker committed before a failed state write can be retried at that same
boundary. Live restart, execution directories, changed content and incomplete
cleanup never authorize rebinding; runtime ownership checks remain strict.

The manager and jobs use the same macOS account. Recommend a dedicated CI account;
Runmoor neither provisions accounts nor requires a privileged helper. The job
allowlist retains PATH and DEVELOPER_DIR when set, the minimal locale, and
execution-owned HOME/TMPDIR/TMP/TEMP. Other manager variables and management
credentials are excluded. Disposable directories provide no security boundary:
trusted jobs can access account files and Keychains. Operators manage Xcode, SDKs,
language tools, shared caches, signing, Simulators, GUI sessions and global tool
settings. Ordinary builds/tests and unsigned Xcode builds are intended workloads.
GitHub repository/group and fork policies remain authoritative.

The existing lifecycle owns registration, assignment/retirement races, reload,
pause/resume/drain, pool-scoped force-stop, launchd, six-hour deadlines,
three-failure suspension, provider rate limits and sleep inhibition. No automatic
job rerun, telemetry, remote control, feature flag or new release channel is added.
Installed version and explicit backend selection determine availability. Manual
stable releases retain all existing signing/package gates. Rollback requires
drain/stop, a compatible binary and paired state/data backup; never use a v3
binary against v4 state.

Acceptance includes automated contract/mocked lifecycle tests, native Darwin
child-process regression fixtures and supported-target builds. The opt-in
`RUNMOOR_HOST_VERSION_TEST=1 go test -run '^TestOfficialHostRunnerVersion$' -v ./cmds/runmoor/internal/runmoor`
validates official ARM64 archive preparation, `--version` and digest sealing in
temporary state, without GitHub registration or job execution. These checks do
not establish actual macOS host job execution, unsigned Xcode builds or live
GitHub jobs. The real authentication/registration matrix remains unvalidated.
Existing Docker/Tart, service and distribution evidence retains its original limits.
Record commands, source revision, results and these gaps in the PR. Public guides
must disclose host's unreleased status until a manual release provides it.
