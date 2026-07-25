# apps-devhud-foundation

## Scope

- Project/component: `devhud` / `app`
- Sole canonical implementation path: `apps/devhud`
- Status: active foundation; `apps/devhud` contains the common bundled-asset application package, internal empty tool registry, desktop/mobile empty-state UI, maintained native iOS and Android hosts, a private mobile widget-state plugin, and independently built WidgetKit/AppWidgetProvider source targets. It has no production tool, visible or distributed widget, packaging, release, publisher, or public support implementation.
- The current implementation includes the shared bundled-asset frontend, provider-owned persisted System/Light/Dark state, internal registry filtering, target-isolated desktop CEF and mobile system-webview runtimes, scoped native commands, Swift/Kotlin shared-data adapters, non-distributed native widget fixtures, and deterministic local validation commands.

## Runtime and Language

- Frontend runtime: React with TypeScript, built by Rsbuild.
- Native runtime: Tauri Rust application under `src-tauri`.
- Desktop runtime: Tauri's upstream CEF runtime from the `feat/cef` line, pinned exactly to commit `f49ebda2fdba5755456b0f049e32593ca0ea331a` with `@tauri-apps/cli-cef` `3.0.0-alpha.6` in lockfiles. The implementation must not build from a moving branch.
- Mobile runtime: standard Tauri iOS WKWebView and Android System WebView. Mobile must not compile, link, embed, or launch the desktop CEF runtime.
- Desktop operating systems: macOS 14 or newer, Windows 11, and Ubuntu 24.04 LTS.
- Desktop architectures: separate x64 and ARM64 builds for each supported desktop operating system.
- Linux display support: X11 and Wayland through XWayland. Native Wayland is out of scope.
- Mobile operating systems and architectures: iOS 17.0 or newer on `arm64` production devices and `x86_64` CI simulators; Android 10/API 29 or newer on `arm64-v8a` and `armeabi-v7a` production devices and `x86_64` CI emulators.
- UX baseline: English-only, System/Light/Dark themes with System initially selected, Toss Design Guidelines, and WCAG 2.2 AA. The product uses the DevHud wordmark and minimal `DH` lettermark; a complete brand system is out of scope.

### CEF desktop runtime

DevHud uses Tauri's pinned CEF runtime directly for desktop builds. Desktop implementation and release validation covers the following behavior on macOS, Windows, and Ubuntu, for x64 and ARM64 where supported:

- CEF sandbox startup using only bundled frontend assets.
- Tauri IPC and capability enforcement.
- Tray/menu-bar lifecycle, global shortcuts, launch-at-login integration, theme handling, DevTools, explicit process shutdown, and clean helper-process cleanup.
- Signed or sign-ready DMG, NSIS, AppImage, and deb packaging.
- Tauri updater compatibility and signed updater bundles.
- No orphaned CEF processes after normal shutdown. Cleanup evidence must observe at least one CEF helper before shutdown and zero helpers afterward.
- Ubuntu 24.04 operation under both X11 and Wayland through XWayland.

DevHud must not fork Tauri, WRY, or `cef-rs`, and must not carry local source patches to the upstream runtime. Runtime, product, mobile, packaging, and release work may proceed against the exact pinned dependency while preserving these upstream-only dependency boundaries.

## Users and Operators

- Primary actor: an individual developer using local built-in tools.
- Secondary actors: DevHud maintainers and release operators.
- System actors: the desktop tray, global shortcut, launch-at-login and updater facilities; WKWebView; Android System WebView; GitHub Releases; TestFlight; and Google Play.
- Explicitly excluded actors: DeliDev users and organizations, remote mini-app publishers, external plugin authors, backend operators, and account administrators.

## Interfaces and Contracts

### Internal tool registry

The registry is an internal, closed contract, not a plugin interface. `ToolDefinition` must contain:

- a stable lowercase kebab-case `toolId`;
- English name, description, and search keywords;
- a supported-platform enum set;
- a required-capability enum set; and
- an internal UI entrypoint.

Tools may support a subset of platforms. Each shell exposes only tools supported by the current platform and granted capabilities. Capability values are closed and enum-backed; a new capability is introduced only with the tool that needs it. Production registration is empty in `0.1.0`; tests may use fixture definitions. No external plugin authors, remote tools, user-authored scripts, runtime code downloads, or plugin SDK are authorized.

