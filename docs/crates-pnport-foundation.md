# pnport Rust foundation

## Scope
`crates/pnport` owns the private CLI, data-only PnP graph, native virtual filesystem, process supervision, and local cache. [The complete issue requirements](crates-pnport-requirements.md) are mandatory release gates.

`crates/pnport-preload` owns the private cdylib and retained upstream interpose source/license.

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

### Current implementation evidence and remaining gates

This is an unreleased development foundation, not completion of #958. The checked-in native C fixture passes on macOS arm64. Eleven Rust tests cover data-only inline/split loading, malformed-reference rejection, aliases/scopes, peer identity with shared backing, native reads/stat/enumeration/openat/mmap/realpath/readlink, write rejection for covered paths, source writes, literal arguments and protocol stdout, posix_spawn environment replacement, graph invalidation, cache leases/concurrent publication, corruption rejection with active-lease preservation and explicit cleanup, and unsafe archive paths. Four Node tests cover generated manifest metadata, exact package/companion lookup and argument/signal forwarding; they do not prove real Yarn consumer installation.

Build the private dylib explicitly with `cargo build -p pnport -p pnport-preload` before running `cargo test -p pnport`. The Unix interpose reference is vendored with its exact upstream revision and MIT notice at `crates/pnport-preload/vendor/fspy`. No upstream runtime dependency or toolchain upgrade is introduced. The loader handles Yarn's multiline single-quoted RAW_RUNTIME_STATE representation, including a UTF-8 prefix, and recognizes the generated split-data declaration.

Remaining required work includes Linux static-child syscall interception, Windows Detours and private ACL ownership, complete macOS fork/exec/posix_spawnp propagation, terminal job control, detached-tree/abrupt-supervisor recovery, complete mutation and handle interception, canonicalization through filesystem symlinks, complete native library/package-bin execution and watch conformance, peer-variant native TypeScript fixtures, complete release/installation pipelines, public guides/navigation and reproducible benchmarks. All six actual target execution/installation gates remain open. Clippy also checks the private crates for Linux x64 GNU and Windows x64 MSVC using the pinned toolchain; these are compile-only checks, not native execution evidence. The macOS native test is not sufficient evidence to publish 0.1.0. The current supervisor polls snapshot and active-archive identity, acknowledges initial injection and shuts down its process group, but does not yet satisfy the complete tree-ownership contract. No release workflow or publication authority has been enabled.

Root `cargo test` on this host needs a canonical `TMPDIR` because existing binpm fixtures compare `/var` and `/private/var` paths. A root test run with the unchanged default failed those five existing fixtures; rerunning with only `TMPDIR` canonicalized passed. A later repeated root run hit the unmodified clibox `concurrent_authorized_replacements_produce_one_complete_result` fixture; an isolated retry passed. This is validation context, not a pnport workaround or change to binpm/clibox.

### Interpreter and signed native TypeScript evidence

Package entrypoints retain logical script paths while native execution uses physical backing. Bounded shebang chains support absolute interpreters and `/usr/bin/env` PATH declarations, including `-S` argument splitting. Resolving an env shebang does not execute or substitute the protected system utility. Protected interpreters remain unsupported. Universal Mach-O admission selects the current architecture, bounds all image reads, and verifies embedded signatures offline with Security.framework. Hardened images require both `com.apple.security.cs.allow-dyld-environment-variables` and `com.apple.security.cs.disable-library-validation`; ad hoc/non-hardened native fixtures remain supported. Never alter a compiler's signature or original bytes.

On macOS 26.6.2 arm64, the official `@typescript/native-preview@7.0.0-dev.20260707.2` and `typescript@7.0.2` binaries lack those entitlements. Upstream fspy at `3aac49e31fba6905bb0b3d0e29d7755493241e9c` recorded only two launch-related paths and neither the TypeScript source nor tsconfig; installed Vite+ 0.3.3 replayed a successful cached result after introducing TS2322. Microsoft fixed the signing inputs in [typescript-go #4868](https://github.com/microsoft/typescript-go/pull/4868), and [vite-task #587](https://github.com/voidzero-dev/vite-task/issues/587#issuecomment-5264778400) records the verified official version `typescript@7.1.0-dev.20260812.1`.

That exact official development version (whose npm command is `tsc`, the native successor to `tsgo`) retains Microsoft's signature and has both entitlements. The same upstream fspy recorded 95 paths, including source and config. The upstream task runner produced miss/success, hit/success, then miss/TS2322 with exit 1 after a source edit. pnport also admitted the unmodified native binary and type-checked a real Yarn 4.18.0 ZIP-backed `@types/node` project. This corrects the initial preview pin: new native TypeScript integration fixtures must explicitly use that verified version, preserve the old unsupported-version regression, and never substitute the old `tsgo` command silently. macOS 13 and x64 execution remain unverified by this host observation.

The [repeatable package-owned suite](packages-pnport-distribution-contract.md#official-native-typescript-conformance) now generates both inline and split Yarn graphs from a committed immutable lockfile. Both pass npm compiler entrypoint execution, direct native execution, workspace reference builds, JavaScript/declaration emission, unchanged incremental builds, and intentional type errors. Its negative control proves the same prepared project cannot resolve ZIP-backed Node types without pnport. Preparation is separate from offline execution; the official compiler digest remains unchanged throughout.

## Dependencies and Integrations
pnp = 0.12.12. fspy input revision 3aac49e31fba6905bb0b3d0e29d7755493241e9c. Native artifacts must include their matched injection companions. Distribution and release readiness follow the package contract; Rust crates never publish to crates.io.

## Change Triggers
Update the project index, requirements traceability, npm/public documentation contracts, relevant AGENTS files and native conformance gates together.

## References
- [Project](project-pnport.md)
- [Requirements](crates-pnport-requirements.md)
- [Repository defaults](repository-defaults.md)
