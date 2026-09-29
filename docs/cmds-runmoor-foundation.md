# Runmoor command foundation

## Scope

`cmds/runmoor` owns the CLI, local controller, GitHub adapter, Docker and Tart backends, image lifecycle, service integration, power inhibition, diagnostics, and reconciliation for issue #893. Internal implementation boundaries are files in `internal/runmoor`; adapters expose interfaces for deterministic tests without substituting mocks in production.

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
- New `init` pools write `runmoor-linux`, `linux`, `x64` on Docker amd64; `runmoor-linux`, `linux`, `ARM64` on Docker arm64; and `runmoor-macos`, `macOS`, `ARM64` on Tart arm64. The existing scale-set name remains the first label. These labels are generated only by `init`: loading, validating or reloading existing and manually authored pools never adds routing labels, and omitted labels remain empty.
- TOML v1 has `storage`, `host`, `timeouts`, `logging`, `connections`, and `pools`; optional Docker socket/Tart executable settings select local dependencies. Connections contain a GitHub.com repository or organization URL, PAT/App enum, one `credential.env` or `credential.file` reference, and App client/installation IDs when applicable. Pools specify connection, explicit scale-set name, routing labels, optional organization group, backend/mode, optional native architecture, image or image source, latest or pinned runner version/path, min-idle/max-runners, runner resources, and DinD image/resources when enabled.
- Host concurrency, CPU, memory MiB and minimum free disk MiB may be omitted for automatic defaults; explicit values must be positive limits. Aggregate minimum idle reservations, including daemon resources, must fit. Image setup consumes the same limits and counts toward the two-macOS-VM maximum.
- Default timeouts are Docker preparation 5 minutes, Tart preparation 10 minutes, and a job 6 hours. Transient retries use exponential jittered backoff of 1–60 seconds, extended for provider rate-limit instructions. Three consecutive preparation failures suspend only the affected pool. Authentication/ownership failures require correction; an unchanged configuration requires explicit resume.
- Forced cancellation preserves the existing preparation-failure counter and pool suspension state, including when an adapter returns success after cancellation. It is logged as cancellation instead of an image/bootstrap failure.
- A scheduled preparation must still be unforced and in the preparing phase when its worker starts. This closes the force-stop gap between reservation and worker registration; cancellation is also checked before JIT and backend work. After journaling a returned JIT registration ID, the worker re-reads the durable forced flag and preparing phase immediately before backend preparation. A stop or lifecycle transition during registration prevents provisioning even before context cancellation arrives, preserves the new phase and failure counters, and retains the registration ID for cleanup.

### Scheduling and lifecycle

