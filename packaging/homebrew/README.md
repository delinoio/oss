# Homebrew Packaging Templates

This directory contains Homebrew formula/cask templates rendered by `scripts/release/update-homebrew.sh`.

Supported package identifiers:

- `binpm` (prebuilt formula: `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`)
- `nodeup` (prebuilt formula: `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`)
- `with-watch` (prebuilt formula: `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`)
- `derun`

binpm Homebrew packaging is prebuilt-only. The formula consumes binpm release archives and must not add a source build fallback unless `docs/project-binpm.md` and `docs/crates-binpm-foundation.md` define a new distribution contract. Release rendering validates binpm formula URL basenames against the expected archive names before pushing tap updates.
