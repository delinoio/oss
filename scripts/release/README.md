# Release Automation Scripts

- `generate-checksums.sh`: produces `SHA256SUMS` and Sigstore bundle sidecars (`*.sigstore.json`) for each published artifact.
- `devhud-release.mjs`: validates the exact `devhud@v<MAJOR.MINOR.PATCH>` private-build identity, source versions, artifact matrix, and secret-redacted signing preflight.
- `generate-devhud-updater.mjs`: creates the ten target/package-specific updater envelopes plus detached Ed25519 artifact and manifest signatures.
- `generate-devhud-supply-chain.mjs`: creates one SPDX 2.3 SBOM and digest-bound SLSA v1 provenance statement per DevHud primary artifact.
- `devhud-evidence.mjs` and `validate-devhud-private-build.mjs`: record platform checks, merge only the complete target set, and validate the private signed candidate without treating it as public-ready.
- `finalize-devhud-deb.sh`: deterministically adds package-owned system Chrome Native Messaging registration and fail-closed removal hooks to the pinned Tauri Debian payload.

For DevHud, call `generate-checksums.sh --artifacts-dir <release-root> --sigstore-dir <release-root>/sigstore`. Recursive relative paths are sorted with the C locale. The script owns only `*.sigstore.json` outputs and deliberately preserves platform certificates/signatures and `updater/signatures/**`.
- `update-homebrew.sh`: renders and optionally pushes Homebrew formula updates to the tap repository `main` branch (binpm, nodeup, and with-watch consume prebuilt multi-OS release artifacts). For binpm, rendered URLs must point to the expected prebuilt archive names for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`; Homebrew is not a source-build fallback channel. In non-dry-run mode, it expects `HOMEBREW_TAP_GH_TOKEN` (or `GH_TOKEN`) with write access to the tap repository and sets a fixed local commit identity (`github-actions[bot] <github-actions@users.noreply.github.com>`) before creating the tap commit.

These scripts are designed for use by release workflows:

- DevHud desktop publication must first run `pnpm --filter devhud release:validate-updater`. The gate recomputes the committed `devhud-release-root-v1` public-key fingerprint and intentionally fails while the explicit placeholder is marked non-production. Signing stays offline; the application and API ship no signing key or token. See `docs/apps-devhud-updater-contract.md`.

- `.github/workflows/release-cargo-mono.yml`
- `.github/workflows/release-binpm.yml`
- `.github/workflows/release-nodeup.yml`
- `.github/workflows/release-derun.yml`
- `.github/workflows/release-with-watch.yml`
