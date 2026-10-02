# pnport npm and native distribution

## Scope
`packages/pnport` owns the private npm source, launcher and four native packages for 0.1.0; the two Windows packages are deferred to 0.2.0. Native release archives include the matching interception artifacts. All distribution requirements in [#958](crates-pnport-requirements.md) remain release gates.

## Runtime and Language
Node.js 22+ for the built-in-only CommonJS launcher; Node.js 24 for repository build/release tooling. The standalone Rust CLI does not require Node.js. No bundler is needed for a native wrapper.

## Users and Operators
npm/Yarn 4 users and maintainers of fixed-version native, Homebrew and npm installs.

## Interfaces and Contracts
The public launcher is @delino/pnport with command pnport. Exact-version optional native packages use suffixes darwin-x64, darwin-arm64, linux-x64-gnu, linux-arm64-gnu for 0.1.0. The reserved future Windows suffixes are win32-x64-msvc and win32-arm64-msvc for 0.2.0; they are not optional dependencies or publication inputs in 0.1.x. Declare os/cpu and GNU libc where applicable; native packages use preferUnplugged so Yarn PnP can execute before virtualization starts. Require version agreement with the native CLI. Preserve argv, cwd, environment, stdio, signals and status. Missing or incompatible packages fail with reinstall guidance, never runtime downloads, compilation, PATH fallback or install scripts.

GitHub archives, checksums, Sigstore material, POSIX installers and prebuilt-only Homebrew cover the same four targets for 0.1.0. The retained PowerShell implementation rejects requests before any network, file or process side effects until Windows 0.2.0 acceptance; its eventual latest-version discovery remains part of that deferred contract. Supported installers resolve `latest` across every GitHub Releases page and select the highest stable pnport version, even when other projects occupy the first page. Changes to either installer select the four-host native CI execution matrix on main. Installation smoke must execute the installer-created public `pnport` or `pnport.cmd` launcher before and after a rejected tampered install, while checking the adjacent installed library. Explicit installation controls updates/rollback. No crates.io, APT/RPM, Apple notarization or Windows Authenticode.

Manual Release Project adds pnport with immutable pnport@v<MAJOR.MINOR.PATCH> identity and synchronized Cargo/npm versions. Preserve coordinator prepare/registry/tag/summary behavior while skipping Cargo registry setup and credentials. Downstream publication verifies complete source-bound native/package sets and actual execution/install evidence before obtaining publication authority. Publish and verify all native npm dependencies before the launcher using provenance. Retries reuse identical immutable bytes; conflicts fail. No partial preview publication. Documentation uses the existing consolidated publisher.

## Storage
Generated packages use an explicit temporary directory or ignored dist. Different native cache formats coexist; an older fixed version never migrates newer entries. Remove repository-owned generated dist after validation.

## Security
Regular CI and dry runs are credential-free and never publish. Only guarded complete-set publication can obtain OIDC/write authority. Runtime is offline except for the child's own behavior. Do not modify user project manifests or install dependencies.

## Logging
Stable bounded launcher error codes on stderr; no raw argv, environments, child output or file content. Structured packaging events identify target, version, revision and integrity without credentials.

## Build and Test
After packaging and direct installation on each native CI/candidate host, rerun `crates/pnport/tests/process_lifecycle.rs` through Cargo with `PNPORT_TEST_BINARY` set to the absolute packaged executable and the selected Rust target. Keep the default parallel runner and matched adjacent injection library. This gate exercises the shipped process owners, signals, crash recovery, cache release, fork/spawn propagation and supported terminal behavior; debug/source-only process checks cannot replace it. Record native evidence and begin benchmarking only after this suite passes. It does not establish the remaining detached macOS, broader job-control or minimum-OS requirements.

Use Node built-in tests, four-target native execution and temporary npm/Yarn 4 PnP consumers installed with scripts disabled. Validate inventories, exact versions, executable modes, missing optional packages, source identity, signature verification and partial-publication recovery. Release cannot pass by cross-compilation alone. The canonical package registry also supplies both workflow matrices through `scripts/native-matrix.mjs`; assembly requires exactly four native archives and five npm tarballs. The private source manifest has `pnportReleaseReady: false` until full native, minimum-OS, and benchmark acceptance is reviewed. Release preparation and every real publisher require boolean true and a nonzero source version before publication; dry runs remain available, and the version coordinator preserves this gate.

### Current implementation status

The private source workspace, built-in-only launcher, platform registry and manifest helpers are implemented. Package-owned scripts now build a native archive with the executable, companion and license notices and optional npm package from the same target build, inspect exact inventories and executable modes, assemble four source-bound evidence records, and generate the launcher package. The supported POSIX installer and a prebuilt-only Homebrew formula install the verified CLI and adjacent interception library with their license notices together. Release Project exposes pnport and creates its version commit and tag through the common coordinator path. The separate exact-tag workflow runs four-target native execution, installed npm/Yarn PnP consumers with lifecycle scripts disabled, TypeScript conformance, and complete-set validation before npm, signed GitHub Release, or Homebrew publication; its manual default is a credential-free dry run. A failed gate leaves the tag intact and blocks publication. npm native packages must be confirmed before the launcher, existing immutable artifact bytes must match on retry, and an older tag retry must never downgrade an already newer Homebrew formula. The macOS package must include the pnport Apache-2.0 text and the separate VoidZero fspy MIT text in npm, native archive and installed result. Linux packages also retain the VoidZero notice for their adapted preload source. The macOS companion has the pinned pnport ABI marker. Linux/Windows keep their previous companion build path. The distribution implementation is not release evidence: the current native runtime gaps still leave the four-target release acceptance incomplete, so no `0.1.0` publication is authorized.

Both native CI and exact-tag release jobs test all three private pnport crates and build the pnport-mode fspy preload in debug beside macOS test binaries and in release beside the packaged CLI. Linux similarly builds its pnport preload in both profiles before execution tests. They pin `MACOSX_DEPLOYMENT_TARGET=13.0`; this artifact deployment floor does not substitute for actual macOS 13 execution acceptance. Windows retains its preload build path for 0.2.0. Generated npm/Yarn smoke consumers permit initial lockfile creation even under CI's default immutable-install policy; this exception applies only to those temporary consumers, while the committed TypeScript fixture retains immutable installation.

Candidate hosts additionally repeat the internally concurrent fork/child-callback control ten times after the complete native suite to expose host-kernel event-ordering failures. A failure stops the job immediately; there is no passing retry, cleanup-deadline relaxation or replacement of the default-parallel installed suite. Fatal Linux mediation logs identify a static source line and ambient errno without recording child paths, memory or environment; the stable diagnostic code and exit class are unchanged.

### Official native TypeScript conformance

The committed `test/fixtures/typescript` fixture and Yarn-generated lockfile pin Yarn 4.18.0, `typescript@7.1.0-dev.20260812.1`, and `@types/node@22.15.30`. This official compiler includes Microsoft's macOS injection-entitlement fix. Its command is `tsc`; the older `@typescript/native-preview` package's `tsgo` command is not automatically replaced. Compiler binaries and signatures are never modified.

Run from the repository root on each supported native host. The same fixture and exact official compiler version run on all four 0.1.0 OS/architecture targets; macOS additionally verifies the unchanged compiler signature and injection entitlements, while Linux verifies the static ELF architecture:

```sh
cargo build -p pnport -p fspy_preload_unix --features fspy_preload_unix/pnport
work=$(mktemp -d)
pnpm --filter @delino/pnport test:typescript:prepare "$work/fixture"
pnpm --filter @delino/pnport test:typescript "$work/fixture"
```

Preparation alone may access npm and install dependencies. It requires a new destination, disables lifecycle scripts, uses an immutable lockfile, and generates inline/split PnP projects sharing an external Yarn cache. Execution invokes only prepared files and pnport; it never calls npm/Yarn, installs, downloads, or repairs dependencies. Both package-owned tasks disable Turbo caching. The optional final argument supplies an already-built pnport executable.

The suite verifies the official compiler payload on each host, its unchanged signature and required entitlements on macOS, and its static ELF architecture on Linux. It also checks ZIP-backed Node types, an unplugged native package, workspace references, scoped and alias dependencies, declaration/JavaScript outputs, unchanged incremental outputs, `--noEmit`, direct native invocation, and TS2322 with exit 1. After a successful reference build, a direct compiler invocation without pnport must fail specifically with TS2688 for missing Node types. Neither format creates a project-root node_modules directory. `typescript-evidence.json` records the actual host OS/architecture, compiler and pnport SHA-256 digests, and cold/warm build durations without imposing a performance threshold. Timings are fixture observations, not the complete filesystem/memory/disk benchmark gate.

This passed on macOS 26.6.2 arm64 and in an offline Ubuntu 22.04 arm64 Docker container. These ARM64 observations do not establish macOS 13, complete native x64 acceptance, peer-variant TypeScript, all process propagation, or installed pnport npm/archive conformance. Those release gates remain open.

### Offline benchmark protocol

The private package's `benchmark` command accepts an already prepared TypeScript conformance directory, a new output directory, the native pnport executable (not an installation wrapper), its declared source commit, and optional sample/iteration counts. Preparation and conformance must run first so reference declarations exist. Execution performs no dependency installation or downloads. The harness requires a clean committed checkout, a native macOS/glibc Linux host, and at least five samples per condition.

For both inline and split manifests, each filesystem and TypeScript repetition uses a fresh pnport cache for its cold run and the same completed cache for its warm run. The compiler's build-info output is removed before both runs, so the workload remains a type check rather than an incremental no-op. The synthetic C fixture counts calls and bytes for dependency reads, metadata, directory enumeration, canonicalization and mmap. These fixture counts are not kernel syscall or device-I/O counts. Descendant `ps` RSS is sampled every 100 ms; report its sampled peak, which can miss short peaks and count shared pages more than once. Cache disk measurements sum unique inode sizes and allocated 512-byte blocks without following symlinks. No numeric performance threshold is imposed.

The external `benchmark.json` includes the harness commit, separately declared native build commit and executable digest, compiler/lock/manifest/source/archive identities, host/tool details, raw numeric samples and median/minimum/maximum summaries. A declared revision is provenance supplied by the caller, not a verified release signature. Never store raw process inventories, child streams, environment values or unrelated user content in results. Record reviewed results in PRs/issues/CI artifacts; do not commit benchmark result documents. Development measurements cannot satisfy the final release-build, minimum-OS or complete four-target acceptance by themselves.

Native main CI and exact-source candidate jobs run this protocol after consumer, TypeScript and installation conformance, using the packaged native executable and the job's checked-out source revision. Each host must finish every sample before its job succeeds. Upload only `benchmark.json` as a separate target/revision/run-attempt artifact; benchmark outputs never enter native archive/npm assembly. Publication still requires reviewed full conformance and minimum-OS acceptance plus the private readiness gate.

```sh
node packages/pnport/scripts/benchmark.mjs /temporary/prepared-typescript /temporary/new-results /path/to/native/pnport <native-source-commit>
```

## Dependencies and Integrations

[Native foundation](crates-pnport-foundation.md), [repository workflow](repository-workflow-contract.md), and consolidated public guides. Source workspace remains private and does not depend on unpublished platform packages.

## Change Triggers
Synchronize CLI/package versions, launchers, installers, CI/release selectors and all pnport contracts when interfaces change.

## References
- [Project](project-pnport.md)
- [Requirements](crates-pnport-requirements.md)
- [Repository defaults](repository-defaults.md)
