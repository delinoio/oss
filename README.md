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

| Project ID          | Purpose                                                                                                                                                                         | Status         | Public Docs                                              |
| ------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------- | -------------------------------------------------------- |
| `async-commit-hook` | Asynchronous repository checks against committed source, with durable receipts shared by humans and coding agents.                                                              | `active`       | [Documentation](https://oss.delino.io/async-commit-hook) |
| `binpm`             | Rust-based, Node-free binary package manager for installing and running command-line tools from release assets.                                                                 | `active`       | [Documentation](https://oss.delino.io/binpm)             |
| `bundlewhy`         | Local diagnostic build and web UI that explain missed bundler and minifier optimizations without changing project source.                                                       | `active`       | [Documentation](https://oss.delino.io/bundlewhy)         |
| `cargo-mono`        | Cargo subcommand for Rust monorepo lifecycle management, including version bump and publish orchestration.                                                                      | `active`       | [Documentation](https://oss.delino.io/cargo-mono)        |
| `clibox`            | Rust CLI pinned through npm for portable execution, configuration, system utilities, transformations, and readiness checks.                                                     | `active`       | [Documentation](https://oss.delino.io/clibox)            |
| `delcore`           | Shared tenants, authorization, metering, and tenant-wide billing for independently hosted Delino services.                                                                      | `active`       | —                                                        |
| `delidev`           | Personal AI sessions across projects, accounts, native harnesses, and execution machines, with desktop and mobile clients.                                                      | `active`       | —                                                        |
| `derun`             | Go CLI that preserves terminal fidelity for AI-agent workflows and bridges MCP output transport.                                                                                | `active`       | [Documentation](https://oss.delino.io/derun)             |
| `devhud`            | Desktop and mobile developer utility for RealQA capture, GitHub issue submission, Deck PR monitoring, and native widgets; some domains remain planned.                          | `active`       | [Documentation](https://oss.delino.io/devhud)            |
| `forge`             | Local Rust CLI and stdio MCP server for typed document authoring and preservation-aware PPTX editing.                                                                           | `active`       | —                                                        |
| `nodeup`            | Rust-based Node.js version manager with deterministic channel resolution, shell completions, and shim execution.                                                                | `active`       | [Documentation](https://oss.delino.io/nodeup)            |
| `pnport`            | Run PnP-unaware subprocesses against Yarn 4 dependency graphs; stable macOS/glibc Linux distribution retains acceptance limits, and Windows release acceptance remains pending. | `active`       | [Documentation](https://oss.delino.io/pnport)            |
| `public-docs`       | Rspress-based public documentation site for user-facing product and platform content.                                                                                           | `active`       | [Documentation](https://oss.delino.io)                   |
| `react-forge`       | React document sessions, TSX CLI, and local MCP server for Office documents, PDF, Figma, sprites, 3D scenes, and procedural audio; availability varies by feature.              | `active`       | [Documentation](https://oss.delino.io/react-forge)       |
| `runmoor`           | Stable manager for disposable Docker and Tart GitHub Actions runners with public documentation under the consolidated site.                                                     | `active`       | [Documentation](https://oss.delino.io/runmoor)           |
| `rustia`            | Serde-based LLM JSON parsing and function-calling tool adapter utilities split across runtime, aisdk adapter, and macro crates.                                                 | `active`       | —                                                        |
| `serde-feather`     | Size-first serialization contract split between runtime core and derive-macro crates.                                                                                           | `active`       | —                                                        |
| `typenative`        | Experimental React/TSX native application framework contract using ScriptC compilation and Blitz rendering without an embedded JavaScript engine.                               | `experimental` | —                                                        |
| `typeopt`           | Experimental Rust CLI for conservative type-aware JavaScript optimization before bundling and type-independent optimization after bundling.                                     | `experimental` | —                                                        |
| `with-watch`        | Rust-based CLI wrapper that reruns delegated shell utilities and arbitrary commands when inferred or explicit filesystem inputs change.                                         | `active`       | [Documentation](https://oss.delino.io/with-watch)        |

## Documentation Contract

- `docs/` is the source of truth for project contracts and implementation documents.
- Every project is defined by `docs/project-<id>.md` plus one or more domain contract documents.
- When ownership, interfaces, or runtime behavior changes, update the relevant `docs/` contracts in the same change.
- Start from [docs/README.md](docs/README.md) for the canonical documentation catalog.
