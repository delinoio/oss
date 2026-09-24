# Project: clibox

## Goal
Provide a Rust CLI that JavaScript projects can pin through npm and their lockfiles. Issue #916 defines cross-platform environment execution, local port inspection/termination, resource opening, and text clipboard commands; issue #917 adds offline text replacement, time formatting/arithmetic, Base64 transformation, and hash generation/verification. Issue #919 adds stateless TCP, HTTP, and regular-file readiness waits. Issue #920 adds local configuration commands to list dotenv keys, merge dotenv layers, and normalize YAML references. Issue #951 adds portable CPU counts. Issue #953 adds local execution wrappers for rate limits, locks, HTTP service readiness, retries, and runtime/idle timeouts. All command sets coexist with help/version.

## Project ID
`clibox`

## Domain Ownership Map
- `crates/clibox`: Rust executable, logging/panic initialization, and root CLI composition.
- `crates/clibox-config`: dotenv/YAML command definitions, bounded processing, cancellation, and private atomic publication.
- `crates/clibox-system`: OS command definitions, runtime, errors, and adapters.
- `crates/clibox-transform`: offline command definitions, transformations, cancellation, and atomic publication.
- `crates/clibox-wait`: readiness command definitions, validation, probes, polling, and reporting.
- `packages/clibox`: private source workspace for the public npm launcher, platform packages, packaging, and publication tooling.
- `apps/public-docs/docs/clibox`: English public guides published at `https://oss.delino.io/clibox`.

## Domain Contract Documents
- [Rust foundation](crates-clibox-foundation.md)
- [npm distribution](packages-clibox-distribution-contract.md)
- [Public documentation](apps-clibox-docs-foundation.md)

## Cross-Domain Invariants
- The implemented CLI adopts the CLI consistency revision in the Rust contract: canonical names without old aliases, quiet/PID separation, explicit stdout output, validated force, complete help, and numeric owned-operation cancellation (130/143). Versioning remains in the manual release workflow.
- The executable crate and command are `clibox`; the public npm entry point is `@delino/clibox`.
- Native binaries and npm show command-specific help on stderr with exit code 2 when `run`, `port`, `clipboard`, `system`, `wait`, `text`, `time`, `base64`, `hash`, `dotenv`, or `yaml` is missing a subcommand. Root no-argument and explicit help remain successful stdout output and include the Cargo-derived version, Delino maintainer, repository, Apache-2.0 license, and GitHub Issues support URL; other invalid inputs retain redacted diagnostics. Subcommand help and `--version` output remain compact and compatible.
- Rust is explicitly selected instead of the repository's default Go language. Node.js 22+ is required only for the npm launcher; repository tooling uses Node.js 24.
- The executable Cargo manifest, its Cargo.lock entry, source npm manifest, nine generated npm packages, and executable version agree exactly.
- macOS and Windows MSVC support x64/arm64; Linux supports x64/arm64 with separate glibc and musl packages.
- Consumers never compile Rust or run installation/download scripts. The npm launcher executes only its exact-version platform dependency.
- Manual `Release Project` versioning and immutable release-source validation precede the `clibox@v<version>` tag and downstream npm/native workflow. clibox does not publish to crates.io or require a Cargo registry token. Main CI runs independently and does not gate the coordinator; downstream native builds, tests, and package validation remain required.
- All five Rust crates use `publish = false`. Only `clibox` depends on the four companions, through path dependencies. Companion versions begin at `0.1.0` and are not automatically bumped with product releases; no public Rust library API is added.
- npm publication uses GitHub Actions OIDC and provenance from the complete verified CI artifact; setup and dry-run validation do not publish.
- The public commands are `run env`, `run with-rate-limit`, `run with-lock`, `run with-service`, `run with-retry`, `run with-timeout`, `port list`, `port kill`, `open`, `clipboard copy`, `clipboard paste`, `system cpus`, `dotenv list`, `dotenv merge`, `yaml normalize`, `wait tcp`, `wait http`, `wait file`, `text replace`, `time format`, `time add`, `base64 encode`, `base64 decode`, `hash compute`, and `hash verify`; no public Rust/JavaScript library API is provided.
- `system cpus` defaults to Rust's unadjusted available-parallelism estimate; `--kind logical` reads online logical CPUs from the current OS. It has exact integer/JSON/quiet output, redacted classified failures, no substitute count, no stdin or external utility use, and no persistent state. The new command is implemented but not in published version 0.1.6.
- Configuration commands are offline Rust operations with 64 MiB input/output limits, private atomic file publication, cancellation, and redacted diagnostics. They introduce no application state or shell execution.
- Environment, port, open, and clipboard OS effects use current-user/session permissions without elevation, application persistence, automatic retries, or telemetry. Diagnostics omit clipboard text, environment values, complete argv, URLs, and paths.
- `wait tcp`, `wait http`, and `wait file` share immediate nonoverlapping polling, unlimited default waiting, monotonic bounded attempts, handled cancellation, and redacted human/quiet/JSON results.
- Network checks run in Rust without external utilities. HTTPS verifies OS trust and hostname, negotiates HTTP/2 or HTTP/1.1, disables proxies/authentication/redirects, and finishes at response headers. Files are observed through metadata only.
- All commands except named run locks and rate buckets remain stateless. Those two controls retain only private, hashed, local same-user coordination state; they add no synchronized settings, telemetry, service, queue, or cross-machine coordination. No other persistent application state, remote telemetry, public library API, or Homebrew is added.
- The seven issue #917 commands share redacted diagnostics, cancellable processing, and permission-preserving atomic file publication. Timezone data is bundled identically across platform artifacts of a version. Owned operations return numeric 130 for Ctrl+C/Windows Ctrl+Break and 143 for Unix SIGTERM; `run env` preserves delegated child status and Unix signal identity.
- Automated local fixtures and Linux/macOS/Windows process CI are the completion gate. All eight artifact checks and Alpine consumers remain required; musl crypto compilation uses target-native `musl-gcc` while final linking retains pinned self-contained `rust-lld`.
- Automated parser, process, OS-adapter, and distribution tests are the completion gate. Real GUI, clipboard persistence, and application-wait verification remain follow-up work.
- Linux/macOS/Windows process tests and installed npm/pnpm smoke tests cover all three configuration commands and environment execution. Eight-target and Alpine release gates remain unchanged.
- Public guides are owned by the consolidated `public-docs` site under `/clibox`; no standalone documentation workspace or deployment is added. Homebrew remains excluded. The two GNU Linux binaries also ship as signed GitHub Release archives and stable APT/DNF packages under [the Linux package contract](repository-linux-packages-contract.md).

## Change Policy
Update the relevant domain contracts, this index, relevant AGENTS files, version synchronization, platform fixtures, and release workflows together when these boundaries change.

## References
- [Repository defaults](repository-defaults.md)
- [Repository workflow](repository-workflow-contract.md)
- [Project template](project-template.md)
