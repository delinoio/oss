# Project: devhud

## Goal

DevHud is a coordinated desktop and mobile developer utility. V1 contains the desktop-only RealQA capture and GitHub issue workflow and the desktop/mobile Deck pull-request monitor with native widgets. This document is a documentation-first contract; no DevHud runtime implementation exists yet.

Issue [#815](https://github.com/delinoio/oss/issues/815) is the current product contract. It supersedes closed historical DevHud issues #729, #755, and #757; those issues are historical context only and must not supply architecture or scope.

## Project ID

```ts
enum ProjectId {
  DevHud = "devhud",
}
```

## Domain Ownership Map

- `apps/devhud` — shared React/TypeScript UI and Tauri desktop/mobile shell (planned).
- `apps/devhud-chrome-extension` — Chrome Manifest V3 extension (planned).
- `apps/devhud-admin` — administrator SPA embedded at `/admin` in the API artifact (planned).
- `servers/devhud-api` — stateless Go API (planned).
- `protos/devhud/v1` — versioned Connect RPC schemas (planned).
- `packages/devhud-api-client` — generated TypeScript client and Connect Query bindings (planned).
- `crates/devhud-native-messaging-host` — Rust Native Messaging broker packaged with desktop installers (planned canonical Rust workspace path).

No planned path is a Cargo workspace member or an implemented runtime until its skeleton and contract exist.

## Domain Contract Documents

- [apps/devhud](apps-devhud-foundation.md)
- [Chrome extension](apps-devhud-chrome-extension-contract.md)
- [administrator](apps-devhud-admin-contract.md)
- [server](servers-devhud-api-contract.md)
- [protocol](protos-devhud-v1-contract.md)
- [TypeScript client](packages-devhud-api-client-contract.md)
- [Native Messaging host](crates-devhud-native-messaging-host-contract.md)

## Cross-Domain Invariants

- Fixed loopback development ports are frontend `46305`, administrator `46306`, and API `46307`; every launcher fails with an actionable conflict instead of remapping.
- Desktop targets are macOS 13+, Windows 10 22H2+, and Ubuntu 22.04 LTS on X11, each in x64 and arm64 builds. XWayland is best effort; native Wayland is excluded. Mobile targets are iOS 16+ and Android 10/API 29+.
- Desktop uses `tauri-runtime-cef` from the authoritative `https://github.com/tauri-apps/tauri` repository, pinned exactly to Tauri commit `4af26a3f7f8b692d62cca549bbacd93f5ce90b41`; mobile uses WKWebView/Android System WebView and native Swift/Kotlin widgets. The bundle identifier is `io.delino.devhud` and the deep-link scheme is `devhud`.
- Code, schemas, logs, and internal contracts are English. End-user UI, validation, widgets, extension UI, and errors support English and Korean, initially following the system language with a synchronized override.
- Stable first-party identifiers are enum-backed: mini-apps `realqa` and `deck`; capture actions `realqa.capture.display`, `realqa.capture.active-window`, `realqa.capture.all-displays`, `realqa.capture.selection`, and `realqa.capture.toolbar`.
- Business flows use Connect RPC. The API never brokers GitHub, polls Decks, receives GitHub webhooks, or stores PR results. GitHub calls remain client-side.
- Service-owned identifiers use UUID v7. Logto subjects/issuers and GitHub identifiers remain external IDs.
- Secrets remain in platform secure storage: Logto tokens, GitHub PATs, R2 secrets, and local-agent credentials never synchronize, persist in server rows, or enter logs. Synchronized settings are versioned snapshots with expected-revision writes and typed conflicts; no last-write-wins merge.
- RealQA is desktop-only. Deck runs on desktop and mobile. Browser context is optional, permission-scoped, sanitized, bounded DOM context; local agents are desktop-only and explicit; widgets use local Deck credentials.
- Official uploads use R2 signed direct uploads. The first `CreateUpload` without an `upload_group_id` creates a server-owned UUID v7 upload group bound to the authenticated user; subsequent `CreateUpload` and `FinalizeUpload` calls must use it, and the 10-image limit applies atomically per group before a GitHub issue exists. Limits are 50 MiB/object, 1 GiB rolling-24-hour, 20 GiB stored, 120 signed URLs/rolling-hour, and 300 public GETs/IP/minute. `FinalizeUpload` revalidates authentication, block status, ownership, exact staging-key and upload-group binding, size, checksum, allowed image content, PNG signature, safe raster dimensions, and all applicable upload quotas. It atomically rechecks or reserves the group image-count, rolling-byte, stored-byte, and signed-URL quota state so concurrent finalizations cannot bypass limits; quota failures return `ResourceExhausted`. It rejects replay and removes invalid staging objects. Staging expires after 24 hours. API logs and crash reports retain 30 days; pseudonymized security/audit events retain at most 180 days. Account deletion purges user data, official-upload/tombstone metadata, and public cached bytes after a 30-day recovery period; upload metadata is deleted or irreversibly pseudonymized, except for the explicit audit-retention boundary. Deletion and quarantine replace the origin object before purging or revalidating the public CDN URL, and are not effective while a cached original remains retrievable.
- Logto uses the exact callback URI `devhud://auth/callback`. Bootstrap exposes deployment-provided public client IDs under the stable platform keys `desktop`, `ios`, and `android`; clients must select the ID matching their platform and must not infer a client from an unordered list.
- Chrome Native Messaging uses host name `io.delino.devhud.native_messaging`. The Web Store extension ID is a fixed 32-character release-configured value shared by the extension, host manifest, and installer; the host accepts only the exact origin `chrome-extension://<DEVHUD_CHROME_EXTENSION_ID>/`.
- Deck refresh intervals of 1/5/15/30 minutes apply only while a client is active and able to poll. Mobile widgets and local notifications use OS-controlled best-effort refresh scheduling, show stale state with the last successful refresh, and never imply freshness when suspended execution has delayed polling.
- Account recovery is exposed as authenticated, ownership-checked, idempotent `AccountService.RestoreAccount` during the 30-day window; after the window it fails with `FailedPrecondition`.
- Official desktop updates use the fixed HTTPS manifest endpoint `https://devhud.api.delino.io/updates/{channel}/{platform}/{architecture}.json`; v1 exposes the `stable` channel and maps `darwin-x86_64`, `darwin-aarch64`, `windows-x86_64`, `windows-aarch64`, `linux-x86_64`, and `linux-aarch64` artifacts explicitly. Installers pin the `devhud-release-root-v1` public-key fingerprint, key rotation requires signed successor metadata chained to that root, unknown or invalid signatures are rejected, versions cannot roll back without signed rollback authorization, and a failed update preserves the last known-good installation.
- Administration requires the `devhud-admin` Logto role, mandatory mutation reasons, audit records, and no synchronized-settings-content display.
- CI must cover Go format/vet/unit/integration/migrations, Rust format/clippy/unit/native-host protocol tests, frontend lint/tests, schema compatibility and generated-client freshness, supported desktop/mobile/widget builds, extension packaging, installer/signature/updater-manifest checks, SBOM/provenance, and non-root OCI validation.
- CI and integration validation must cover upload-group binding, upload-finalization rejection and staging cleanup, concurrent per-group image quota enforcement, origin replacement before CDN purge/revalidation for user deletion/quarantine/account purge, stale widget presentation, recovery-window boundaries, and updater signature/key-rotation/rollback behavior.
- Release is one coordinated GA only after all desktop architectures, both mobile apps/widgets, extension, API/admin deployment, artifacts, updater, and documentation pass gates. Partial GA is prohibited.
- Required releases include signed macOS DMG, Windows MSI and NSIS, Linux AppImage and Debian package, signed updater manifests, App Store/Google Play packages, Chrome Web Store package plus reproducible ZIP, and a non-root signed/provenanced OCI API image. Deployment requires Logto, PostgreSQL, R2/public asset hosting, signing identities, store accounts, and release secrets.
- Product analytics, remote feature flags/kill switches, third-party plugin ABI/SDK, server-side GitHub brokerage, native Wayland, server-side Deck polling, webhooks, push infrastructure, and staged public beta are out of scope.

## Change Policy

Update this index, affected domain contracts, `docs/README.md`, and applicable root/domain `AGENTS.md` files together when ownership, identifiers, interfaces, platform support, persistence, security, release, or exclusions change. Do not add runtime code before the documentation-first contracts are updated.

## References

- [Issue #815](https://github.com/delinoio/oss/issues/815)
- [Repository defaults](repository-defaults.md)
- [Project index template](project-template.md)
- [Domain contract template](domain-template.md)
