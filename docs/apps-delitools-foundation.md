# DeliTools Application Foundation

## Scope

`apps/delitools` owns the empty desktop and mobile application. The mobile release workflow owns signed build artifacts and test-channel uploads.

## Runtime and Language

React 19 and Rsbuild render an empty root. Rust uses the repository-pinned Tauri revision. Desktop uses CEF, the existing helper entry point, and macOS 13 or newer. Android uses Wry and API 29 or newer; iOS uses Wry and iOS 16 or newer. The mobile library exposes staticlib, cdylib and rlib targets.

## Users and Operators

Developers launch the shell. Maintainers run the protected mobile distribution workflow.

## Interfaces and Contracts

The title is `DeliTools`; the application identifier is `io.delino.delitools`. Desktop opens one decorated, resizable 1280 by 820 window and exits when the last window closes. Mobile opens one blank webview. There are no product controls, menus, commands, permissions, plugins, servers or API clients.

`pnpm dev` serves the frontend on fixed loopback port 46312 and fails on conflicts. `pnpm dev:desktop` starts the desktop host. `pnpm mobile:android:init` and `pnpm mobile:ios:init` generate native projects; `pnpm dev:android` and `pnpm dev:ios` launch mobile development. Mobile build commands are `pnpm build:android` and `pnpm build:ios`. Local Android release builds remain unsigned when no upload keystore is supplied; the release workflow still requires and validates signing credentials.

## Storage

There is no product data. Native build projects under `src-tauri/gen` and frontend `dist` are ignored generated output. Desktop uses an incognito webview. Do not inspect or migrate DeliDev data.

## Security

No application capabilities are granted. Desktop retains CEF's required sandbox on macOS/Linux and its upstream automatic-policy limitation on Windows. Mobile release secrets are supplied through the `delitools-mobile` GitHub environment, never committed or included in artifacts. Release dispatch is main-only. Store submission is opt-in and targets Google Play internal testing and TestFlight, not public production release.

The environment supplies Android secrets `DELITOOLS_ANDROID_KEYSTORE_B64`, `DELITOOLS_ANDROID_KEYSTORE_PASSWORD`, `DELITOOLS_ANDROID_KEY_ALIAS`, `DELITOOLS_ANDROID_KEY_PASSWORD`, and `DELITOOLS_GOOGLE_PLAY_SERVICE_ACCOUNT_JSON`. The protected `DELITOOLS_ANDROID_CERTIFICATE_SHA256` variable pins the SHA-256 fingerprint of the registered Android upload certificate; both build-only and publishing modes reject a different signer before retaining the candidate. iOS uses `APPLE_API_ISSUER`, `APPLE_API_KEY_ID`, `APPLE_API_PRIVATE_KEY_B64` and the `DELITOOLS_APPLE_TEAM_ID` variable. Maintainers must register the application in both stores, enroll Play App Signing, configure the upload certificate and give the service account access to this app. Initial Google Play setup and store agreements remain account-side prerequisites.

## Logging

Native hosts emit structured startup, window-ready, failure and shutdown events through tracing. CI records the source revision, build number, commands and outcomes without credential contents.

## Build and Test

Run package-local `pnpm test` and root `cargo test`. Frontend validation joins affected repository CI on Ubuntu; no macOS runner is added to PR, main-push or affected manual CI. The separate main-only release dispatch builds Android arm64/armv7 AABs on Ubuntu and iOS arm64 IPAs on macOS. Its shared build number must be an integer from 1 to 9999 and increase for each store upload. This keeps the single-component iOS `CFBundleVersion` within its four-digit limit. The checked-in application version is 0.1.0. Artifacts include license notices and a revision record. Retain the CI artifacts before either store upload so a provider failure leaves the signed candidate and its validation record available for recovery. Signed build-only mode does not upload to stores; `publish` enables test-channel submission. Always remove temporary signing material and generated `dist` after validation.

Frontend builds and workflow fixtures do not establish device or store acceptance. Verify native launch, blank content, desktop resizing/exit, mobile lifecycle, signed install and account-backed upload separately. Preserve failed, skipped and unperformed checks in CI or PR evidence.

## Dependencies and Integrations

Use the shared verified Tauri CLI wrapper. CEF notices remain attached to desktop packaging. Android requires Java 17, the Android SDK and NDK; iOS requires Xcode and an Apple Developer team. Store uploads use Google's Android Publisher API and Apple's altool.

## Change Triggers

Keep the project index, workspace registrations, owning instructions and workflow contract aligned with structural or procedure changes.

## References

- [Project index](project-delitools.md)
- [Repository defaults](repository-defaults.md)
- [Workflow contract](repository-workflow-contract.md)
- [Tauri Android signing](https://v2.tauri.app/distribute/sign/android/)
- [Tauri iOS signing](https://v2.tauri.app/distribute/sign/ios/)
