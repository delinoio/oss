# Project: clibox

## Goal
Provide a Rust CLI that JavaScript projects can pin through npm and their lockfiles. Issue #916 defines cross-platform environment execution, local port inspection/termination, resource opening, and text clipboard commands; issue #917 adds offline text replacement, time formatting/arithmetic, Base64 transformation, and hash generation/verification. Issue #919 adds stateless TCP, HTTP, and regular-file readiness waits. All three command sets coexist with help/version.

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
- Cargo and npm show command-specific help on stderr with exit code 2 when `run`, `port`, `clipboard`, `wait`, `text`, `time`, `base64`, or `hash` is missing a subcommand. Root no-argument and explicit help remain successful stdout output; other invalid inputs retain redacted diagnostics.
- Rust is explicitly selected instead of the repository's default Go language. Node.js 22+ is required only for the npm launcher; repository tooling uses Node.js 24.
- The Cargo manifest, Cargo.lock, source npm manifest, nine generated npm packages, and executable version agree exactly.
- macOS and Windows MSVC support x64/arm64; Linux supports x64/arm64 with separate glibc and musl packages.
- Consumers never compile Rust or run installation/download scripts. The npm launcher executes only its exact-version platform dependency.
- Manual `Release Project` versioning, exact-commit CI, crates.io publication, and the `clibox@v<version>` tag precede the downstream npm workflow.
- npm publication uses GitHub Actions OIDC and provenance from the complete verified CI artifact; setup and dry-run validation do not publish.
- The public OS commands are `run env`, `port which`, `port kill`, `open`, `clipboard copy`, and `clipboard paste`; no public Rust/JavaScript library API is provided.
- OS effects use current-user/session permissions without elevation, persistence, automatic retries, or telemetry. Diagnostics omit clipboard text, environment values, complete argv, URLs, and paths.
- The seven issue #917 commands share redacted diagnostics, cancellable processing, and permission-preserving atomic file publication. Timezone data is bundled identically across platform artifacts of a version. Handled transformation cancellation returns 1; OS utilities preserve their supported termination signals and delegated child status.
- `wait tcp`, `wait http`, and `wait file` share immediate nonoverlapping polling, unlimited default waiting, monotonic bounded attempts, handled cancellation, and redacted human/quiet/JSON results.
- Network checks run in Rust without external utilities. HTTPS verifies OS trust and hostname, negotiates HTTP/2 or HTTP/1.1, disables proxies/authentication/redirects, and finishes at response headers. Files are observed through metadata only.
- Automated local fixtures and Linux/macOS/Windows process CI are the completion gate. All eight artifact checks and Alpine consumers remain required; musl crypto compilation uses target-native `musl-gcc` while final linking retains pinned self-contained `rust-lld`.
- Automated parser, process, OS-adapter, and distribution tests are the completion gate. Real GUI, clipboard persistence, and application-wait verification remain follow-up work.
- A dedicated docs website and Homebrew remain outside this project. The two GNU Linux binaries also ship as signed GitHub Release archives and stable APT/DNF packages under [the Linux package contract](repository-linux-packages-contract.md).

## Change Policy
Update both domain contracts, this index, relevant AGENTS files, version synchronization, platform fixtures, and release workflows together when these boundaries change.

## References
- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
- [Project template](project-template.md)