- The official client handles App/PAT token exchange. A custom session loop persists effects before acknowledgement; duplicate and stale messages do not become incremental demand counters. Demand comes from `TotalAssignedJobs`; lifecycle events identify started/completed runners.
- Round-robin allocation satisfies real demand before idle targets. Reservations include preparation, active jobs, uncertain live resources, setup VMs and DinD. Running work is never preempted to satisfy another pool's demand. Low disk blocks new provisioning without evicting jobs or images. Insufficient host/pool capacity is a stable visible wait reason, logged when it changes.
- Idle retirement uses the same logical pool cap across generations as allocation. Excess queued demand at that cap does not evict minimum-idle runners in other pools; newly admissible demand can still displace idle capacity. Tart demand blocked by the shared two-VM ceiling does not evict warm Docker runners; a warm Tart VM can still yield its VM slot. Retirement planning uses the allocator with only demand targets and accounts for available capacity, candidates already selected, and deregistered executions pending cleanup. Once that capacity satisfies demand it preserves remaining minimum-idle runners, while additional CPU/memory needs can still require multiple retirements. These planning reservations never enter durable state or release actual capacity before confirmed termination.
- Scale-set ownership requires the installation marker and durable ownership journal; names alone are insufficient. Unknown existing sets are rejected. Every new runner gets fresh JIT credentials and can execute at most one job.
- Per-pool initialization and retirement share a serialization boundary through remote calls and durable publication. Reload can drain immediately, but new creation intent rechecks the current phase. Draining generations resolve journaled creation by lookup only: delete verified owned results, clear confirmed absence, and preserve unresolved outcomes before releasing the remote identity. Late initialization failures cannot resurrect retired generations.
- When a validated reload supplies a connection for the same remote scale-set identity, its verified connection replaces any earlier durable cleanup authority for each draining generation, including a rollback to the generation's original credential reference. Each validation refreshes the cached client for old-generation lookup, busy-aware runner removal and scale-set deletion. Existing owner labels, recorded scale-set IDs and pending-creation checks remain mandatory; a failed ownership check keeps the old generation draining and blocks same-identity creation.
- Successful durable retirement drops the pool's cached GitHub client and serialization mutex and cancels its session loop. Stale generation references cannot recreate either cache, and late session creation closes its result instead of publishing into retired or pruned state. Failed durable retirement retains the caches for retry. A final message may commit before the retired pool is pruned; the subsequent eligibility lookup treats a missing pool as ineligible, skips job acquisition and cannot interrupt unrelated pools.
- Idle retirement removes the GitHub registration before terminating local resources. GitHub's busy rejection preserves a concurrently assigned job. Ambiguous preparation also performs this busy-aware check before cleanup.
- An expired preparation after restart remains ambiguous and follows busy-aware cleanup. A busy rejection preserves the job and derives an unobserved start from the persisted creation time, never a new recovery-time deadline. Forced termination is reserved for an explicit force-stop or an established busy-job timeout.
- `pause` stops acquisition/provisioning, preserves busy work, and retires idle runners. `drain` pauses and waits. `stop` drains and exits after local termination/cleanup; unresolved remote cleanup remains durable and visible. `stop --pool NAME` drains just that pool, including its older generations; offline drain/stop waits use the same pool selection if the manager becomes unavailable; `stop --pool NAME --force` restricts forced termination to it. `stop --force` immediately cancels in-flight preparation and terminates only verified owned work. It never permits deletion of unrelated resources.
- Resume revalidates credentials, backend/image and resource conditions. Reload validates the candidate before accepting one new generation. A validated reload resumes a suspended replacement only when the prior failure's relevant input changed: connection or runner group for authentication; target, group or scale-set identity for a recorded remote scale-set ownership failure; image, runner version, platform or startup environment for their matching failures. Local execution ownership conflicts and legacy ownership failures without a recorded source stay suspended until ownership is resolved and the operator explicitly resumes them. Runner-version recovery requires a change to the runner-bearing image, source, path, version or platform; a DinD daemon image alone is unrelated. For a preparation failure, only the affected backend's preparation timeout and its Docker socket or Tart executable count as relevant global settings; the job timeout and the other backend's settings do not. A relevant global setting change alone replaces the suspended generation. Other suspensions carry their exact safe problem and preparation-failure count into the replacement. Failed reloads change nothing; unchanged configuration, missing prior reason, operator pause and stop never auto-resume. Existing jobs retain original generation/configuration/deadlines. Changed or removed pools drain; a replacement using the same remote identity waits for old ownership to retire and still verifies ownership before creation. Storage relocation is not a live reload operation. Reload serializes with image operations and cannot clear an already requested manager stop.
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
- DinD reconciliation inspects the daemon's recorded identity and ownership as well as the runner. Missing/stopped/paused/restarting daemons make the execution unavailable for busy-aware cleanup without claiming termination; ownership changes quarantine it, and transient inspection errors preserve reservations for retry.
- Termination checks deterministic runner/daemon/init names and recorded container IDs independently of label-filtered discovery, before stopping resources and again before confirming termination. Foreign replacements or identity mismatches quarantine the execution and retain its reservation even during force-stop; an empty filtered list alone never proves termination.
- Paused or restarting runner containers are also unavailable, including in plain mode. They enter the same busy-aware cleanup path, retaining their capacity reservation until confirmed termination permits a replacement.
- Never mount the host socket, personal host directories, SSH agents, configuration, or credentials into jobs. JIT is sent through stdin rather than Docker configuration/environment. All execution containers disable Docker log retention; the nested DinD daemon also defaults to the `none` log driver so container actions and service output are not retained in its storage. Clean up job/daemon/init containers, networks and volumes; retain only base images. Build caches belong in GitHub Actions cache.

### Tart images and jobs

- Image mutations require a running manager; the CLI never executes them offline. The manager retains sleep inhibition for open setup/validation revisions after the request returns. Whole-manager stop, including force-stop and service stop/uninstall, waits for open setup and pending image removal; pool-scoped waits do not. Shutdown rejects new image operations but permits sealing an already open revision and retrying pending removal. Close the setup VM normally or seal it before expecting stop to finish. Offline image listing remains available. Initial setup accepts automatic or explicit host budgets with no pools/connections; add the Tart pool and reload after sealing.

