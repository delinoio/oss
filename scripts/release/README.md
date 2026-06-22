# Release Automation Scripts

- `generate-checksums.sh`: produces `SHA256SUMS` and Sigstore bundle sidecars (`*.sigstore.json`) for each published artifact.
- `update-homebrew.sh`: renders and optionally pushes Homebrew formula updates to the tap repository `main` branch (binpm, nodeup, and with-watch consume prebuilt multi-OS release artifacts). For binpm, rendered URLs must point to the expected prebuilt archive names for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`; Homebrew is not a source-build fallback channel. In non-dry-run mode, it expects `HOMEBREW_TAP_GH_TOKEN` (or `GH_TOKEN`) with write access to the tap repository and sets a fixed local commit identity (`github-actions[bot] <github-actions@users.noreply.github.com>`) before creating the tap commit.

These scripts are designed for use by release workflows:

- `.github/workflows/release-cargo-mono.yml`
- `.github/workflows/release-binpm.yml`
- `.github/workflows/release-nodeup.yml`
- `.github/workflows/release-derun.yml`
- `.github/workflows/release-with-watch.yml`
