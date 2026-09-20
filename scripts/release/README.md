# Release Automation Scripts

Use **Actions → Release Project → Run workflow** on `main`, choose one of `binpm`, `cargo-mono`, `nodeup`, `with-watch`, `derun`, `runmoor`, and choose `patch`, `minor`, or `major`. The workflow commits the version, waits for its exact CI Result, publishes the selected crate when applicable, and pushes the single release tag. That tag starts the existing signed binary/Homebrew release asynchronously; the coordinator does not wait for or report its completion. Main pushes no longer publish the Rust workspace. Configuration, concurrency, and recovery are defined in `docs/repository-workflow-contract.md`.

Configure the private `delino-release-bot` app on only `oss` and `homebrew-tap`, with Contents write and Metadata read. Add its always-bypass entry to the applicable organization and repository main rulesets. Store `DELINO_RELEASE_BOT_CLIENT_ID` as an `oss` Actions variable and `DELINO_RELEASE_BOT_PRIVATE_KEY` as an `oss` Actions secret. Generate the key directly into secret storage; never commit it or retain it in logs/artifacts. Keep `CARGO_REGISTRY_TOKEN` for Rust uploads.

- `project.mjs`: closed project/version adapters, exact version-only run journal, commit and single-tag pushes, and bounded CI inspection. `RELEASE_PROJECT=binpm RELEASE_BUMP=patch node scripts/release/project.mjs plan` is a read-only local preview requiring no credentials. Other coordinator commands are manual-main workflow operations.
- Rerun the same coordinator run after an interrupted commit, registry, or tag phase to preserve its version. For a failed CI run, rerun that exact run from its Actions page before retrying the coordinator. For a failed downstream tag workflow, rerun that exact downstream workflow independently; do not retry the coordinator solely for that failure. Existing tags must point to the recorded commit. No recovery path force-pushes, republishes all crates, or automatically overwrites public assets.

- `generate-checksums.sh`: produces `SHA256SUMS` and Sigstore bundle sidecars (`*.sigstore.json`) for each published artifact.
- `devhud-release.mjs`: validates the exact `devhud@v<MAJOR.MINOR.PATCH>` private-build identity, source versions, artifact matrix, and secret-redacted signing preflight.
- `generate-devhud-updater.mjs`: creates the ten target/package-specific updater envelopes plus detached Ed25519 artifact and manifest signatures.
- `generate-devhud-supply-chain.mjs`: validates the component-bearing SPDX 2.3 SBOM produced from each artifact's unpacked/package-aware build layout and creates its digest-bound SLSA v1 provenance statement.
- `devhud-evidence.mjs` and `validate-devhud-private-build.mjs`: record platform checks, merge only the complete target set, and validate the private signed candidate without treating it as public-ready.
- `devhud-public-release.mjs`: validates the exact `main` version/tag and selected source-revision identity, live-publication configuration-name set, closed channel state machine, rollback boundary, secret-redacted dry/public plan, and exposes the rollback policy through its direct CLI.
- `devhud-candidate-artifact.mjs`: discovers the oldest retained complete candidate and its candidate-bound release-configuration fingerprint across recovery runs so signed bytes, provenance identity, and destinations are preserved.
- `package-devhud-updater-input.mjs`: packages only signed updater manifests and signatures with deterministic path order, ownership, modes, timestamps, and gzip headers for idempotent controller retries.
- `validate-devhud-public-assets.mjs`: validates the exact downloaded GitHub Release and extracted archive inventory, the complete expected keyless bundle set, signed checksums and bundles for every public binary, evidence payload, and remotely served updater manifest, the release index, and updater manifests against the public package bytes.
- `devhud-live-preflight.mjs`: authenticates every documented GitHub, store, registry, Logto, generated asset route, docs, PostgreSQL, R2, and exact-identity provider-neutral deployment boundary without publishing, including a read-only App Store submission-authority probe without requiring private package-signing keys.
- `devhud-store-release.mjs`: distinguishes absent, processing, and processed App Store builds read-only; creates an absent exact version only during submission; idempotently reconciles store submissions and publication; and discovers, cancels, or verifies the exact release before an automatic infrastructure rollback.
- `devhud-release-controller.mjs`: sends identity-bound, OIDC-authenticated prepare/promote/status/rollback requests to the operator-selected deployment controller.
- `validate-devhud-ios-signing.mjs`: verifies that the signed iOS app and both extensions use exact App Store identities, profiles, certificates, and production entitlements before IPA evidence is recorded.
- `finalize-devhud-deb.sh`: deterministically adds package-owned system Chrome Native Messaging registration and a fail-closed removal hook that re-enters each affected active user session for authenticated revocation and credential cleanup before deleting user or system registration.