The native boundary exposes only scoped Tauri commands required for settings, application lifecycle, diagnostics, updates where supported, and the versioned future widget-state record. It must not expose a CLI, localhost API, public API, Connect RPC service, webhook, public route, custom URL scheme, universal link, app link, or deep link. Native errors are stable enum-backed classifications, including invalid or conflicting shortcuts, shortcut registration failure, unsupported display server, CEF initialization, corrupt state, updater unavailability or rate limiting, invalid signature, and installation failure.

### Desktop HUD and tray behavior

- Run as a tray/menu-bar resident application without a persistent Dock or taskbar icon.
- Closing the HUD or settings window hides it while the process remains resident. Only the tray `Quit` action terminates the app, except a fatal CEF initialization failure, which logs and exits immediately.
- Tray actions are `Open DevHud`, `Settings`, `Check for Updates`, `Open DevTools`, and `Quit`.
- Show a skippable first-run settings window that captures and validates a global shortcut. Tray access remains available until a shortcut is configured.
- Launch-at-login is disabled by default. Its persisted setting is reserved for the native desktop integration; do not expose a settings toggle until that integration can apply changes and roll back failures.
- Store shortcuts as structured modifier and key values, never as an unchecked free-form string. A malformed, conflicting, permission-denied, or failed registration preserves the previous valid binding.
- Display the always-on-top HUD centered on the monitor containing the mouse pointer and focus the search input immediately.
- Repeating the global shortcut toggles the HUD. `Esc` or focus loss hides it immediately.
- The empty production registry displays the exact message `No tools are available in this foundation preview.` and a Settings action.
- CEF DevTools are enabled in development and in the signed `0.1.0` technical preview. DevTools must not widen navigation, download, IPC, or filesystem capabilities.

### Mobile screens and native host boundary

The app provides stable internal screens for `Home`, `Widgets`, `Settings`, and `Diagnostics`, with explicit empty states because no production tool or visible widget ships in `0.1.0`. The frontend may select an initial shell from the user agent, but must reconcile it to the authoritative native `system-webview` runtime result so iPadOS desktop-content mode remains mobile.

The checked-in Android Gradle project and canonical iOS XcodeGen application project use application identity `dev.deli.devhud`. Package-local generation commands invoke the independently pinned standard Tauri mobile CLI and then enforce this contract. Production commands build device architectures; `:ci` commands build the required x64 simulator or emulator target. iOS generation and builds require macOS, Xcode with the iOS 17 SDK, and XcodeGen.

The package also owns independent build-only projects:

- The iOS project compiles a WidgetKit extension source target with bundle identifier `dev.deli.devhud.widget`, typed configuration models, the future shared App Group `group.dev.deli.devhud`, a timeline refresh bridge, and XCTest fixture coverage. The distributed `devhud_iOS` project has no target dependency, copy-files phase, embedded `.appex`, extension provisioning identity, or WidgetKit source. Its application entitlement contains the App Group only so the scoped application-side adapter can prepare future shared state.
- The Android project compiles `DevHudWidgetProvider`, typed layout and provider-info resources, the shared DataStore adapter, refresh behavior, and JVM fixture tests in an independent library. The distributed Android application does not depend on that library, and its source, merged, packaged, and release manifests contain no receiver, `APPWIDGET_UPDATE` action, or `android.appwidget.provider` metadata.
- Mobile widget configuration crosses the application boundary only through the private standard Tauri mobile plugin: Rust calls `run_mobile_plugin` for the exact Swift/Kotlin read, write-and-refresh, and reset-and-refresh operations. There is no JavaScript plugin package, plugin permission, generic native store method, arbitrary key, or public plugin API.

Consequently no widget appears in either platform's widget gallery. The `Widgets` screen and production widget configuration remain empty. Do not register custom URL schemes, universal links, app links, associated domains, browsable activities, or public deep links.

### Stable application and storage identifiers

- Application ID: `dev.deli.devhud`.
- Build-only widget extension/provider ID: `dev.deli.devhud.widget`.
- Future iOS shared App Group: `group.dev.deli.devhud`.
- Versioned settings key: `devhud.settings.v1`, containing theme, launch-at-login, and optional structured shortcut settings.
- Widget configuration key: `devhud.widget-configuration.v1`, containing widget slot references and future stable `toolId` values.

These identifiers must not be renamed or reused for DeliDev or another project. Production tools and user-visible widgets remain empty in `0.1.0`.

## Storage

