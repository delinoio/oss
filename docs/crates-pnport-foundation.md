# pnport Rust foundation

## Scope
`crates/pnport` owns the private CLI, data-only PnP graph, native virtual filesystem, process supervision, and local cache. [The complete issue requirements](crates-pnport-requirements.md) are mandatory release gates.

## Runtime and Language
Rust on the root nightly-2026-01-01 toolchain; explicit Cargo workspace membership, publish = false. Native interception must not upgrade the toolchain or alter protected dependencies. Retain upstream fspy attribution for adapted code.

## Users and Operators
Local developers and CI users running finite commands, servers, watchers and language servers; no hosted operators.

## Interfaces and Contracts
Commands are `run -- <command> [args...]`, `doctor [--json]`, and `cache path|list|prune|clean`. Global flags are `--project`, `--cache-dir`, `--log-level`, and `--color`; NO_COLOR is supported. Selecting a project preserves cwd. Automatic selection walks upward to the nearest .pnp.cjs. Explicit executable paths stay explicit; bare commands prefer the active workspace's direct dependency bins and reject ambiguous bins before inherited PATH lookup. Never build an implicit shell command.

Read inline and split Yarn 4 data without evaluating loader JavaScript. Validate before calling pnp hydration, including its required top-level locator. Preserve aliases, fallback policy, workspaces and peer-specific virtual identity. One owned process tree uses one graph snapshot; no independent-project merging.

Dependency content and virtual directories are read-only. Project source/output writes are native. Read, stat, enumeration, links, realpath, directory-relative handles, watches, mmap, native loading and execution are conformance requirements. Detect physical node_modules conflicts before access, including conflicts created during execution. Do not delete conflicting user content. Ordinary misses return normal filesystem errors.

Inherit cwd, environment and stdio. Never capture child output; stdout remains suitable for language-server protocols. Preserve literal arguments and child status. Owned failures use 2 (arguments), 127 (not found), 126 (not executable), or 125 (initialization/runtime/restart). Structured stable codes disambiguate owned failures from child status. Signals use conventional signal-derived status.

Graph-data or active archive changes/deletion require restart and tree termination. Source edits use native watch behavior. Cancellation and runtime failures request graceful termination, wait five seconds, then terminate/reap remaining descendants. Detached descendants cannot survive the supervisor. No normal timeout or automatic retry. Injection is tree-scoped, with explicit capability errors; no protected-executable replacement or privilege escalation.

Doctor JSON schema v1 is a single ANSI-free object on stdout: `schemaVersion: 1`, `ready: boolean`, and `checks: [{id, status, code, message}]`. Status is `pass`, `fail`, or `unsupported`; IDs identify project, platform, injection and cache checks. Codes are stable PNPORT_* classifications. JSON diagnostics never contain raw parser excerpts, environment values, file contents, full argv or child output. Schema additions are compatible within 0.1.x; removals/meaning changes need a minor version and migration guidance.

## Storage
Use a private OS user cache, optionally overridden with --cache-dir. Content and format identity prevent stale reuse. Atomic publication, confined extraction, archive integrity/path/link validation, cross-process materialization locking and active leases are mandatory. Retain completed entries until explicit cleanup; no eviction, expiry or quota. Cache commands work without a project. Clean removes inactive owned entries; prune removes abandoned incomplete and obsolete-format entries. Preserve unknown ownership and active leases and report retained entries/deletion failures. Never destructively migrate another format. Disk, permissions and publication failures are explicit.

## Security
Current-user permissions only. Injection artifacts, local IPC and cache ownership stay private. No remote control, runtime network or telemetry. Validate archive arithmetic and path containment, including symlinks, before publication. This is compatibility tooling, not a security sandbox. Incompatible interception must not appear successful.

## Logging
Use tracing on stderr; errors/necessary notices by default. Explicit debug covers initialization, graph identity, interception, translation failures, cache, invalidation and cleanup. Paths may appear at debug level. Never log file bytes, environment values, argv vectors or child streams. Stable error codes cover malformed data, resolution, conflicts, unsupported operations, injection, archive corruption, cache, graph changes and cleanup.

## Build and Test
Run focused pnport tests, root cargo test, cargo fmt and Clippy. Native execution gates run on all six targets. Conformance uses PnP-unaware C/Rust plus Linux static Go fixtures and pinned Yarn 4 inline/split generation. Cover the entire requirements test matrix, including cache concurrency/recovery, active cleanup, signals/detachment, environment replacement, stdio/privacy canaries and source watches. Publish reproducible cold/warm execution, filesystem, memory and disk benchmarks without a numeric SLA. Remove generated repository-owned dist directories after verification.

## Dependencies and Integrations
pnp = 0.12.12. fspy input revision 3aac49e31fba6905bb0b3d0e29d7755493241e9c. Native artifacts must include their matched injection companions. Distribution and release readiness follow the package contract; Rust crates never publish to crates.io.

## Change Triggers
Update the project index, requirements traceability, npm/public documentation contracts, relevant AGENTS files and native conformance gates together.

## References
- [Project](project-pnport.md)
- [Requirements](crates-pnport-requirements.md)
- [Repository defaults](repository-defaults.md)
