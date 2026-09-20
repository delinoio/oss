# Project: clibox

## Goal
Provide a Rust CLI that JavaScript projects can pin through npm and their lockfiles. Issue #916 defines cross-platform environment execution, local port inspection/termination, resource opening, and text clipboard commands; issue #917 adds offline text replacement, time formatting/arithmetic, Base64 transformation, and hash generation/verification. Issue #919 adds stateless TCP, HTTP, and regular-file readiness waits. Issue #920 adds local configuration commands to list dotenv keys, merge dotenv layers, and normalize YAML references. All four command sets coexist with help/version.

## Project ID
`clibox`

## Domain Ownership Map
- `crates/clibox`: Rust executable, logging/panic initialization, and root CLI composition.
- `crates/clibox-config`: dotenv/YAML command definitions, bounded processing, cancellation, and private atomic publication.
- `crates/clibox-system`: OS command definitions, runtime, errors, and adapters.
- `crates/clibox-transform`: offline command definitions, transformations, cancellation, and atomic publication.
- `crates/clibox-wait`: readiness command definitions, validation, probes, polling, and reporting.
- `packages/clibox`: private source workspace for the public npm launcher, platform packages, packaging, and publication tooling.

## Domain Contract Documents
- [Rust foundation](crates-clibox-foundation.md)
- [npm distribution](packages-clibox-distribution-contract.md)

## Cross-Domain Invariants
- The executable crate and command are `clibox`; the public npm entry point is `@delino/clibox`.
- Native binaries and npm show command-specific help on stderr with exit code 2 when `run`, `port`, `clipboard`, `wait`, `text`, `time`, `base64`, `hash`, `dotenv`, or `yaml` is missing a subcommand. Root no-argument and explicit help remain successful stdout output; other invalid inputs retain redacted diagnostics.
- Rust is explicitly selected instead of the repository's default Go language. Node.js 22+ is required only for the npm launcher; repository tooling uses Node.js 24.
- The executable Cargo manifest, its Cargo.lock entry, source npm manifest, nine generated npm packages, and executable version agree exactly.
- macOS and Windows MSVC support x64/arm64; Linux supports x64/arm64 with separate glibc and musl packages.
- Consumers never compile Rust or run installation/download scripts. The npm launcher executes only its exact-version platform dependency.
- Manual `Release Project` versioning and exact-commit CI precede the `clibox@v<version>` tag and downstream npm/native workflow. clibox does not publish to crates.io or require a Cargo registry token.
- All five Rust crates use `publish = false`. Only `clibox` depends on the four companions, through path dependencies. Companion versions begin at `0.1.0` and are not automatically bumped with product releases; no public Rust library API is added.
- npm publication uses GitHub Actions OIDC and provenance from the complete verified CI artifact; setup and dry-run validation do not publish.
- The public commands are `run env`, `port which`, `port kill`, `open`, `clipboard copy`, `clipboard paste`, `dotenv list`, `dotenv merge`, `yaml normalize`, `wait tcp`, `wait http`, `wait file`, `text replace`, `time format`, `time add`, `base64 encode`, `base64 decode`, `hash encode`, and `hash verify`; no public Rust/JavaScript library API is provided.
- Configuration commands are offline Rust operations with 64 MiB input/output limits, private atomic file publication, cancellation, and redacted diagnostics. They introduce no application state or shell execution.
- Environment, port, open, and clipboard OS effects use current-user/session permissions without elevation, application persistence, automatic retries, or telemetry. Diagnostics omit clipboard text, environment values, complete argv, URLs, and paths.
- `wait tcp`, `wait http`, and `wait file` share immediate nonoverlapping polling, unlimited default waiting, monotonic bounded attempts, handled cancellation, and redacted human/quiet/JSON results.
- Network checks run in Rust without external utilities. HTTPS verifies OS trust and hostname, negotiates HTTP/2 or HTTP/1.1, disables proxies/authentication/redirects, and finishes at response headers. Files are observed through metadata only.
- No persistent application state, remote telemetry, public library API, docs website, or Homebrew are added.
- The seven issue #917 commands share redacted diagnostics, cancellable processing, and permission-preserving atomic file publication. Timezone data is bundled identically across platform artifacts of a version. Handled transformation cancellation returns 1; OS utilities preserve their supported termination signals and delegated child status.
- Automated local fixtures and Linux/macOS/Windows process CI are the completion gate. All eight artifact checks and Alpine consumers remain required; musl crypto compilation uses target-native `musl-gcc` while final linking retains pinned self-contained `rust-lld`.
- Automated parser, process, OS-adapter, and distribution tests are the completion gate. Real GUI, clipboard persistence, and application-wait verification remain follow-up work.
- Linux/macOS/Windows process tests and installed npm/pnpm smoke tests cover all three configuration commands and environment execution. Eight-target and Alpine release gates remain unchanged.
- A dedicated docs website and Homebrew remain outside this project. The two GNU Linux binaries also ship as signed GitHub Release archives and stable APT/DNF packages under [the Linux package contract](repository-linux-packages-contract.md).

## Change Policy
Update both domain contracts, this index, relevant AGENTS files, version synchronization, platform fixtures, and release workflows together when these boundaries change.

## References
- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
- [Project template](project-template.md)