- Host compatibility accepts stable Tart 2.x.x releases with complete SemVer major.minor.patch numbers and optional valid build metadata; reject prereleases, shorthand versions, malformed versions, and other major versions. Guest Agent remains pinned to 0.14.2 on macOS 14+ arm64. Require functional Guest Agent RPC and an exact runner version in the prepared non-root guest account. A reachable guest returns a bounded ready/invalid marker; invalid accounts, versions, registration or workspace state fail immediately with `IMAGE_INVALID`, while unavailable RPC retries within the preparation deadline.
- `image create --name NAME [--cpu N --memory-mib N]` accepts exactly one `--ipsw latest|PATH` or `--from SOURCE`. `latest` delegates selection of the newest host-supported Apple restore image to Tart; a path is an absolute local `.ipsw`. Sources are a stopped local Tart name (optional `--source-home`), absolute `.tvm`, sealed Runmoor revision UUID, or `oci://` input resolved to a digest. Imports never modify the operator's source image.
- Local-name imports use a temporary `import-<image UUID>.tvm` archive in private managed data. The image UUID is durable before export begins; normal completion/failure, restart reconciliation across all image phases, and image removal clean that exact artifact. Cleanup errors remain visible in image diagnostics and prevent successful removal from discarding the ownership record. Unjournaled archives and unexpected symlink/directory targets are preserved.
- `image open --id UUID` opens a mutable setup revision for account/Xcode/tool installation. `image seal --id UUID --runner-version VERSION [--runner-path PATH]` validates guest readiness, clean runner registration/workspace and exact versions, stops the VM, hashes the base files and publishes an immutable local revision. Sealing and sealed-image verification hash with bounded, cancellation-aware reads under the operation context, so preparation deadlines and force-stop do not wait for a whole disk read; cancellation closes the file and discards the partial digest. Editing requires a new revision. `image remove` refuses referenced or active images.
- Image open polls the owned VM until Tart confirms it running, bounded by the Tart preparation deadline. A spawned process alone is insufficient. Unconfirmed startup records a safe preparation error and holds its reservation until reconciliation confirms the VM stopped.
- Seal runner paths use the same validation as TOML pools: a leading `/`, no `..` substring, NUL or line breaks. Reject invalid paths before reservation or guest preparation; store accepted strings unchanged so the pool can match sealed metadata exactly.
- Every Tart invocation uses argv/stdin and an allowlisted environment containing private `TART_HOME` and `TART_NO_AUTO_PRUNE=1`. No external automatic pruning is permitted. Each job clones a sealed base, configures limits and boots that clone only. Pool validation and every sealed-base clone (including a new setup revision) recompute the recorded digest and reject missing or altered files before cloning.
- The same arm64 Runmoor binary provides a private guest supervisor, transferred by stdin without management credentials. Its detached runner and the detached native Tart process inherit real null output descriptors, not parent-owned pipes, and survive a manager-only restart. Guest status contains bounded execution metadata, never JIT credentials or job output. Bootstrap checks matching supervisor identity and unfinished readiness after the runner survives a one-second startup observation; missing/non-executable launchers and immediate exits are preparation failures and do not reset the pool circuit breaker. The guest's private state root is fixed alongside the uploaded helper, independent of inherited temporary-directory settings. Reacquisition checks the live supervisor command as well as its persisted identity and startup readiness; rebooted or missing guest state remains uncertain until reconciliation or the original deadline.
- Ownership markers and durable records constrain all VM cleanup. Finished/failed clones and workspaces are destroyed; bases persist until explicit deletion. Tart and Guest Agent are external dependencies with version-specific licenses. The previously pinned Tart 2.37.0 and Guest Agent 0.14.2 sources use FSL-1.1-ALv2; operators must check the license of their installed Tart release. Runmoor neither bundles them nor distributes macOS/Xcode images.

### Service operation

launchd and systemd user services invoke the same foreground manager and drain control path. Definitions contain executable/config references only, never copied credential values. File credential references are recommended for restart persistence. A manager-only failure must not implicitly kill detached live work. Install does not overwrite existing definitions; uninstall preserves configuration, images and unresolved state.