- `devhud.settings.v1` has schema version `1` and contains only a System/Light/Dark theme enum, reserved launch-at-login boolean, and optional structured shortcut. A shortcut contains one or more unique modifier enum values and a key enum; unchecked shortcut strings are invalid.
- `devhud.widget-configuration.v1` has the exact JSON shape `{"version":1,"configuration":{"slots":[{"slot":"primary","toolId":"stable-tool-id"}]}}`. `slot` is one of the unique `primary`, `secondary`, or `tertiary` enum values, and `toolId` is validated as stable lowercase kebab case. Production configuration is `slots: []`; `fixture-diagnostics` exists only in native/frontend tests and is never rendered as a sample tool.
- The frontend uses a dependency-injected two-record storage adapter on desktop CEF and standard Tauri mobile runtimes. Native IPC exposes only record-specific read/write commands, not filesystem paths, a default store, arbitrary keys, or generic value operations. On iOS, widget configuration uses App Group `UserDefaults`; on Android it uses the dedicated `devhud-widget-state.preferences_pb` DataStore and only the exact versioned preference key.
- Local writes are atomic and serialize each record so call order yields last-successful-write-wins behavior. A failed write leaves the previous valid record intact and restores the provider-owned state to that value.
- Each successful mobile widget write or reset requests a platform widget refresh. With no embedded/registered widget the live refresh reports zero affected Android widgets or reloads no installed iOS timeline. Refresh failures are logged as widget-bridge failures, but do not reject or corrupt the successfully validated stored record.
- iOS excludes both the application-local record directory and future App Group container from device backups. Android backup and extraction rules exclude all application/DataStore domains from cloud backup and device transfer so local state is not restored onto another device.
- Unsupported future schema versions are rejected without reading them into state or overwriting stored data. Corrupt, incompatible, or unreadable data surfaces actionable confirmed-reset guidance without raw stored data in UI, errors, or logs. An unreadable record remains write-blocked until it can be read successfully or the user completes Reset DevHud.
- Local state is retained until the user chooses `Reset DevHud` or uninstalls the app.
- `Reset DevHud` requires confirmation and clears settings, widget shared state, CEF runtime state, and logs. User-exported diagnostic files remain user-owned.
- If a reset operation only partially clears native state before failing, the frontend reloads the resulting records while continuing to report the reset failure.
- Persist only the minimum CEF profile data required for operation. Disable web downloads, browsing history, cookies, and application web storage; Reset clears all CEF state.
- Do not implement settings import, settings export, migration from another application, account migration, or cloud synchronization.
- The mobile application and build-only native targets use matching typed adapters for the exact widget record. Tests inject isolated App Group suites or temporary DataStores, round-trip the shared fixture, preserve unrelated state on reset, reject corrupt and future-version records without overwrite, and propagate injected refresh failures.
- Diagnostic-session correlation IDs are ephemeral UUID v7 values; they are not accounts or persistent user identifiers.

## Security

- Render only bundled frontend assets. Enable the CEF sandbox in every signed desktop build.
- Block external navigation, popups, downloads, and remote frontend resources.
- Keep Tauri capabilities window-specific and least-privileged. DevTools in the technical preview does not relax these boundaries.
- Do not implement authentication, an account system, tenant data, billing, cloud storage, backend services, DeliDev integration, analytics, crash telemetry, remote logs, advertising, or user tracking.
- The sole network exception is desktop updater discovery and download: use the unauthenticated GitHub Releases API, filter releases to `delinoio/oss` tags beginning with `devhud@v`, ignore drafts, unrelated releases, invalid semantic versions, unsupported targets, and releases without a valid signed DevHud updater manifest, and download manifests/assets only from GitHub Releases. Never ship a GitHub token. The mobile runtime reports updates as unsupported and has no network permission or endpoint.
- Check for updates at startup and every 24 hours. Offline, unavailable, and rate-limited checks are non-fatal. Require user confirmation before install/restart; invalid updater signatures leave the installed version unchanged.
- The app has no CLI, backend, public API, plugin SDK, deep link, telemetry, account system, DeliDev integration, localhost service, webhook, route, or remote extension surface.

## Logging

