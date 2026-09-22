# pnport npm and native distribution

## Scope
`packages/pnport` owns the private npm source, launcher and six native packages. Native release archives include the matching interception artifacts. All distribution requirements in [#958](crates-pnport-requirements.md) remain release gates.

## Runtime and Language
Node.js 22+ for the built-in-only CommonJS launcher; Node.js 24 for repository build/release tooling. The standalone Rust CLI does not require Node.js. No bundler is needed for a native wrapper.

## Users and Operators
npm/Yarn 4 users and maintainers of fixed-version native, Homebrew and npm installs.

## Interfaces and Contracts
The public launcher is @delino/pnport with command pnport. Exact-version optional native packages use suffixes darwin-x64, darwin-arm64, win32-x64-msvc, win32-arm64-msvc, linux-x64-gnu, linux-arm64-gnu. Declare os/cpu and GNU libc where applicable; native packages use preferUnplugged so Yarn PnP can execute before virtualization starts. Require version agreement with the native CLI. Preserve argv, cwd, environment, stdio, signals and status. Missing or incompatible packages fail with reinstall guidance, never runtime downloads, compilation, PATH fallback or install scripts.

GitHub archives, checksums, Sigstore material, POSIX/PowerShell installers and prebuilt-only Homebrew cover the same six targets. Explicit installation controls updates/rollback. No crates.io, APT/RPM, Apple notarization or Windows Authenticode.

Manual Release Project adds pnport with immutable pnport@v<MAJOR.MINOR.PATCH> identity and synchronized Cargo/npm versions. Preserve coordinator prepare/registry/tag/summary behavior while skipping Cargo registry setup and credentials. Downstream publication verifies complete source-bound native/package sets and actual execution/install evidence before obtaining publication authority. Publish and verify all native npm dependencies before the launcher using provenance. Retries reuse identical immutable bytes; conflicts fail. No partial preview publication. Documentation uses the existing consolidated publisher.

## Storage
Generated packages use an explicit temporary directory or ignored dist. Different native cache formats coexist; an older fixed version never migrates newer entries. Remove repository-owned generated dist after validation.

## Security
Regular CI and dry runs are credential-free and never publish. Only guarded complete-set publication can obtain OIDC/write authority. Runtime is offline except for the child's own behavior. Do not modify user project manifests or install dependencies.

## Logging
Stable bounded launcher error codes on stderr; no raw argv, environments, child output or file content. Structured packaging events identify target, version, revision and integrity without credentials.

## Build and Test
Use Node built-in tests, six-target native execution and temporary npm/Yarn 4 PnP consumers installed with scripts disabled. Validate inventories, exact versions, executable modes, missing optional packages, source identity, signature verification and partial-publication recovery. Release cannot pass by cross-compilation alone.

## Dependencies and Integrations
[Native foundation](crates-pnport-foundation.md), [repository workflow](repository-workflow-contract.md), and consolidated public guides. Source workspace remains private and does not depend on unpublished platform packages.

## Change Triggers
Synchronize CLI/package versions, launchers, installers, CI/release selectors and all pnport contracts when interfaces change.

## References
- [Project](project-pnport.md)
- [Requirements](crates-pnport-requirements.md)
- [Repository defaults](repository-defaults.md)
