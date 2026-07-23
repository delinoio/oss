# OSS Monorepo

This repository hosts multiple products and shared tooling across apps, CLIs, servers, Rust crates, and Connect RPC contracts.

## Repository Overview

- `apps/`: User-facing apps (React Native and documentation web surfaces)
- `cmds/`: Go command tools and workflow CLIs
- `crates/`: Rust crates and Rust-based tooling
- `servers/`: Backend services and APIs
- `protos/`: Shared Connect RPC proto contracts
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
| `ttl` | TTL compiler contract for task graph validation, execution, and cache-aware runtime behavior. | `cmds/ttlc` | `active` | [project-ttl](docs/project-ttl.md), [cmds-ttl-foundation](docs/cmds-ttl-foundation.md), [cmds-ttl-language-contract](docs/cmds-ttl-language-contract.md) |
| `mpapp` | Expo React Native app for mobile workflows with stable platform behavior and documented device capability usage. | `apps/mpapp` | `active` | [project-mpapp](docs/project-mpapp.md), [apps-mpapp-foundation](docs/apps-mpapp-foundation.md) |
| `thenv` | Secure `.env` sharing workflow across CLI and server components. | `cmds/thenv`<br>`servers/thenv` | `active` | [project-thenv](docs/project-thenv.md), [cmds-thenv-cli-foundation](docs/cmds-thenv-cli-foundation.md), [servers-thenv-server-foundation](docs/servers-thenv-server-foundation.md) |
| `serde-feather` | Size-first serialization contract split between runtime core and derive-macro crates. | `crates/serde-feather`<br>`crates/serde-feather-macros` | `active` | [project-serde-feather](docs/project-serde-feather.md), [crates-serde-feather-core-foundation](docs/crates-serde-feather-core-foundation.md), [crates-serde-feather-macros-foundation](docs/crates-serde-feather-macros-foundation.md) |
| `rustia` | Serde-based LLM JSON parsing and function-calling tool adapter utilities split across runtime, aisdk adapter, and macro crates. | `crates/rustia`<br>`crates/rustia-llm`<br>`crates/rustia-macros` | `active` | [project-rustia](docs/project-rustia.md), [crates-rustia-core-foundation](docs/crates-rustia-core-foundation.md), [crates-rustia-llm-foundation](docs/crates-rustia-llm-foundation.md), [crates-rustia-macros-foundation](docs/crates-rustia-macros-foundation.md) |
| `public-docs` | Rspress-based public documentation site for user-facing product and platform content. | `apps/public-docs` | `active` | [project-public-docs](docs/project-public-docs.md), [apps-public-docs-foundation](docs/apps-public-docs-foundation.md) |
| `delidev` | Planned React/TypeScript/Rsbuild Cloudflare Pages PWA for the DeliDev developer-tools catalog and organization UI. | `apps/delidev-app` | `planned` | [project-delidev](docs/project-delidev.md), [apps-delidev-app-foundation](docs/apps-delidev-app-foundation.md) |
| `delibase` | Planned reusable Go/PostgreSQL/sqlc organization, billing, and metered-usage service with a shared versioned Connect API. | `servers/delibase`<br>`protos/delibase` | `planned` | [project-delibase](docs/project-delibase.md), [servers-delibase-server-foundation](docs/servers-delibase-server-foundation.md), [protos-delibase-api-contract](docs/protos-delibase-api-contract.md) |

## Documentation Contract

- `docs/` is the source of truth for project contracts and implementation documents.
- Every project is defined by `docs/project-<id>.md` plus one or more domain contract documents.
- When ownership, interfaces, or runtime behavior changes, update the relevant `docs/` contracts in the same change.
- Start from [docs/README.md](docs/README.md) for the canonical documentation catalog.
- `servers/internal` is repository-shared Go infrastructure consumed by `delibase` and is intentionally not a project catalog entry.
