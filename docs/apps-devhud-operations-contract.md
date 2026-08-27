# DevHud Maintainer Operations Contract

## Scope and invariants

This is the internal maintainer runbook for the implemented DevHud workflow. The repository contracts and checked-in scripts remain authoritative; this document names the operator actions, evidence, and stopping points. Never print secrets, tokens, signed URLs, private response bodies, local paths, capture bytes, prompts, or issue bodies.

The private candidate is never public-ready until the complete signed inventory passes validation. There is no automatic downgrade, partial GA, service-level objective, remote-alert service, or kill switch. A failed check stops the channel and requires an explicit maintainer decision.

## Release preparation and approval

1. Confirm `packaging/devhud/release-metadata.json`, package manifests, Tauri configuration, Cargo packages, and the Native Messaging host all contain the same stable version. The release identity is `devhud@v<MAJOR.MINOR.PATCH>`.
2. Run `.github/workflows/package-devhud-private.yml` manually in `plan-only` mode first. It is credential-free and must not produce a package. Run it in `signed-private` mode only from the selected caller revision and only after the `devhud-private-build` environment is approved.
3. Treat the private jobs as the exact evidence pipeline: `plan`, `preflight`, `desktop`, `extension`, `mobile`, `oci`, and `assemble`. The complete artifact is named `devhud-v<version>-private-signed-candidate-<sha>-<run_id>-<run_attempt>` and is retained briefly for recovery.
4. Run `.github/workflows/release-devhud.yml` with `dry-run` before `release`. Its jobs are `identity`, `private_candidate`, `candidate`, `preflight`, `submit_stores`, `review_gate`, `docs_candidate`, `registry`, `prepare_infrastructure`, `stores_public`, `github_release`, `updater_public`, `public_docs`, `verify_all`, `ga`, and `rollback_pre_store`.
5. Approve the protected environments only at their documented boundary: `devhud-private-build`, `devhud-publication`, `devhud-store-review-approved`, `devhud-store-publication`, and `devhud-ga`. Verify names and presence of configuration, never values.

The public sequence is: complete candidate and live preflight; submit Apple, Google Play, and Chrome review inputs; wait for all exact versions to be `approved-held`; prepare API/sweeper infrastructure with updater discovery closed; publish all stores; publish the regular GitHub Release; expose all ten updater manifests; publish and verify `/devhud`; independently verify every channel; then approve GA. Do not announce or mark GA early.

## Credential and signing categories

- `devhud-private-build`: updater Ed25519 material, macOS Developer ID, Apple API issuer/key ID/private key for notarization, Windows PFX, iOS distribution/profile material, Android upload keystore, and Chrome extension public key. The Apple API identity is dual-use and is also required by `devhud-publication` for store submission.
- `devhud-publication`: live API/controller, Logto, PostgreSQL/R2/Cloudflare, store access, registry, public-docs, Apple API, and Chrome extension identity inputs. It must not duplicate private package-signing keys.
- `devhud-store-review-approved`, `devhud-store-publication`, and `devhud-ga`: protected approval boundaries, not storage for new credentials.
- `GITHUB_TOKEN` is workflow-scoped. `DEVHUD_RELEASE_CONTROLLER_TOKEN` is short-lived OIDC-derived authorization and is never stored.

Keep platform signatures, updater Ed25519 signatures, and keyless Sigstore bundles as separate trust domains. An unsigned, incompletely signed, or secret-bearing result is rejected.

## Candidate validation

Validate the exact primary artifacts before retaining a candidate:

- Desktop exact files: `devhud-macos-x64.dmg`, `devhud-macos-x64-macos-app.tar.gz`, `devhud-macos-arm64.dmg`, `devhud-macos-arm64-macos-app.tar.gz`, `devhud-windows-x64-windows-msi.msi`, `devhud-windows-x64-windows-nsis.exe`, `devhud-windows-arm64-windows-msi.msi`, `devhud-windows-arm64-windows-nsis.exe`, `devhud-ubuntu-x64-linux-appimage.AppImage`, `devhud-ubuntu-x64-linux-deb.deb`, `devhud-ubuntu-arm64-linux-appimage.AppImage`, and `devhud-ubuntu-arm64-linux-deb.deb`; confirm pinned CEF helpers/sandbox, package-kind markers, lifecycle behavior, and Native Messaging registration/removal.
- Store: `devhud-ios-arm64-app-store.ipa` and `devhud-android-arm64-armv7-google-play.aab`; confirm iOS identities/profiles/entitlements and Android ABI, signer fingerprint, and final merged non-exported `DevHudWidgetProvider` receiver.
- Extension: `devhud-chrome-web-store.zip` and byte-equivalent `devhud-chrome-github-validation.zip`; confirm the fixed configured extension ID and exact concrete-origin mapping.
- Native Messaging identity: `io.delino.devhud.native_messaging`; confirm the host manifest, installer registration, user-scoped IPC, and configured extension ID remain consistent.
- Widget identities: iOS bundle `io.delino.devhud.widget` and App Group `group.io.delino.devhud`; confirm widget state, receiver evidence, and backup exclusion remain aligned with the mobile contracts.
- OCI: `devhud-api-linux-amd64-arm64.oci.tar` and `devhud-api-sweeper-linux-amd64-arm64.oci.tar`; confirm both architectures, non-root users, embedded migrations, identical administrator assets, and readiness tests. The private workflow never pushes these layouts.
- Supply chain: every artifact has a component-bearing SPDX SBOM, run-attempt-bound SLSA provenance, deterministic `SHA256SUMS`, validation evidence, and the corresponding keyless Sigstore bundle. Updater input contains exactly ten signed stable platform/package manifests and detached signatures.

Use `scripts/release/validate-devhud-private-build.mjs`, `validate-devhud-public-assets.mjs`, `validate-devhud-ios-signing.mjs`, `apps/devhud/scripts/validate-updater-release.mjs`, and the release test suite. Never replace a missing SBOM, provenance statement, signature, or validation record with a placeholder.

## Coordinated publication, delays, withdrawal, and rollback

Store providers are `apple`, `google-play`, and `chrome-web-store`. `submit_stores` submits or reconciles exact versions; `review_gate` waits in `devhud-store-review-approved` until each provider reports `approved-held` or `public`. Store review delay is expected: leave the gate pending and resume it after independently checking the exact version. Do not upload another package for a processing, processed, pending, approved-held, or public exact version.

If publication fails before any store is public, `rollback_pre_store` first queries every exact store, withdraws held Apple/Chrome submissions, requires protected Google Play removal, verifies withdrawal, then reconciles controller status. Automatic controller rollback is allowed only when the API and sweeper pair is known to be public and no store is public. A prepared pair is left untouched; a disagreement fails closed.

After the first store is public, automatic rollback is forbidden. Use a coordinated roll-forward or an explicitly approved emergency withdrawal, preserving the already-public immutable release and recording the operator decision. For updater withdrawal, use the operator-selected deployment/controller emergency procedure at the exact release boundary; if that procedure is unavailable, keep the existing served set stable while preparing the coordinated roll-forward. Never publish a partial target set or silently downgrade clients.

## API, database, R2, migrations, and recovery

The deployed services are `devhud-api` and `devhud-api-sweeper`. Connect services are `BootstrapService`, `SettingsService`, `UploadService`, `AccountService`, `AdminService`, and `DiagnosticsService`; non-Connect checks are `/healthz`, `/readyz`, `/metrics`, `/admin/`, and the fixed updater route.

Before promotion, verify the PostgreSQL connection, ordered embedded migration ledger, schema readiness, R2 staging/public buckets, exact CORS/rate-limit policy, public-asset authority, Logto, and trusted-proxy configuration. Run migrations only with `go run ./servers/devhud-api/cmd/devhud-api migrate` through the release controller; never edit the migration ledger manually. The sweeper owns staging expiry, upload-removal reconciliation, post-recovery account purge, and request/audit/crash retention batches.

