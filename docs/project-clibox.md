# Project: clibox

## Goal
Provide a Rust CLI that JavaScript projects can pin through npm and their lockfiles. Issue #916 defines cross-platform environment execution, local port inspection/termination, resource opening, and text clipboard commands alongside help/version. Issue #920 adds local configuration commands to list dotenv keys, merge dotenv layers, and normalize YAML references.

## Project ID
`clibox`

## Domain Ownership Map
- `crates/clibox`: Rust executable and crates.io package.
- `packages/clibox`: private source workspace for the public npm launcher, platform packages, packaging, and publication tooling.

## Domain Contract Documents
- [Rust foundation](crates-clibox-foundation.md)
- [npm distribution](packages-clibox-distribution-contract.md)

## Cross-Domain Invariants
- The crate and command are `clibox`; the public npm entry point is `@delino/clibox`.
- Rust is explicitly selected instead of the repository's default Go language. Node.js 22+ is required only for the npm launcher; repository tooling uses Node.js 24.
- The Cargo manifest, Cargo.lock, source npm manifest, nine generated npm packages, and executable version agree exactly.
- macOS and Windows MSVC support x64/arm64; Linux supports x64/arm64 with separate glibc and musl packages.
- Consumers never compile Rust or run installation/download scripts. The npm launcher executes only its exact-version platform dependency.
- Manual `Release Project` versioning, exact-commit CI, crates.io publication, and the `clibox@v<version>` tag precede the downstream npm workflow.
- npm publication uses GitHub Actions OIDC and provenance from the complete verified CI artifact; setup and dry-run validation do not publish.
- The public commands are `run env`, `port which`, `port kill`, `open`, `clipboard copy`, `clipboard paste`, `dotenv list`, `dotenv merge`, and `yaml normalize`; no public Rust/JavaScript library API is provided.
- Configuration commands are offline Rust operations with 64 MiB input/output limits, private atomic file publication, cancellation, and redacted diagnostics. They introduce no application state or shell execution.
- OS effects use current-user/session permissions without elevation, application persistence, automatic retries, or telemetry. Diagnostics omit clipboard text, environment values, complete argv, URLs, and paths.
- Automated parser, process, OS-adapter, and distribution tests are the completion gate. Real GUI, clipboard persistence, and application-wait verification remain follow-up work.
- Linux/macOS/Windows process tests and installed npm/pnpm smoke tests cover all three configuration commands and environment execution. Eight-target and Alpine release gates remain unchanged.
- A docs website, Homebrew, and public GitHub Release binaries remain outside this project.

## Change Policy
Update both domain contracts, this index, relevant AGENTS files, version synchronization, platform fixtures, and release workflows together when these boundaries change.

## References
- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
- [Project template](project-template.md)
