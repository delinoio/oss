# OSS Monorepo

This repository hosts multiple products and shared tooling across apps, CLIs, servers, Rust crates, and Connect RPC contracts.

## Repository Overview

- `apps/`: User-facing apps (Next.js, React Native, desktop frontends)
- `cmds/`: Go command tools and workflow CLIs
- `crates/`: Rust crates and Rust-based tooling
- `servers/`: Backend services and APIs
- `protos/`: Shared Connect RPC proto contracts
- `packaging/`: Package manager and release automation assets
- `docs/`: Canonical contracts, project indexes, and domain-level documentation

## Project Catalog

| Project ID | Purpose | Owned Paths | Status | Primary Docs |
| --- | --- | --- | --- | --- |
| `cargo-mono` | Cargo subcommand for Rust monorepo lifecycle management, including version bump and publish orchestration. | `crates/cargo-mono` | `active` | [project-cargo-mono](docs/project-cargo-mono.md), [crates-cargo-mono-foundation](docs/crates-cargo-mono-foundation.md) |
| `nodeup` | Rust-based Node.js version manager with deterministic channel resolution, shell completions, and shim execution. | `crates/nodeup` | `active` | [project-nodeup](docs/project-nodeup.md), [crates-nodeup-foundation](docs/crates-nodeup-foundation.md) |
| `derun` | Go CLI that preserves terminal fidelity for AI-agent workflows and bridges MCP output transport. | `cmds/derun` | `active` | [project-derun](docs/project-derun.md), [cmds-derun-foundation](docs/cmds-derun-foundation.md) |
| `ttl` | TTL compiler contract for task graph validation, execution, and cache-aware runtime behavior. | `cmds/ttlc` | `active` | [project-ttl](docs/project-ttl.md), [cmds-ttl-foundation](docs/cmds-ttl-foundation.md), [cmds-ttl-language-contract](docs/cmds-ttl-language-contract.md) |
| `mpapp` | Expo React Native app for mobile workflows with stable platform behavior and documented device capability usage. | `apps/mpapp` | `active` | [project-mpapp](docs/project-mpapp.md), [apps-mpapp-foundation](docs/apps-mpapp-foundation.md) |
| `devkit` | Next.js micro-app host platform with shared shell contracts and scaffold-first route conventions. | `apps/devkit` | `active` | [project-devkit](docs/project-devkit.md), [apps-devkit-foundation](docs/apps-devkit-foundation.md) |
| `devkit-commit-tracker` | Commit-level metric tracking mini app with time-series visualization and PR comparison reporting. | `apps/devkit/src/apps/commit-tracker`<br>`servers/commit-tracker` | `active (partial: collector deferred)` | [project-devkit-commit-tracker](docs/project-devkit-commit-tracker.md), [apps-devkit-commit-tracker-web-app-foundation](docs/apps-devkit-commit-tracker-web-app-foundation.md) |
| `devkit-remote-file-picker` | Signed-URL upload mini app for local file or camera input with direct object-storage upload and callbacks. | `apps/devkit/src/apps/remote-file-picker`<br>`servers/remote-file-picker` | `active (partial: production adapter deferred)` | [project-devkit-remote-file-picker](docs/project-devkit-remote-file-picker.md), [apps-devkit-remote-file-picker-foundation](docs/apps-devkit-remote-file-picker-foundation.md) |
| `thenv` | Secure `.env` sharing workflow across CLI, server, and Devkit web console components. | `cmds/thenv`<br>`servers/thenv`<br>`apps/devkit/src/apps/thenv` | `active` | [project-thenv](docs/project-thenv.md), [cmds-thenv-cli-foundation](docs/cmds-thenv-cli-foundation.md), [servers-thenv-server-foundation](docs/servers-thenv-server-foundation.md), [apps-thenv-web-console-foundation](docs/apps-thenv-web-console-foundation.md) |
| `serde-feather` | Size-first serialization contract split between runtime core and derive-macro crates. | `crates/serde-feather`<br>`crates/serde-feather-macros` | `active` | [project-serde-feather](docs/project-serde-feather.md), [crates-serde-feather-core-foundation](docs/crates-serde-feather-core-foundation.md), [crates-serde-feather-macros-foundation](docs/crates-serde-feather-macros-foundation.md) |
| `public-docs` | Mintlify-based public documentation site for user-facing product and platform content. | `apps/public-docs` | `active` | [project-public-docs](docs/project-public-docs.md), [apps-public-docs-foundation](docs/apps-public-docs-foundation.md) |
| `dexdex` | Connect RPC-first orchestration platform for CLI coding agents across desktop app, control plane, worker plane, and shared proto contracts. | `apps/dexdex`<br>`servers/dexdex-main-server`<br>`servers/dexdex-worker-server`<br>`protos/dexdex/v1` | `active + planned (poller auto-remediation)` | [project-dexdex](docs/project-dexdex.md), [apps-dexdex-desktop-app-foundation](docs/apps-dexdex-desktop-app-foundation.md), [servers-dexdex-main-server-foundation](docs/servers-dexdex-main-server-foundation.md), [servers-dexdex-worker-server-foundation](docs/servers-dexdex-worker-server-foundation.md), [protos-dexdex-v1-contract](docs/protos-dexdex-v1-contract.md) |

## Documentation Contract

- `docs/` is the source of truth for project contracts and implementation documents.
- Every project is defined by `docs/project-<id>.md` plus one or more domain contract documents.
- When ownership, interfaces, or runtime behavior changes, update the relevant `docs/` contracts in the same change.
- Start from [docs/README.md](docs/README.md) for the canonical documentation catalog.