Backups must cover PostgreSQL data and migration metadata plus R2 staging/public objects and object metadata. Restore into an isolated environment, run the exact migration/readiness checks, verify account/upload tombstones and removal markers, reconcile R2 objects with the sweeper, and exercise `/healthz`, `/readyz`, `/admin/`, Bootstrap, upload finalization, and updater discovery before considering recovery complete. Do not restore secrets into logs or a production bucket without an explicit ownership decision.

Image quarantine/removal uses the implemented administrator workflow: record the authenticated administrator, target upload metadata ID, reason, expected state, and evidence; do not expose image bytes or signed/public URLs. Use the existing `AdminService.QuarantineUpload` and `AdminService.DeleteUpload` contracts, including metadata-first removal, preserve leases and tombstones, and stop if ownership or state compare-and-set fails.

## Deletion, restore, purge, diagnostics, and support

Account deletion is recoverable for 30 days through ownership-checked `AccountService.RestoreAccount`; restore clears deletion state only and never clears an administrator block. After the window, the sweeper irreversibly pseudonymizes/purges server-side account data, replaces or invalidates public CDN copies, and retains only the contracted audit/tombstone projections. Device-local PATs and R2 credentials never enter the API: each client performs immediate secure-store cleanup when possible and eventual profile reconciliation for devices that were offline. Never report purge completion until the server-side database/R2 boundaries and applicable per-device secure-store cleanup boundaries are confirmed.

Diagnostics and crash reports are opt-in, user-previewed, bounded, redacted, and correlation-bound. Guests export only; blocked or deletion-pending users cannot submit. For a crash, capture the typed safe error code, build/platform/architecture, exact desktop CEF/Tauri revisions where applicable, correlation IDs, quota state, and server availability. Never request or attach DOM, screenshots, credentials, issue bodies, prompts, or local paths.

Administrator support triage uses `AdminService` metadata-only user, usage, upload, and audit views. Validate a non-blank reason before mutation, preserve expected-state conflicts, use the returned correlation ID, and redact all credentials/locators. High-severity cases: stop publication or updater exposure, preserve the exact candidate and evidence, assess whether any store/API/updater channel is public, quarantine affected images through the contract if needed, notify the accountable maintainer through the approved human channel, and choose rollback-before-store or roll-forward/emergency withdrawal-after-store. Do not invent a remote alert or kill-switch path.

## High-risk CEF vulnerability response

The monthly `.github/workflows/devhud-cef-security-review.yml` report compares the committed Tauri revision with upstream `feat/cef`; it is read-only and produces metadata only. A high-risk signal requires an immediate maintainer-owned change:

1. Pin a reviewed Tauri/CEF revision in `apps/devhud/cef-pins.json`, every matching `Cargo.lock` git source entry, the root `Cargo.toml`, `apps/devhud/src-tauri/Cargo.toml`, and `apps/devhud/scripts/verify-pins.mjs`. Update every runtime revision consumer—`apps/devhud/src/diagnostics.ts`, `packages/devhud-api-client/src/validation.ts`, and `servers/devhud-api/internal/rpc/diagnostics.go`—or mechanically derive its value from the pin. The CEF review workflow derives its comparison revision from `cef-pins.json`; do not add a separate hardcoded workflow revision. Record the old/new revision, reason, compatibility result, and review reference in this contract and the release contracts.
2. Run the CEF/Tauri compatibility matrix for macOS x64/arm64, Windows x64/arm64, Ubuntu x64/arm64, CEF helpers/sandbox, official plugin compatibility patch, updater markers, Native Messaging lifecycle, and mobile dependency exclusion. The review workflow must never perform this mutation.
3. Build a new complete signed private candidate and repeat SBOM, provenance, artifact, installer, widget, extension, OCI, and runtime validation. Do not reuse prior signed bytes for a changed pin.
4. Decide updater publication explicitly. Keep discovery closed until the regular GitHub Release and all stores satisfy the existing gates; then expose all ten signed manifests together. If the vulnerable revision is already public, use coordinated roll-forward or emergency withdrawal; never automatic downgrade.

Every pin update must be reviewable in the contract diff, `apps/devhud/cef-pins.json`, `Cargo.lock`, candidate provenance, compatibility evidence, and release record.