For DevHud, call `generate-checksums.sh --artifacts-dir <release-root> --sigstore-dir <release-root>/sigstore`. Recursive relative paths are sorted with the C locale. The script owns only `*.sigstore.json` outputs and deliberately preserves platform certificates/signatures and `updater/signatures/**`.
- `update-homebrew.sh`: renders and optionally pushes Homebrew formula updates to the tap repository `main` branch (binpm, nodeup, and with-watch consume prebuilt multi-OS release artifacts). For binpm, rendered URLs must point to the expected prebuilt archive names for `darwin/amd64`, `darwin/arm64`, `linux/amd64`, and `linux/arm64`; Homebrew is not a source-build fallback channel. In non-dry-run mode, it expects `HOMEBREW_TAP_GH_TOKEN` (or `GH_TOKEN`) with write access to the tap repository and uses `RELEASE_BOT_NAME` and `RELEASE_BOT_EMAIL` resolved from the dedicated app before creating the tap commit (the existing `github-actions[bot]` identity remains the standalone-script fallback). Workflows provide a fresh tap-only installation token, and Git authentication uses an environment-backed credential helper rather than a token-bearing URL.

These scripts are designed for use by release workflows:

- DevHud desktop publication must first run `pnpm --filter devhud release:validate-updater`. The gate recomputes the committed `devhud-release-root-v1` public-key fingerprint and intentionally fails while the explicit placeholder is marked non-production. Signing stays offline; the application and API ship no signing key or token. See `docs/apps-devhud-updater-contract.md`.

- `.github/workflows/release-project.yml` (manual project and SemVer selection)
- `.github/workflows/release-cargo-mono.yml`
- `.github/workflows/release-binpm.yml`
- `.github/workflows/release-nodeup.yml`
- `.github/workflows/release-derun.yml`
- `.github/workflows/release-with-watch.yml`
- `.github/workflows/release-devhud.yml` (manual `dry-run` or protected coordinated `release`, with an optional exact ancestor revision for retained-candidate recovery; see `docs/servers-devhud-release-controller-contract.md` for stable configuration names)

Public release guidance is maintained at the stable `/devhud/releases` route of the configured public documentation site, with `/devhud/install` and `/devhud/security` companion routes.

## Native Linux CLI repositories

`linux-packages.mjs` verifies an already-published release, creates nFPM packages and signed APT/DNF metadata, and resumes publication from private R2 state. Use Actions → Publish Linux CLI packages on `main`, with the exact project, version without `v`, and 40-character tag commit. The default `dry-run` performs disposable signing and the full installation matrix. Select `publish` only for the same supported release identity when recovering its package publication. Existing GitHub assets and tags are never replaced.

Build local tools with `docker build -f packaging/linux/Tools.Dockerfile -t delino-linux-package-tools .`. Generate disposable fixtures with `fixture.mjs` inside that image and use `smoke.mjs --fixture <output> --image <supported-image> --architecture amd64|arm64` to exercise package-manager install, update and remove. Production secrets are not needed. Full provisioning, key recovery, cache and Environment ownership are defined in `docs/repository-linux-packages-contract.md`.
