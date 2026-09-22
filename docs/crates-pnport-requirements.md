# pnport 0.1 requirements

Normative source: [issue #958](https://github.com/delinoio/oss/issues/958). The complete accepted scope is retained below. Requirements are release gates, not claims of completed implementation.

## Summary

Implement **pnport**, a Rust CLI that lets subprocesses use an installed Yarn 4 Plug’n’Play project through a virtual `node_modules` filesystem and transparent access to ZIP-backed packages.

Version **0.1.0** targets local developers and CI users on macOS, Linux, and Windows. It supports finite commands, development servers, watch processes, and language servers. Users invoke `pnport run -- <command> [args...]` without generating a physical project `node_modules` directory.

Release acceptance is based on filesystem and process conformance fixtures executed on all six supported OS/architecture targets. Version-specific certification of individual development tools is deferred. Universal compatibility with arbitrary executables is not claimed.

## Evidence

- The user requested a project named `pnport`, using Rust PnP resolution and the subprocess interception mechanisms in vite-task’s `fspy`.
- Yarn’s [`pnp-rs`](https://github.com/yarnpkg/pnp-rs/tree/7296f196369a2799fe12124f1c2080e335908b0c) provides PnP resolution, virtual-path handling, and ZIP-reading utilities. Published crate `pnp` version `0.12.12` is available and not yanked.
- Its current manifest loader extracts inline data from `.pnp.cjs`; pnport must add data-only loading for Yarn’s split `.pnp.data.json` representation.
- The inspected [`fspy` revision](https://github.com/voidzero-dev/vite-task/tree/3aac49e31fba6905bb0b3d0e29d7755493241e9c/crates/fspy) observes filesystem access using Unix preload libraries, Linux syscall interception, and Windows Detours. Its existing hooks do not provide the required virtual filesystem.
- Upstream macOS handling [replaces certain system executables](https://github.com/voidzero-dev/vite-task/blob/3aac49e31fba6905bb0b3d0e29d7755493241e9c/crates/fspy_shared_unix/src/spawn/macos.rs). pnport must disable that behavior.
- Yarn documents virtual `node_modules` translation and its limitations in [PnPify](https://yarnpkg.com/advanced/pnpify).
- Repository searches found no existing pnport implementation, contract, or issue. [Runlens #907](https://github.com/delinoio/oss/issues/907) also uses fspy but concerns command diagnostics; it is not a pnport dependency.
- Repository contracts determine documentation ownership, explicit Cargo membership, structured logging, CI isolation, and issue formatting.

These observations establish implementation inputs and gaps, not completed compatibility or performance results.

## Current Gap

Tools that independently traverse `node_modules` or expect ordinary OS files cannot necessarily consume PnP package locations and ZIP contents.

A resolver library alone does not change what an unmodified subprocess sees. pnport needs a filesystem translation layer that preserves dependency context, implements native filesystem operations, and propagates to supported descendants.

The product must distinguish supported virtualized execution from unsupported interception, rather than silently executing with incomplete PnP support.

## Proposed Scope

### Platforms and implementation ownership

Support x64 and arm64 on:

| Platform | Minimum supported environment |
|---|---|
| macOS | macOS 13+ |
| Windows | Windows 10 22H2+, native MSVC targets |
| Linux | Ubuntu 22.04-equivalent glibc and kernel capabilities |

Linux support includes dynamically linked and fully static child executables. Musl hosts and mixed-architecture execution are excluded.

Use Rust and integrate pnport into the existing root Cargo workspace. Preserve the root `nightly-2026-01-01` toolchain and protected dependency contracts; adapt the required upstream code rather than upgrading the repository toolchain.

Use `pnp` version `0.12.12` and vendor the necessary fspy components from commit `3aac49e31fba6905bb0b3d0e29d7755493241e9c`. Record provenance, retained licenses, local modifications, and upstream maintenance instructions. Porting and virtualization belong to pnport; an upstream contribution is not a release dependency.

The executable belongs under `crates/pnport`. Required companion crates must have explicit root workspace membership. All pnport-owned Cargo packages remain `publish = false`; no public Rust API is introduced.

### CLI and project selection

Provide:

| Interface | Behavior |
|---|---|
| `pnport run -- <command> [args...]` | Execute a command inside the selected PnP filesystem view. |
| `pnport doctor [--json]` | Diagnose project data, platform capabilities, injection prerequisites, and cache access. |
| `pnport cache path` | Print the effective cache location. |
| `pnport cache list` | List retained cache entries and their state. |
| `pnport cache prune` | Remove abandoned incomplete entries and obsolete cache-format entries. |
| `pnport cache clean` | Remove all entries not in use. |
| `--help`, `--version` | Describe the installed CLI and version. |

Configuration uses flags rather than a configuration file:

- `--project` explicitly selects a PnP project.
- `--cache-dir` overrides the user’s standard OS cache location.
- `--log-level` enables explicit diagnostic verbosity.
- `--color` controls human-output color; support `--color=never` and `NO_COLOR`.

Without `--project`, search upward from cwd for the nearest `.pnp.cjs`. Missing or invalid PnP data is an error. One execution uses one selected project; do not dynamically combine independent PnP projects.

Preserve cwd when selecting a project. Resolve bare commands from the applicable workspace’s direct dependency bins before searching inherited `PATH`. Explicit executable paths remain explicit; ambiguous dependency-bin names produce an actionable error. Preserve literal arguments and do not construct an implicit shell command.

`doctor --json` uses a documented version 1 schema with typed check statuses and stable diagnostic codes. JSON must contain no ANSI sequences or mixed progress output. Cache-management commands remain usable without an active PnP project.

Preserve CLI and diagnostic compatibility throughout `0.1.x`. Later breaking changes require a minor version and migration guidance.

### PnP graph and filesystem behavior

Support Yarn 4 manifests with both inline and split data, without evaluating project loader JavaScript while loading the graph. pnport does not install dependencies, modify lockfiles, run package lifecycle scripts, or repair a missing installation.

Support workspaces, scoped packages, aliases, peer-dependent virtual packages, unplugged packages, and package caches outside the project directory. Respect the manifest’s declared resolution and fallback behavior.

Provide a virtual `node_modules` view and access to ZIP-backed package locations. Preserve peer-specific logical package identity through path normalization and `realpath`; sharing physical package bytes must not merge distinct dependency contexts.

Implement and test:

- File reads, metadata, existence checks, directory enumeration, symlinks, `readlink`, and `realpath`.
- Relative paths, directory-relative operations, and filesystem handles.
- File and directory watching.
- Memory-mapped reads.
- Execution of supported package binaries and loading of native libraries from ZIP-backed packages.

Dependency views and managed package contents are read-only. Writes through those views fail with appropriate filesystem errors. Ordinary project-source and output paths retain native write behavior.

If a physical `node_modules` conflicts with a virtual directory, report the conflict and stop without modifying or deleting it. Detect conflicts encountered after startup as well.

The filesystem follows the PnP dependency graph, but does not promise the Yarn JavaScript loader’s complete import-boundary enforcement for arbitrary tools. Each tool remains responsible for its language-specific resolution semantics.

### Process lifecycle and changing inputs

Inherit cwd, environment, and standard streams. Keep pnport diagnostics on stderr and preserve child stdout for protocols such as language-server communication. Do not capture or persist child streams.

Apply virtualization to supported descendants, including supported spawn, exec, and environment-replacement paths. Scope injection to the owned process tree; do not attach to unrelated processes.

Retain ordinary interactive terminal behavior without introducing a separate terminal recorder or PTY product.

Unsupported injection, protected executables, unavailable Linux syscall capabilities, or incompatible interception must produce explicit diagnostics. Do not replace executables, elevate privileges, or silently fall back to unvirtualized execution. This is not a security sandbox or a guarantee against deliberate interception bypass.

Each process tree uses one PnP graph snapshot. On detected changes or deletion of PnP data or an actively used ZIP archive, terminate the owned tree and instruct the user to restart. Workspace-source changes continue through normal watch notifications.

On cancellation, handled supervisor termination, infrastructure failure, or required restart:

1. Request normal termination of owned processes.
2. Allow five seconds for shutdown.
3. Terminate and reap remaining owned processes.

Do not permit intentionally detached descendants to outlive the supervised execution. Forced OS termination cannot guarantee graceful cleanup; ownership and recovery must not depend solely on graceful exit.

Normal execution has no timeout and no automatic retry. Propagate child exit status; use conventional signal-derived status where applicable. pnport-originated failures use:

| Failure | Exit code |
|---|---:|
| Invalid arguments | 2 |
| Command not found | 127 |
| Command cannot be executed | 126 |
| pnport initialization/runtime failure or required restart | 125 |

Stable stderr diagnostic codes distinguish pnport failures from a child returning the same numeric status. Ordinary filesystem misses remain normal child-visible errors rather than automatically terminating the supervisor.

### Cache, concurrency, and recovery

Lazily materialize required packages into a private per-user cache. Do not create a project `node_modules` tree. Native loading and executable access use managed physical backing while retaining the virtual-path contract.

Use content identity and cache-format identity to prevent stale or incompatible reuse. Publish completed entries atomically, serialize competing materialization, and retain ownership leases for active executions.

Cache retention has:

- No automatic eviction.
- No automatic expiry.
- No product-imposed total-size quota.
- Explicit errors for disk exhaustion, permission failure, or failed publication.

`prune` removes abandoned incomplete entries and obsolete formats. `clean` removes all inactive entries. Both preserve active entries and report retained entries and deletion failures. Never remove unrelated files or blindly delete entries whose ownership or active use cannot be established.

Different installed versions must not destructively migrate shared cache entries. An incompatible version can create its own format namespace, making fixed-version rollback possible without modifying the project.

Validate archive paths, links, integrity, and arithmetic before or during confined extraction. Prevent traversal outside the owned cache. Protect package publication and cleanup from concurrent processes and partially written state.

### Security and observability

pnport operates with the invoking user’s permissions. It has no hosted service, account system, remote API, runtime networking, telemetry, or feature flags. Explicit invocation and the installed version determine availability. The child’s normal network behavior is unchanged.

Keep IPC, injected artifacts, and cache ownership private to the local execution/user boundary. Do not expose a remotely accessible control service.

Use structured Rust logging through `tracing`:

- Default output contains errors and necessary notices only.
- Explicit debug logging covers initialization, graph identity, injection, filesystem translation failures, cache operations, invalidation, and cleanup.
- Debug output may include paths.
- Never collect file contents, environment values, full argv, or captured child output in pnport diagnostics.

Use stable error classifications for malformed manifests, resolution failures, filesystem conflicts, unsupported operations, injection failure, archive corruption, cache failure, graph changes, and cleanup failure.

There are no hosted dashboards, alerting requirements, or external security-certification gates. Publish reproducible cold/warm execution, filesystem, memory, and disk benchmarks without a numerical performance SLA.

### Distribution, rollout, documentation, and support

Ship version `0.1.0` only after all six targets pass actual execution and installation validation. There is no partial preview release, fixed deadline, or support-response SLA.

Provide:

- GitHub prebuilt archives for all six targets, checksums, and Sigstore verification material.
- POSIX and PowerShell installers.
- Prebuilt Homebrew distribution for supported macOS and Linux targets.
- `@delino/pnport`, supporting Node.js 22+, with six exact-version native optional dependencies.

Use npm platform suffixes `darwin-x64`, `darwin-arm64`, `win32-x64-msvc`, `win32-arm64-msvc`, `linux-x64-gnu`, and `linux-arm64-gnu`.

The npm launcher forwards execution to the matching installed package without install scripts, runtime downloads, compilation, or fallback to an unrelated PATH binary. Platform packages use Yarn’s [`preferUnplugged`](https://yarnpkg.com/configuration/manifest#preferUnplugged) metadata so the launcher can start under Yarn 4 PnP before pnport virtualization exists.

Native distributions include the required injection artifacts from the same verified build. Standalone installations do not require Node.js merely to start pnport.

Extend the manual `Release Project` workflow with `pnport` and the immutable release identity `pnport@v<MAJOR.MINOR.PATCH>`. Synchronize native and npm versions. Skip crates.io publication and Cargo registry credentials.

The coordinator retains the repository’s existing behavior; downstream publication must validate the complete native/package set before obtaining publication authority. Publish and verify native npm packages before the launcher. Use npm provenance, source-bound release verification, immutable assets, and recoverable retries that never overwrite conflicting published bytes.

CI and release dry runs remain credential-free and non-publishing. Public documentation continues through the existing consolidated publisher.

Updates and rollback use explicit version installation. Do not add automatic update checks, self-update, Apple notarization, or Windows Authenticode.

Before runtime implementation, create the project and Rust, npm-distribution, and public-documentation contracts. Register pnport ownership, update the documentation catalog and relevant AGENTS files, and document Rust/local-storage choices against repository defaults.

Keep npm source ownership under `packages/pnport` and public guides under `apps/public-docs/docs/pnport`, published at `https://oss.delino.io/pnport`. Use the existing Rspress site, shared accessible navigation, Cloudflare Pages deployment, and fixed documentation development server.

Provide English CLI help, errors, README, and public documentation covering installation, commands, supported filesystem behavior, limitations, editor configuration, cache management, diagnostics, benchmarks, and rollback. Keep internal architecture and release operations in `docs/`. Support uses GitHub issues.

## Acceptance Criteria

- A PnP-unaware fixture can traverse virtual dependencies and read ZIP-backed files without a physical project `node_modules`.
- Yarn 4 inline and split manifests, workspace relationships, aliases, fallback settings, and peer-specific identities behave as documented.
- All six supported targets pass native filesystem, process, watch, binary-loading, and installation conformance.
- Linux static-child fixtures work through the syscall interception path.
- Dependency writes fail while ordinary source/output writes retain native behavior.
- Existing physical `node_modules` conflicts are reported without modifying user files.
- Child arguments, streams, terminal behavior, environment, and exit status remain compatible.
- Required injection failures cannot become apparently successful unvirtualized runs.
- PnP-data or active-ZIP changes terminate the tree with restart guidance; workspace edits produce normal watch behavior.
- Cache publication is atomic and concurrent-safe, active entries survive cleanup, and completed entries remain until explicit cleanup.
- Logs and diagnostics meet the agreed privacy and output-separation contracts.
- Native, installer, Homebrew, and npm distributions are verified; the npm launcher works under Yarn 4 PnP with installation scripts disabled.
- Documentation, support guidance, manual rollback, release recovery, and compatibility rules are complete.
- Existing repository toolchains, protected dependencies, CI boundaries, development commands, and unrelated releases remain intact.

## Test Scenarios

1. Generate pinned Yarn 4 fixtures for inline/split manifests, workspaces, scoped packages, aliases, peer variants, missing dependencies, configured fallback, unplugged packages, and external package caches.
2. Use PnP-unaware C/Rust fixtures and static Go/Linux fixtures to verify that virtualization changes previously unavailable filesystem access.
3. Cover reads, metadata, enumeration, links, canonicalization, directory-relative operations, handles, mmap, executable loading, and native-library loading.
4. Verify peer identity through repeated resolution, canonical paths, shared backing content, and fixture module loading.
5. Test Unicode and space-containing paths, malformed manifests, missing archives, corrupt archives, unsafe archive entries, and physical `node_modules` collisions.
6. Verify dependency write rejection and ordinary workspace/output mutations.
7. Exercise source watching, linked-workspace changes, PnP-data changes, active archive replacement/deletion, and required restart behavior.
8. Exercise nested descendants, shell/interpreter entrypoints, environment replacement, cwd changes, literal/empty arguments, interactive input, and language-server-style stdio.
9. Test normal exit, child failure, unsupported injection, supervisor failure, cancellation, five-second escalation, lingering descendants, and ownership recovery after abrupt termination.
10. Test concurrent extraction, reuse, partial publication, cache corruption, disk exhaustion, permission loss, prune/clean during active runs, and coexistence of cache formats.
11. Use canary data to verify that file contents, environment values, full argv, and child output never enter pnport logs. Verify JSON and ANSI separation.
12. Verify six-target artifact completeness, checksums, Sigstore material, version consistency, npm platform selection, missing optional dependencies, and immutable partial-publication recovery.
13. Install prepared packages in temporary npm and Yarn 4 PnP consumers with scripts disabled. Verify launcher startup without pnport already active and absence of runtime downloads or compilation.
14. Verify fixed-version install/update/rollback, installer failure handling, Homebrew prebuilt selection, and documentation installation guidance.
15. Validate public routes, navigation accessibility, CLI help, internal-content boundaries, release dry-run isolation, and CI selection/aggregation.
16. Publish reproducible cold/warm benchmarks using the conformance workloads. Do not impose an unapproved numerical threshold.

Run focused pnport tests and repository-required root `cargo test`, Rust formatting/Clippy checks, npm launcher/package tests, and `pnpm test` from the public-docs frontend directory. Generate required build artifacts explicitly and remove repository-owned generated `dist` directories before completing implementation.

## Out of Scope

- Yarn Classic and Yarn 2/3 compatibility guarantees.
- Musl hosts, mixed-architecture execution, and universal executable compatibility.
- Version-specific certification of individual development tools in v1.
- Perfect Yarn-loader import-boundary enforcement for arbitrary tools.
- Writable dependency overlays, automatic cache eviction, and automatic cache expiry.
- Live PnP graph replacement without restarting the process tree.
- Package installation management, lifecycle execution, lockfile changes, or automatic installation repair.
- Editor extensions, public Rust/JavaScript APIs, and crates.io publication.
- Docker or remote-process injection, unrelated-process attachment, privilege escalation, kernel drivers, or FUSE installation.
- Persistent detached descendants and automatic command retries.
- Security sandboxing, hostile-process containment, and external security certification.
- Hosted accounts, remote control, runtime telemetry, feature flags, dashboards, and automatic updates.
- APT/RPM distribution, Apple notarization, and Windows Authenticode.
- Partial preview releases, numerical performance guarantees, support-response SLAs, and fixed release deadlines.

