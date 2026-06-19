# Getting Started

## Install

Nodeup releases are distributed as first-party prebuilt artifacts. Direct installer flows must verify `SHA256SUMS` entries and Sigstore bundle sidecars before installing a binary.

The repository keeps direct installers at:

- `scripts/install/nodeup.sh`
- `scripts/install/nodeup.ps1`

## First Use

Use Nodeup to resolve and install a Node.js version, then execute Node.js through the generated shim. Channel names and shim dispatch behavior are stable contracts and must stay consistent with the project contract in `docs/project-nodeup.md`.

## Package Manager Resolution

When Nodeup reads `package.json`, the `packageManager` value is intentionally strict for `yarn` and `pnpm`. Documentation examples should avoid ambiguous package-manager values.
