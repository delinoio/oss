# clibox Rust foundation

## Scope
`crates/clibox` owns the Rust executable and public crates.io package `clibox`.

## Runtime and Language
Rust 2021, MIT license, repository-pinned Rust toolchain, and `clap` argument parsing. It is an explicit Cargo workspace member and an approved crates.io publication target.

## Users and Operators
Developers invoking a pinned CLI, and maintainers building and releasing the same executable through Cargo and npm.

## Interfaces and Contracts
- `clibox`, `clibox --help`, and `clibox -h` print help to stdout and exit successfully.
- `clibox --version` and `clibox -V` print `clibox <Cargo package version>` and exit successfully.
- Unknown arguments and positional commands print clap diagnostics to stderr and exit with code 2.
- No domain commands or public Rust library API are defined yet.

## Storage
No persistent state, project configuration, or caches.

## Security
The scaffold performs no network access, filesystem mutation, or delegated command execution. Cargo publication is explicit and gated by the repository release coordinator.

## Logging
Help and version are user output, not logs. Parse failures use clap diagnostics. There are no business or system operations requiring a tracing subscriber in this scaffold; future runtime operations must use structured `tracing` without polluting stdout.

## Build and Test
- `cargo run -p clibox -- --help`
- `cargo test -p clibox`
- Root `cargo test` and `cargo fmt --all --check` remain required repository checks.
- `cargo publish -p clibox --dry-run` verifies standalone crate packaging.
- Process tests cover help, version, no arguments, malformed input, output streams, and exit codes. npm packaging also checks the native executable version against both source manifests.

## Dependencies and Integrations
Uses `clap`; npm distribution is owned by `packages/clibox`. The root cargo-mono tag allowlist includes `clibox` and releases use `clibox@v<version>`.

## Change Triggers
Update the project index, npm distribution contract, `crates/AGENTS.md`, and root release contracts when command behavior, naming, versions, platforms, or publication changes.

## References
- [Project index](project-clibox.md)
- [Repository defaults](repository-defaults.md)
