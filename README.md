# OSS Monorepo

This repository hosts multiple products and shared tooling across apps, CLIs, and Rust crates.

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

| Project ID          | Purpose                                                                                                                                                                         | Status   | Public Docs                                              |
| ------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------- | -------------------------------------------------------- |
| `async-commit-hook` | Asynchronous repository checks against committed source, with durable receipts shared by humans and coding agents.                                                              | `active` | [Documentation](https://oss.delino.io/async-commit-hook) |
| `binpm`             | Rust-based, Node-free binary package manager for installing and running command-line tools from release assets.                                                                 | `active` | [Documentation](https://oss.delino.io/binpm)             |
| `cargo-mono`        | Cargo subcommand for Rust monorepo lifecycle management, including version bump and publish orchestration.                                                                      | `active` | [Documentation](https://oss.delino.io/cargo-mono)        |
| `clibox`            | Rust CLI pinned through npm for portable execution, configuration, system utilities, transformations, and readiness checks.                                                     | `active` | [Documentation](https://oss.delino.io/clibox)            |
| `nodeup`            | Rust-based Node.js version manager with deterministic channel resolution, shell completions, and shim execution.                                                                | `active` | [Documentation](https://oss.delino.io/nodeup)            |
| `pnport`            | Run PnP-unaware subprocesses against Yarn 4 dependency graphs; stable macOS/glibc Linux distribution retains acceptance limits, and Windows release acceptance remains pending. | `active` | [Documentation](https://oss.delino.io/pnport)            |
| `public-docs`       | Rspress-based public documentation site for user-facing product and platform content.                                                                                           | `active` | [Documentation](https://oss.delino.io)                   |
| `runmoor`           | Stable manager for disposable Docker and Tart GitHub Actions runners with public documentation under the consolidated site.                                                     | `active` | [Documentation](https://oss.delino.io/runmoor)           |
| `rustia`            | Serde-based LLM JSON parsing and function-calling tool adapter utilities split across runtime, aisdk adapter, and macro crates.                                                 | `active` | —                                                        |
| `serde-feather`     | Size-first serialization contract split between runtime core and derive-macro crates.                                                                                           | `active` | —                                                        |
| `with-watch`        | Rust-based CLI wrapper that reruns delegated shell utilities and arbitrary commands when inferred or explicit filesystem inputs change.                                         | `active` | [Documentation](https://oss.delino.io/with-watch)        |

## Documentation Contract

- `docs/` is the source of truth for project contracts and implementation documents.
- Every project is defined by `docs/project-<id>.md` plus one or more domain contract documents.
- When ownership, interfaces, or runtime behavior changes, update the relevant `docs/` contracts in the same change.
- Start from [docs/README.md](docs/README.md) for the canonical documentation catalog.

Delcore, DeliDev, and DeliTools are maintained in [delino-apps](https://github.com/delinoio/delino-apps).

Bundlewhy is maintained in [delinoio/bundlewhy](https://github.com/delinoio/bundlewhy).

Typeopt is maintained in [delinoio/typeopt](https://github.com/delinoio/typeopt).

TaiRouter (Team AI Router) is an open-source Delino Labs product with a [product page](https://labs.delino.io/tairouter/). It provides a self-hosted organization AI provider router, personal provider pools, a configuration CLI and a web console. Read the [project contracts](docs/project-tairouter.md), [public guides](https://docs.labs.delino.io/tairouter/) and [deployment guide](packaging/tairouter/README.md). With Docker running, use `pnpm dev:tairouter` to build and start a local evaluation at `http://127.0.0.1:8080`, including PostgreSQL, password-free synthetic login and automatic migrations. No external accounts or manual environment setup are needed to open the console. Use `pnpm dev:tairouter:stop` to stop it while keeping local data. Generated media requires real R2 configuration. Source development with configured external services uses `pnpm dev:tairouter:source`.
