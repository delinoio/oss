# Homebrew Packaging Templates

This directory contains Homebrew formula/cask templates rendered by `scripts/release/update-homebrew.sh`.

Supported package identifiers:

- `binpm` (prebuilt formula: `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`)
- `nodeup` (prebuilt formula: `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`)
- `with-watch` (prebuilt formula: `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`)
- `derun` (prebuilt formula: `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`)
- `runmoor` (prebuilt formula: macOS 14+ `darwin/arm64` only)
- `clibox` (prebuilt formula: macOS `darwin/amd64` and `darwin/arm64`, starting with the next release)

Runmoor uses the signed public release archive without rebuilding it. Its dedicated workflow verifies all release assets and runs native Homebrew installation, tests and audit before obtaining tap publication credentials. Initial publication and recovery can target an existing public version without a new release. See `docs/cmds-runmoor-foundation.md` and this directory's `AGENTS.md` for the contract.

binpm Homebrew packaging is prebuilt-only. The formula consumes binpm release archives and must not add a source build fallback unless `docs/project-binpm.md` and `docs/crates-binpm-foundation.md` define a new distribution contract. Release rendering validates binpm formula URL basenames against the expected archive names before pushing tap updates.
