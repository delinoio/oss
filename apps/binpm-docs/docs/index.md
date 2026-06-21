# binpm

binpm is a Rust-based, Node-free binary package manager for installing and running command-line tools from release assets.

Use binpm when a tool already publishes native executables and you want project-local or user-level installation without npm, pnpm, yarn, Bun, Cargo install, Homebrew, or system package managers acting as the install backend.

## Start Here

- [Install binpm](/installation).
- [Declare and run a local tool](/getting-started).
- [Read the command overview](/commands).
- [Understand local manifests and lockfiles](/local-tooling).
- [Review cache and verification behavior](/cache-and-verification).

## Implementation Status

The binpm runtime has concrete Rust CLI behavior for command parsing, release source parsing, provider release lookup, target-aware asset selection, asset download, archive extraction for documented formats, global and project-local records, cache metadata, URL sanitization, SHA-256 validation, and atomic file writes.

Checksum sidecar and manifest discovery, signature verification, and global update behavior remain future implementation work. Documentation for this app must preserve those boundaries until the runtime contract changes.

## Source Specs

Stable source identifiers are:

```text
github:owner/repo[@version]
github:<host>/owner/repo[@version]
gitlab:<host>/<namespace...>/<project>[@version]
```

Direct URLs, registries, and package-manager backends are outside the current contract.

## Validation Commands

Use these commands from the repository root when changing binpm docs:

```bash
pnpm --filter binpm-docs test
pnpm --filter binpm-docs build
```
