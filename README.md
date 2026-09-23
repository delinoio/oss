# OSS Monorepo

This repository hosts multiple products and shared tooling across apps, CLIs, and Rust crates.

## Repository Overview

- `apps/`: User-facing apps and documentation web surfaces
- `cmds/`: Go command tools and workflow CLIs
- `crates/`: Rust crates and Rust-based tooling
- `packaging/`: Package manager and release automation assets
- `docs/`: Canonical contracts, project indexes, and domain-level documentation

## Project Catalog

| Project ID | Purpose | Owned Paths | Status | Primary Docs |
| --- | --- | --- | --- | --- |
| `binpm` | Rust-based, Node-free binary package manager for installing and running command-line tools from release assets. | `crates/binpm` | `active` | [project-binpm](docs/project-binpm.md), [crates-binpm-foundation](docs/crates-binpm-foundation.md) |
| `cargo-mono` | Cargo subcommand for Rust monorepo lifecycle management, including version bump and publish orchestration. | `crates/cargo-mono` | `active` | [project-cargo-mono](docs/project-cargo-mono.md), [crates-cargo-mono-foundation](docs/crates-cargo-mono-foundation.md) |
| `nodeup` | Rust-based Node.js version manager with deterministic channel resolution, shell completions, and shim execution. | `crates/nodeup` | `active` | [project-nodeup](docs/project-nodeup.md), [crates-nodeup-foundation](docs/crates-nodeup-foundation.md) |
| `with-watch` | Rust-based CLI wrapper that reruns delegated shell utilities and arbitrary commands when inferred or explicit filesystem inputs change. | `crates/with-watch` | `active` | [project-with-watch](docs/project-with-watch.md), [crates-with-watch-foundation](docs/crates-with-watch-foundation.md) |
| `derun` | Go CLI that preserves terminal fidelity for AI-agent workflows and bridges MCP output transport. | `cmds/derun` | `active` | [project-derun](docs/project-derun.md), [cmds-derun-foundation](docs/cmds-derun-foundation.md) |
| `runmoor` | Stable manager for disposable Docker and Tart GitHub Actions runners with public documentation under the consolidated site. | `cmds/runmoor`<br>`apps/public-docs/docs/runmoor` | `active` | [project-runmoor](docs/project-runmoor.md), [cmds-runmoor-foundation](docs/cmds-runmoor-foundation.md), [Runmoor public documentation contract](docs/apps-runmoor-docs-foundation.md) |
| `serde-feather` | Size-first serialization contract split between runtime core and derive-macro crates. | `crates/serde-feather`<br>`crates/serde-feather-macros` | `active` | [project-serde-feather](docs/project-serde-feather.md), [crates-serde-feather-core-foundation](docs/crates-serde-feather-core-foundation.md), [crates-serde-feather-macros-foundation](docs/crates-serde-feather-macros-foundation.md) |
| `rustia` | Serde-based LLM JSON parsing and function-calling tool adapter utilities split across runtime, aisdk adapter, and macro crates. | `crates/rustia`<br>`crates/rustia-llm`<br>`crates/rustia-macros` | `active` | [project-rustia](docs/project-rustia.md), [crates-rustia-core-foundation](docs/crates-rustia-core-foundation.md), [crates-rustia-llm-foundation](docs/crates-rustia-llm-foundation.md), [crates-rustia-macros-foundation](docs/crates-rustia-macros-foundation.md) |
| `public-docs` | Rspress-based public documentation site for user-facing product and platform content. | `apps/public-docs` | `active` | [project-public-docs](docs/project-public-docs.md), [apps-public-docs-foundation](docs/apps-public-docs-foundation.md) |

## Documentation Contract

- `docs/` is the source of truth for project contracts and implementation documents.
- Every project is defined by `docs/project-<id>.md` plus one or more domain contract documents.
- When ownership, interfaces, or runtime behavior changes, update the relevant `docs/` contracts in the same change.
- Start from [docs/README.md](docs/README.md) for the canonical documentation catalog.