Every Linux `systemctl --user` invocation, including service installation and uninstallation `daemon-reload`, receives the normal minimal command environment plus `XDG_RUNTIME_DIR` and `DBUS_SESSION_BUS_ADDRESS` only when the caller supplied them. Runmoor does not invent session selectors or inherit other caller variables for service commands. Missing or unreachable user-session context remains a safe dependency failure without exposing environment values or subprocess stderr. The shared `minimalEnv` remains unchanged for Tart, guests, and unrelated subprocesses.

A launchd bootout failure is returned as a safe dependency error and leaves the plist intact unless a separate exact-service query returns service-not-found and the GUI domain remains reachable. An arbitrary query failure or a still-loaded definition cannot count as a successful stop or uninstall.

## Storage

- Config: `$XDG_CONFIG_HOME/runmoor/config.toml`, otherwise `~/.config/runmoor/config.toml`.
- State: `$XDG_STATE_HOME/runmoor`, otherwise `~/.local/state/runmoor`.
- Data: `$XDG_DATA_HOME/runmoor`, otherwise `~/.local/share/runmoor`.
- TOML may override absolute state/data directories. The Unix socket path must fit macOS's length limit. Directories are mode 0700; files/socket are 0600. Reject symlinks and foreign ownership at sensitive file boundaries.
- SQLite schema v1 stores an atomic snapshot row under WAL/FULL durability: installation identity, configuration generations/references, pool/session metadata without tokens, runner lifecycle and reservations, image revisions, and cleanup progress. A nonblocking file lock excludes concurrent manager or offline state access.
- Delete completed execution history after seven days. Preserve unresolved cleanup and ownership indefinitely. User-created sealed images remain until explicit deletion; generated revisions use reference-aware managed retention. SQLite v1 migrates atomically to v2; other unsupported versions are rejected.
- Storage relocation is accepted only after all executions complete cleanup and all image setup/removal operations close; it atomically rebinds retained generations while preserving installation ownership. Backup only after drain and stop: preserve the complete state and managed data directories, protect referenced credentials separately, and pair backups with a compatible binary. Runmoor binary updates and rollback are manual; never open a newer state schema with an older binary.

## Security

Trust is limited to developer/team workflows. GitHub runner-group/repository access policy is never replaced. Management credentials remain host-only environment or permission-restricted file references. No credential/JIT values enter SQLite or logs. Unknown upstream errors are converted to bounded safe classifications; raw errors are not wrapped into public messages. Cleanup requires exact owned identity, and unresolved ambiguity remains visible rather than being guessed away.

## Logging

Use `log/slog` text or JSON for lifecycle transitions, preparation duration, retries, capacity/dependency failures, cleanup results and power warnings. Include only safe identifiers and stable codes. Preserve a structured diagnostic record before destroying an execution; never copy raw workflow output or raw runner logs. Enforce both seven-day and 256 MiB retention across local diagnostics. No remote telemetry is emitted.

## Build and Test

- Run formatting, `go vet ./cmds/runmoor/...`, `go test ./cmds/runmoor/...`, and supported-host `go test -race ./cmds/runmoor/...`.
- Ordinary tests use an in-process TLS GitHub API substitute with the real scale-set SDK, deterministic backend/session adapters, temporary private state, and no live credentials or user-service installation. Archive download/repack fixtures and the opt-in official archive download run only on Darwin/Linux because private file creation requires Unix ownership checks. Windows CI still runs portable release metadata and asset-selection validation; unsupported-host storage continues returning `PLATFORM_UNSUPPORTED`.
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
all pools, DinD and image preparation. Disk reserves default to 10240 MiB, or
20480 MiB when Tart is configured. Warm capacity defaults to zero.
GitHub session polling and job acquisition apply the same host and Docker engine
CPU/memory ceilings, including DinD, even when an explicit pool concurrency is
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

SQLite v2 adds managed-pool state, immutable artifact identities, release metadata
and preparation/cleanup reservations. Opening v1 migrates its snapshot and
`user_version` atomically without rewriting installation ownership or execution
history. Old binaries reject v2. Read-only configuration inspection does not
migrate state. Public status/doctor JSON remains schema v1 with additive managed
runner and artifact fields. Generated artifacts retain current, previous and
referenced bases; cleanup never force-removes externally referenced Docker images
or deletes user sources. Official pulled base layers remain Docker-owned.

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
