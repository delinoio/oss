# Release Automation Scripts

- `generate-checksums.sh`: produces `SHA256SUMS` and cosign signatures.
- `update-homebrew.sh`: renders and optionally pushes Homebrew formula/cask updates to the tap repository `main` branch (DexDex server formulas and nodeup consume prebuilt multi-OS release artifacts). In non-dry-run mode, it expects `HOMEBREW_TAP_GH_TOKEN` (or `GH_TOKEN`) with write access to the tap repository and sets a fixed local commit identity (`github-actions[bot] <github-actions@users.noreply.github.com>`) before creating the tap commit.

These scripts are designed for use by release workflows:

- `.github/workflows/release-cargo-mono.yml`
- `.github/workflows/release-nodeup.yml`
- `.github/workflows/release-derun.yml`
- `.github/workflows/release-dexdex.yml`
