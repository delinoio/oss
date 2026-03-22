# Release Automation Scripts

- `generate-checksums.sh`: produces `SHA256SUMS` and cosign signatures.
- `update-homebrew.sh`: renders and optionally submits Homebrew formula/cask updates (DexDex server formulas consume prebuilt multi-OS release artifacts). In non-dry-run mode, it sets a fixed local commit identity (`github-actions[bot] <github-actions@users.noreply.github.com>`) before creating the tap commit.

These scripts are designed for use by release workflows:

- `.github/workflows/release-cargo-mono.yml`
- `.github/workflows/release-nodeup.yml`
- `.github/workflows/release-derun.yml`
- `.github/workflows/release-dexdex.yml`
