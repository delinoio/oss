# OSS Monorepo

This repository hosts multiple products and shared tooling across apps, CLIs, and Rust crates.

Browse the [public documentation](https://oss.delino.io) for product guides, installation instructions, and supported workflows.

## Repository Overview

- `apps/`: User-facing apps and documentation web surfaces
- `cmds/`: Go command tools and workflow CLIs
- `crates/`: Rust crates and Rust-based tooling
- `servers/`: Backend services
- `protos/`: Versioned protocol schemas and generated Go bindings
- `packages/`: Shared runtime packages, generated clients, and distribution tooling
- `packaging/`: Package manager and release automation assets
- `docs/`: Canonical contracts, project indexes, and domain-level documentation

## Project Catalog

This catalog summarizes the projects in this repository. Status does not establish release availability or platform acceptance; see each project index and its domain contracts for those boundaries.

| Project ID                                               | Purpose                                                                                                                                                                          | Status   | Public Docs                                              |
| -------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------- | -------------------------------------------------------- |
| [`async-commit-hook`](docs/project-async-commit-hook.md) | Asynchronous repository checks against committed source, with durable receipts shared by humans and coding agents.                                                               | `active` | [Documentation](https://oss.delino.io/async-commit-hook) |
| [`binpm`](docs/project-binpm.md)                         | Rust-based, Node-free binary package manager for installing and running command-line tools from release assets.                                                                  | `active` | [Documentation](https://oss.delino.io/binpm)             |
| [`cachecow`](docs/project-cachecow.md)                   | Rust CLI (`ccow`) that prepares independent writable build caches for Git worktrees with filesystem copy-on-write; 0.1.0 remains unreleased and native acceptance is unverified. | `active` | [Documentation](https://oss.delino.io/cachecow)          |
| [`cargo-mono`](docs/project-cargo-mono.md)               | Cargo subcommand for Rust monorepo lifecycle management, including version bump and publish orchestration.                                                                       | `active` | [Documentation](https://oss.delino.io/cargo-mono)        |
| [`clibox`](docs/project-clibox.md)                       | Rust CLI pinned through npm for portable execution, configuration, system utilities, transformations, and readiness checks.                                                      | `active` | [Documentation](https://oss.delino.io/clibox)            |
| [`jsonllm`](docs/project-jsonllm.md)                     | Rust derive macro and libraries that generate typed values from text or images through Clairic.                                                                                  | `active` | [Crate guide](crates/jsonllm/README.md)                  |
| [`nodeup`](docs/project-nodeup.md)                       | Rust-based Node.js version manager with deterministic channel resolution, shell completions, and shim execution.                                                                 | `active` | [Documentation](https://oss.delino.io/nodeup)            |
| [`pnport`](docs/project-pnport.md)                       | Run PnP-unaware subprocesses against Yarn 4 dependency graphs; stable macOS/glibc Linux distribution retains acceptance limits, and Windows release acceptance remains pending.  | `active` | [Documentation](https://oss.delino.io/pnport)            |
| [`public-docs`](docs/project-public-docs.md)             | Rspress-based public documentation site for user-facing product and platform content.                                                                                            | `active` | [Documentation](https://oss.delino.io)                   |
| [`runmoor`](docs/project-runmoor.md)                     | Stable manager for disposable Docker and Tart GitHub Actions runners with public documentation under the consolidated site.                                                      | `active` | [Documentation](https://oss.delino.io/runmoor)           |
| [`rustia`](docs/project-rustia.md)                       | Serde-based LLM JSON parsing and function-calling tool adapter utilities split across runtime, aisdk adapter, and macro crates.                                                  | `active` | —                                                        |
| [`serde-feather`](docs/project-serde-feather.md)         | Size-first serialization contract split between runtime core and derive-macro crates.                                                                                            | `active` | —                                                        |
| [`with-watch`](docs/project-with-watch.md)               | Rust-based CLI wrapper that reruns delegated shell utilities and arbitrary commands when inferred or explicit filesystem inputs change.                                          | `active` | [Documentation](https://oss.delino.io/with-watch)        |

## Documentation Contract

- `docs/` is the source of truth for project contracts and implementation documents.
- Every project is defined by `docs/project-<id>.md` plus one or more domain contract documents.
- When ownership, interfaces, or runtime behavior changes, update the relevant `docs/` contracts in the same change.
- Start from [docs/README.md](docs/README.md) for the canonical documentation catalog.

Delcore, DeliDev, and DeliTools are maintained in [delino-apps](https://github.com/delinoio/delino-apps).

Bundlewhy is maintained in [delinoio/bundlewhy](https://github.com/delinoio/bundlewhy).

Typeopt is maintained in [delinoio/typeopt](https://github.com/delinoio/typeopt).

## License

Repository-owned source and future distributions use [Apache-2.0](LICENSE). See [NOTICE](NOTICE) for attribution. Imported code and bundled fonts retain their original licenses and notices; see the [repository license contract](docs/repository-license-contract.md).