- Use structured local logs for troubleshooting. Retain at most seven days and 20 MB with rotation.
- Safe log fields may include application/build versions, OS/architecture, upstream Tauri/CEF versions, safe event IDs, timestamps, duration measurements, and enum error classifications.
- Never log search text, clipboard contents, user files, arbitrary filesystem paths, shortcut keys, credentials, signing data, raw process environment values, invitation/account data, or tokens.
- CEF initialization failure produces a fatal structured diagnostic followed by immediate process exit.
- Diagnostics export occurs only after explicit user action to a user-selected destination. Redaction tests must prevent excluded values from logs and release bundles.
- Public English `PRIVACY.md` and `SUPPORT.md` files at stable GitHub paths, plus a DevHud GitHub issue template without default metadata, are required support material when the app is implemented. Their absence today is not an implementation claim.

## Build and Test

The foundation provides package-local `dev`, `build`, `typecheck`, `lint`, `test`, `test:a11y`, deterministic rebuild, contract/pin, lockfile, Rust, debug desktop build, and host-appropriate desktop smoke commands. Mobile hosts add `mobile:generate:android`, `mobile:generate:ios`, `build:android`, `build:android:ci`, `build:ios`, `build:ios:ci`, `check:mobile`, `test:mobile`, and `test:android:native`. Native widget commands are `build:widget:android`, `build:widget:ios`, `test:widget:android`, `test:widget:ios`, and `check:widget-artifacts`. The artifact command checks canonical projects plus discovered release merged manifests and accepts explicit Android manifest/APK and iOS `.app` paths; it requires at least one built release artifact and fails if any receiver is registered or an extension/provisioning identity is embedded. Deterministic frontend output is declared in `apps/devhud/turbo.json`.

Required validation coverage is:

- React type checking, linting, unit/component tests, Rsbuild output, keyboard/screen-reader behavior, and WCAG automation.
- Root Rust formatting, Clippy, and tests including the DevHud application and private mobile plugin crates.
- CEF desktop builds and smoke tests on macOS, Windows, and Ubuntu for x64 and ARM64, including X11 and XWayland Linux coverage.
- Clean iOS and Android Tauri generation/builds, runtime-feature separation, native architecture declarations, mobile navigation and empty/error/loading states, settings/theme persistence, Diagnostics entry, and accessibility.
- Static and built-artifact checks for absent deep-link handlers, associated domains, Android network permission, unintended endpoints, release widget target dependencies/embedding/registration, production tools/configuration, and visible sample widget state.
- Installer, signature, updater, SBOM, and provenance validation.
- Performance measurements must record HUD display latency, cold startup, package size, and idle memory per supported desktop platform, plus mobile startup time. Publish these measurements with the release; `0.1.0` defines no numeric pass threshold.

DevHud participates in the existing change-scoped Rust formatting, Clippy, and test jobs. The `node-devhud` CI job runs the frontend typecheck, lint, unit, accessibility, build, and portable mobile contract commands when DevHud inputs change. Change-scoped `devhud-widget-android` and `devhud-widget-ios` jobs compile and test the build-only foundations, compile the private Kotlin/Swift plugin into an x64 release application on the native host, and pass the built application artifact to the fail-closed release-absence guard. Android uses JDK 17 and API 36; iOS uses macOS, Xcode/XCTest, an available simulator, and XcodeGen. No dedicated DevHud release job exists.

## Dependencies and Integrations

### Upstream and project boundaries

- Tauri, `tauri-build`, the private bridge's `tauri-plugin` build support, the directly selected desktop `tauri-runtime-cef` sandbox dependency, and the mobile-only `tauri-runtime-wry` dependency are pinned to commit `f49ebda2fdba5755456b0f049e32593ca0ea331a`; `@tauri-apps/cli-cef` is pinned to `3.0.0-alpha.6`, while `@tauri-apps/cli-mobile` aliases the standard Tauri CLI exactly at `2.11.4`. Do not maintain a Tauri, WRY, or `cef-rs` fork or local patch, and do not replace the revision with `feat/cef` or another moving branch.
- DevHud is a local-only app for individual developers. It must remain independent from DeliDev and must not consume DeliDev accounts, catalog, billing, APIs, routes, or contracts. It has no dependency on delibase, Logto, Connect RPC, or any DeliDev service.
- The only runtime network dependency is GitHub Releases for the updater exception defined in Security. No backend, API origin, remote configuration, or online operational service is allowed.

### Release, beta, signing, and publisher contract

