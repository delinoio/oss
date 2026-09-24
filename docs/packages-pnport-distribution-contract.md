# pnport npm and native distribution

## Scope
`packages/pnport` owns the private npm source, launcher and six native packages. Native release archives include the matching interception artifacts. All distribution requirements in [#958](crates-pnport-requirements.md) remain release gates.

## Runtime and Language
Node.js 22+ for the built-in-only CommonJS launcher; Node.js 24 for repository build/release tooling. The standalone Rust CLI does not require Node.js. No bundler is needed for a native wrapper.

## Users and Operators
npm/Yarn 4 users and maintainers of fixed-version native, Homebrew and npm installs.

## Interfaces and Contracts
The public launcher is @delino/pnport with command pnport. Exact-version optional native packages use suffixes darwin-x64, darwin-arm64, win32-x64-msvc, win32-arm64-msvc, linux-x64-gnu, linux-arm64-gnu. Declare os/cpu and GNU libc where applicable; native packages use preferUnplugged so Yarn PnP can execute before virtualization starts. Require version agreement with the native CLI. Preserve argv, cwd, environment, stdio, signals and status. Missing or incompatible packages fail with reinstall guidance, never runtime downloads, compilation, PATH fallback or install scripts.

GitHub archives, checksums, Sigstore material, POSIX/PowerShell installers and prebuilt-only Homebrew cover the same six targets. Both installers resolve `latest` across every GitHub Releases page and select the highest stable pnport version, even when other projects occupy the first page. Changes to either installer select the six-host native CI execution matrix on main. Installation smoke must execute the installer-created public `pnport` or `pnport.cmd` launcher before and after a rejected tampered install, while checking the adjacent installed library. Explicit installation controls updates/rollback. No crates.io, APT/RPM, Apple notarization or Windows Authenticode.

Manual Release Project adds pnport with immutable pnport@v<MAJOR.MINOR.PATCH> identity and synchronized Cargo/npm versions. Preserve coordinator prepare/registry/tag/summary behavior while skipping Cargo registry setup and credentials. Downstream publication verifies complete source-bound native/package sets and actual execution/install evidence before obtaining publication authority. Publish and verify all native npm dependencies before the launcher using provenance. Retries reuse identical immutable bytes; conflicts fail. No partial preview publication. Documentation uses the existing consolidated publisher.

## Storage
Generated packages use an explicit temporary directory or ignored dist. Different native cache formats coexist; an older fixed version never migrates newer entries. Remove repository-owned generated dist after validation.

## Security
Regular CI and dry runs are credential-free and never publish. Only guarded complete-set publication can obtain OIDC/write authority. Runtime is offline except for the child's own behavior. Do not modify user project manifests or install dependencies.

## Logging
Stable bounded launcher error codes on stderr; no raw argv, environments, child output or file content. Structured packaging events identify target, version, revision and integrity without credentials.

## Build and Test
Use Node built-in tests, six-target native execution and temporary npm/Yarn 4 PnP consumers installed with scripts disabled. Validate inventories, exact versions, executable modes, missing optional packages, source identity, signature verification and partial-publication recovery. Release cannot pass by cross-compilation alone.

### Current implementation status

The private source workspace, built-in-only launcher, platform registry and manifest helpers are implemented. Package-owned scripts now build a native archive with the executable, companion and license notices and optional npm package from the same target build, inspect exact inventories and executable modes, assemble six source-bound evidence records, and generate the launcher package. POSIX and PowerShell installers and a prebuilt-only Homebrew formula install the verified CLI and adjacent interception library with their license notices together. Release Project exposes pnport and creates its version commit and tag through the common coordinator path. The separate exact-tag workflow runs six-target native execution, installed npm/Yarn PnP consumers with lifecycle scripts disabled, TypeScript conformance, and complete-set validation before npm, signed GitHub Release, or Homebrew publication; its manual default is a credential-free dry run. A failed gate leaves the tag intact and blocks publication. npm native packages must be confirmed before the launcher, existing immutable artifact bytes must match on retry, and an older tag retry must never downgrade an already newer Homebrew formula. The macOS package must include the pnport MIT text and the separate VoidZero fspy MIT text in npm, native archive and installed result. Linux packages also retain the VoidZero notice for their adapted preload source. The macOS companion has the pinned pnport ABI marker. Linux/Windows keep their previous companion build path. The distribution implementation is not release evidence: the current native runtime gaps still make the six-target gate fail, so no `0.1.0` publication is authorized.

Both native CI and exact-tag release jobs build the pnport-mode fspy preload in debug beside macOS test binaries and in release beside the packaged CLI. Linux and Windows use their existing pnport preload builds.

### Official native TypeScript conformance

The committed `test/fixtures/typescript` fixture and Yarn-generated lockfile pin Yarn 4.18.0, `typescript@7.1.0-dev.20260812.1`, and `@types/node@22.15.30`. This official compiler includes Microsoft's macOS injection-entitlement fix. Its command is `tsc`; the older `@typescript/native-preview` package's `tsgo` command is not automatically replaced. Compiler binaries and signatures are never modified.

Run from the repository root on each supported native host. The same fixture and exact official compiler version run on all six OS/architecture targets; macOS additionally verifies the unchanged compiler signature and injection entitlements, while Linux verifies the static ELF architecture:

```sh
cargo build -p pnport -p fspy_preload_unix --features fspy_preload_unix/pnport
work=$(mktemp -d)
pnpm --filter @delino/pnport test:typescript:prepare "$work/fixture"
pnpm --filter @delino/pnport test:typescript "$work/fixture"
```

Preparation alone may access npm and install dependencies. It requires a new destination, disables lifecycle scripts, uses an immutable lockfile, and generates inline/split PnP projects sharing an external Yarn cache. Execution invokes only prepared files and pnport; it never calls npm/Yarn, installs, downloads, or repairs dependencies. Both package-owned tasks disable Turbo caching. The optional final argument supplies an already-built pnport executable.

The suite verifies the official compiler payload on each host, its unchanged signature and required entitlements on macOS, and its static ELF architecture on Linux. It also checks ZIP-backed Node types, an unplugged native package, workspace references, scoped and alias dependencies, declaration/JavaScript outputs, unchanged incremental outputs, `--noEmit`, direct native invocation, and TS2322 with exit 1. After a successful reference build, a direct compiler invocation without pnport must fail specifically with TS2688 for missing Node types. Neither format creates a project-root node_modules directory. `typescript-evidence.json` records the actual host OS/architecture, compiler and pnport SHA-256 digests, and cold/warm build durations without imposing a performance threshold. Timings are fixture observations, not the complete filesystem/memory/disk benchmark gate.

This passed on macOS 26.6.2 arm64 and in an offline Ubuntu 22.04 arm64 Docker container. It does not establish macOS 13, native Linux x64, peer-variant TypeScript, all process propagation, or installed pnport npm/archive conformance. Those release gates remain open.

## Dependencies and Integrations
[Native foundation](crates-pnport-foundation.md), [repository workflow](repository-workflow-contract.md), and consolidated public guides. Source workspace remains private and does not depend on unpublished platform packages.

## Change Triggers
Synchronize CLI/package versions, launchers, installers, CI/release selectors and all pnport contracts when interfaces change.

## References
- [Project](project-pnport.md)
- [Requirements](crates-pnport-requirements.md)
- [Repository defaults](repository-defaults.md)
