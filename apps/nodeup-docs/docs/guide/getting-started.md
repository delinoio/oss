# Getting Started

`nodeup` is a Rust-based Node.js version manager for predictable channel resolution and shim-based command execution.

## Install

Use the installation path documented by the active release contract for your environment. Published release assets include standalone archives for supported macOS, Linux, and Windows x64/arm64 hosts.

Direct installer flows verify `SHA256SUMS` entries and Sigstore bundle sidecars for bundle-enabled releases.

## Select a Runtime

`nodeup` resolves stable channel names and project metadata deterministically. Projects using `package.json` `packageManager` metadata should keep package manager identifiers strict and explicit.

## Validate

Use the CLI help and shell completion output to inspect supported commands for the installed version:

```bash
nodeup --help
```

Generated completions must remain deterministic for supported shells and top-level command scopes.