- Trigger the target release from `devhud@v0.1.0`. Publish it as a regular GitHub Release, not a prerelease, after privately building and validating every platform.
- Publish separate x64 and ARM64 macOS DMGs; separate x64 and ARM64 Windows NSIS installers; separate x64 and ARM64 Ubuntu AppImage and deb packages; target-specific signed Tauri updater bundles and manifest; `SHA256SUMS`; platform signatures; SPDX SBOMs; and GitHub artifact provenance.
- Produce signed iOS and Android builds for TestFlight and Google Play beta channels, with minimal `DH` store assets in the corresponding listing material. Open the GitHub Release, TestFlight external group, and Google Play open-testing release in the same release window after private validation. If external beta review rejects the empty mobile foundation, use TestFlight internal testing and Google Play closed testing without adding a sample tool.
- Publishing requires every signing and publisher prerequisite; never publish an unsigned public release. Required protected secrets/variables are the Tauri updater signing key and password, macOS Developer ID certificate and password, Windows signing certificate and password, Apple team ID, App Store Connect issuer/key IDs and private key, iOS distribution certificate/password/provisioning profile, Android keystore/store password/key alias/key password, and Google Play service-account credentials.
- On a broken desktop release, withdraw it from update discovery, annotate the release, and direct users to manually reinstall the previous signed installer. Do not implement automatic downgrade. Mobile updates remain managed by normal TestFlight and Google Play beta channels.
- Track the pinned upstream `feat/cef` commit monthly and perform an urgent `0.1.x` update when a high-risk Chromium/CEF security fix affects DevHud.
- Required operator runbooks are release, signing, updater withdrawal, manual rollback, store submission, CEF pin update, diagnostics, and support. No dedicated DevHud website or documentation deployment is part of this contract.

## Change Triggers

- Update [project-devhud](project-devhud.md), this document, `docs/README.md`, `README.md`, root `AGENTS.md`, and `apps/AGENTS.md` for ownership, path, identifier, runtime, platform, UI, storage, security, diagnostics, support, or exclusion changes.
- Update package manifests, workspace membership, Turborepo configuration, CI workflows, release workflows, signing configuration, public `PRIVACY.md`/`SUPPORT.md`, issue templates, and runbooks in the same change when implementation or operations are introduced.
- Update the project and this contract before changing any tool registry entry, native command, screen ID, widget registration, deep-link behavior, network exception, account behavior, telemetry, DeliDev integration, or public API. Those surfaces are currently prohibited.
- Update the release and upstream-monitoring material when the CEF pin, updater selection, supported artifact, signing prerequisite, beta channel, rollback, or security response changes.

## References

- [Project devhud](project-devhud.md)
- [Project template](project-template.md)
- [Domain template](domain-template.md)
- [Repository defaults](repository-defaults.md)
- [Documentation catalog](README.md)
- [Issue #729](https://github.com/delinoio/oss/issues/729)
- [Tauri CEF branch](https://github.com/tauri-apps/tauri/tree/feat/cef)
- [Pinned Tauri commit](https://github.com/tauri-apps/tauri/commit/f49ebda2fdba5755456b0f049e32593ca0ea331a)
- [Tauri system tray](https://v2.tauri.app/learn/system-tray/)
- [Tauri global shortcut](https://v2.tauri.app/plugin/global-shortcut/)
- [Tauri mobile plugins](https://v2.tauri.app/develop/plugins/develop-mobile/)

## Out of Scope

- Any production developer tool, sample tool, or test tool visible to users; production registration remains empty.
- Any widget visible in the iOS or Android widget gallery, any sample tool rendered by a fixture, or any release dependency, embedding, provisioning identity, or manifest registration for the build-only WidgetKit/AppWidgetProvider targets.
- Live Activities, Control Center controls, watchOS, Wear OS, desktop widgets, and native Wayland.
- External plugins, remote mini-apps, user-authored scripts, runtime code downloads, and a plugin SDK.
- DeliDev catalog, accounts, organizations, billing, APIs, routes, contracts, or any other DeliDev integration.
- Authentication, backend services, account synchronization, cloud storage, tenant data, or an account system.
- CLI commands, localhost APIs, public APIs, Connect RPC, webhooks, public routes, custom URL schemes, universal links, app links, or deep links.
- Remote telemetry, crash reporting, analytics, advertising, user tracking, online dashboards, remote alerts, feature flags, and kill switches.
- Settings import/export, migration from an existing application, account migration, and cloud synchronization.
- macOS App Store, Microsoft Store, Linux package repositories, or a production App Store/Google Play release; beta channels are limited to TestFlight and Google Play as defined above.
- Automatic update downgrade.
- A dedicated DevHud website or documentation deployment, Korean or other localization, a complete brand identity, and a marketing system.
- Numeric performance SLOs or production operational services.
