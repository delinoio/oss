# apps-devhud-foundation

## Scope

- Project/component: `devhud` / `app`
- Sole canonical implementation path: `apps/devhud`
- Status: the isolated macOS CEF gate is implemented; the complete feasibility gate remains blocked on Windows/Linux. `apps/devhud` contains only the non-product probe package and no product, mobile/widget, production packaging, release, publisher, or public support implementation.
- This document covers the future deployable package. The current scaffold is limited to the shared bundled-asset frontend probe, `src-tauri` runtime-selection boundary, typed gate harness, deterministic validation commands, and private macOS gate automation.

## Runtime and Language

- Frontend runtime: React with TypeScript, built by Rsbuild.
- Native runtime: Tauri Rust application under `src-tauri`.
- Desktop runtime: Tauri's upstream CEF runtime from the `feat/cef` line, pinned exactly to commit `649d4e6b0fbfd0b60cb5f2ed8d83ceef648a6769` with `@tauri-apps/cli-cef` `3.0.0-alpha.6` in lockfiles. The implementation must not build from a moving branch.
- Mobile runtime: standard Tauri iOS WKWebView and Android WebView. Mobile must not use the desktop CEF runtime.
- Desktop operating systems: macOS 14 or newer, Windows 11, and Ubuntu 24.04 LTS.
- Desktop architectures: separate x64 and ARM64 builds for each supported desktop operating system.
- Linux display support: X11 and Wayland through XWayland. Native Wayland is out of scope.
- Mobile operating systems: iOS 17 or newer and Android 10/API 29 or newer. Production device architectures and x64 emulator targets required for CI must be documented by the eventual package manifest.
- UX baseline: English-only, System/Light/Dark themes with System initially selected, Toss Design Guidelines, and WCAG 2.2 AA. The product uses the DevHud wordmark and minimal `DH` lettermark; a complete brand system is out of scope.

### CEF feasibility stop condition

The CEF gate is a prerequisite, not an implementation claim. It has no calendar timebox. Before shared app, mobile foundation, or publishing work continues, the gate must prove all of the following on macOS, Windows, and Ubuntu, for x64 and ARM64 where supported:

- CEF sandbox startup using only bundled frontend assets.
- Tauri IPC and capability enforcement.
- Tray/menu-bar lifecycle, global shortcuts, launch-at-login integration, theme handling, DevTools, explicit process shutdown, and clean helper-process cleanup.
- Signed or sign-ready DMG, NSIS, AppImage, and deb packaging.
- Tauri updater compatibility and signed updater bundles.
- No orphaned CEF processes after normal shutdown. Cleanup evidence must observe at least one CEF helper before shutdown and zero helpers afterward.
- Ubuntu 24.04 operation under both X11 and Wayland through XWayland.

DevHud must not fork Tauri, WRY, or `cef-rs`, and must not carry local source patches to the upstream runtime. If any required behavior cannot be achieved without a fork or patch, or any gate condition fails, stop product-foundation and release work, document the blocker, and require a separate architecture decision.

### Isolated macOS gate

The package's `macos-gate` feature and `.github/workflows/devhud-macos-cef-gate.yml` implement only the mandatory macOS portion on native x64 and ARM64 macOS 14+ runners. Each job must:

- start sandboxed CEF from bundled assets and prove scoped allowed/denied Tauri IPC;
- exercise menu-bar residency, hidden persistent Dock behavior, close-to-hide, a structured global shortcut, disabled-by-default launch at login, System/Light/Dark handling, DevTools with navigation denial, and explicit shutdown;
- run three normal startup/shutdown cycles, make CEF initialization and renderer termination fatal without restart, and observe CEF helpers before shutdown and none afterward;
- build, mount, and architecture-check a separate DMG and target-specific signed Tauri updater bundle, accept its valid updater signature, and reject a mutated bundle;
- validate Developer ID signatures when both certificate inputs are available, otherwise validate ad hoc code signatures and sign-ready metadata; and
- reject any diagnostic or retained evidence containing the shortcut value, arbitrary filesystem paths, environment values, updater keys, certificate data, or passwords.

The gate creates an ephemeral updater key only within the runner and suppresses raw subprocess output. It uploads only the safe evidence JSON for a short retention period; it does not upload or publish DMGs, updater bundles, keys, or signing inputs. The exact upstream Tauri revision and `@tauri-apps/cli-cef` version remain unchanged, with no Cargo patches or local upstream source changes.

### Current gate blocker

The gate is blocked at the required upstream commit:

- Tauri's public `Builder::on_web_content_process_terminate` API is compiled only for macOS and iOS. It is unavailable to a Windows or Linux DevHud application.
- `tauri-runtime-cef` receives CEF's `on_render_process_terminated` callback internally, but its webview construction takes the termination handler only on macOS/iOS and explicitly assigns `None` on other targets.
- Therefore the required fatal renderer-termination diagnostic and immediate shutdown cannot be installed or proved on Windows or Ubuntu through the public pinned API. Fixing this at the pinned revision requires changing upstream Tauri/`tauri-runtime-cef` source, which this project forbids.

This is the cross-platform CEF stop condition. The macOS probe integrations and private validation packages are gate-only evidence, not product or release implementation. Product UI, mobile/widget work, production packaging/updater integration, signing, publishing, and release work remain blocked pending a separate architecture decision. Evidence: [public hook target guard](https://github.com/tauri-apps/tauri/blob/649d4e6b0fbfd0b60cb5f2ed8d83ceef648a6769/crates/tauri/src/app.rs#L1884-L1898) and [CEF handler discarded on Windows/Linux](https://github.com/tauri-apps/tauri/blob/649d4e6b0fbfd0b60cb5f2ed8d83ceef648a6769/crates/tauri-runtime-cef/src/webview.rs#L354-L360).

## Users and Operators

- Primary actor: an individual developer using local built-in tools.
- Secondary actors: DevHud maintainers and release operators.
- System actors: the desktop tray, global shortcut, launch-at-login and updater facilities; iOS and Android native widget frameworks; GitHub Releases; TestFlight; and Google Play.
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

The native boundary exposes only scoped Tauri/plugin commands required for settings, window lifecycle, diagnostics, updates, and native widget state. It must not expose a CLI, localhost API, public API, Connect RPC service, webhook, public route, custom URL scheme, universal link, app link, or deep link. Native errors are stable enum-backed classifications, including invalid or conflicting shortcuts, shortcut registration failure, unsupported display server, CEF initialization or termination, corrupt state, widget bridge failure, updater unavailability or rate limiting, invalid signature, and installation failure.

### Desktop HUD and tray behavior

- Run as a tray/menu-bar resident application without a persistent Dock or taskbar icon.
- Closing the HUD or settings window hides it while the process remains resident. Only the tray `Quit` action terminates the app, except a fatal CEF initialization or renderer termination failure, which logs and exits immediately without automatic renderer restart.
- Tray actions are `Open DevHud`, `Settings`, `Check for Updates`, `Open DevTools`, and `Quit`.
- Show a skippable first-run settings window that captures and validates a global shortcut. Tray access remains available until a shortcut is configured.
- Launch-at-login is disabled by default and has an explicit settings toggle.
- Store shortcuts as structured modifier and key values, never as an unchecked free-form string. A malformed, conflicting, permission-denied, or failed registration preserves the previous valid binding.
- Display the always-on-top HUD centered on the monitor containing the mouse pointer and focus the search input immediately.
- Repeating the global shortcut toggles the HUD. `Esc` or focus loss hides it immediately.
- The empty production registry displays the exact message `No tools are available in this foundation preview.` and a Settings action.
- CEF DevTools are enabled in development and in the signed `0.1.0` technical preview. DevTools must not widen navigation, download, IPC, or filesystem capabilities.

### Mobile screens and widget boundary

The app provides stable internal screens for `Home`, `Widgets`, `Settings`, and `Diagnostics`, with explicit empty states because no production tool or visible widget ships in `0.1.0`.

The implementation must compile and test an iOS WidgetKit extension, an Android `AppWidgetProvider`, a shared data adapter, and a refresh bridge using fixtures. The WidgetKit extension must not be embedded in the distributed iOS app, and the `AppWidgetProvider` must not be registered in the Android manifest. Therefore no widget appears in either platform's widget gallery for this issue. Widget configuration and refresh are test fixtures only; no sample product tool is exposed.

Use iOS App Group `group.dev.deli.devhud` for the future app/widget shared container, Android DataStore for Android widget state, and build-only widget identifier `dev.deli.devhud.widget`. Do not register custom URL schemes, universal links, app links, or public deep links.

### Stable application and storage identifiers

- Application ID: `dev.deli.devhud`.
- Versioned Tauri Store key: `devhud.settings.v1`, containing theme, launch-at-login, and optional structured shortcut settings.
- Widget configuration key: `devhud.widget-configuration.v1`, containing widget slot references and future stable `toolId` values.
- iOS App Group: `group.dev.deli.devhud`.
- Build-only widget identifier: `dev.deli.devhud.widget`.

These identifiers must not be renamed or reused for DeliDev or another project. Production tools and user-visible widgets remain empty in `0.1.0`.

## Storage

- Local writes are atomic and use last-successful-write-wins behavior.
- Unsupported future schema versions are rejected without overwriting the stored data. Corrupt or incompatible data surfaces reset guidance.
- Local state is retained until the user chooses `Reset DevHud` or uninstalls the app.
- `Reset DevHud` requires confirmation and clears settings, widget shared state, CEF runtime state, and logs. User-exported diagnostic files remain user-owned.
- Persist only the minimum CEF profile data required for operation. Disable web downloads, browsing history, cookies, and application web storage; Reset clears all CEF state.
- Do not implement settings import, settings export, migration from another application, account migration, or cloud synchronization.
- Android widget state uses Android DataStore; the iOS shared-container boundary uses `group.dev.deli.devhud`. Both are local-only and fixture-tested in the foundation phase.
- Diagnostic-session correlation IDs are ephemeral UUID v7 values; they are not accounts or persistent user identifiers.

## Security

- Render only bundled frontend assets. Enable the CEF sandbox in every signed desktop build.
- Block external navigation, popups, downloads, and remote frontend resources.
- Keep Tauri capabilities window-specific and least-privileged. DevTools in the technical preview does not relax these boundaries.
- Do not implement authentication, an account system, tenant data, billing, cloud storage, backend services, DeliDev integration, analytics, crash telemetry, remote logs, advertising, or user tracking.
- The sole network exception is desktop updater discovery and download: use the unauthenticated GitHub Releases API, filter releases to `delinoio/oss` tags beginning with `devhud@v`, ignore drafts, unrelated releases, invalid semantic versions, unsupported targets, and releases without a valid signed DevHud updater manifest, and download manifests/assets only from GitHub Releases. Never ship a GitHub token.
- Check for updates at startup and every 24 hours. Offline, unavailable, and rate-limited checks are non-fatal. Require user confirmation before install/restart; invalid updater signatures leave the installed version unchanged.
- The app has no CLI, backend, public API, plugin SDK, deep link, telemetry, account system, DeliDev integration, localhost service, webhook, route, or remote extension surface.

## Logging

- Use structured local logs for troubleshooting. Retain at most seven days and 20 MB with rotation.
- Safe log fields may include application/build versions, OS/architecture, upstream Tauri/CEF versions, safe event IDs, timestamps, duration measurements, and enum error classifications.
- Never log search text, clipboard contents, user files, arbitrary filesystem paths, shortcut keys, credentials, signing data, raw process environment values, invitation/account data, or tokens.
- CEF initialization failure and renderer termination are fatal structured diagnostics followed by immediate process exit; do not enter an automatic restart loop.
- Diagnostics export occurs only after explicit user action to a user-selected destination. Redaction tests must prevent excluded values from logs and release bundles.
- Public English `PRIVACY.md` and `SUPPORT.md` files at stable GitHub paths, plus a DevHud GitHub issue template without default metadata, are required support material when the app is implemented. Their absence today is not an implementation claim.

## Build and Test

The scaffold provides package-local `build`, `typecheck`, `lint`, `test`, `test:probe`, `test:macos-gate-contract`, deterministic rebuild, contract/pin, lockfile, Rust, debug desktop build, host-appropriate desktop smoke, and native `gate:macos` commands. Its deterministic frontend output is declared in `apps/devhud/turbo.json`. The future product tasks for development, accessibility, the complete desktop matrix, mobile build, widget build, production packaging, and release validation remain blocked and are not stubbed as passing commands.

Required validation coverage is:

- React type checking, linting, unit/component tests, Rsbuild output, keyboard/screen-reader behavior, and WCAG automation.
- Root Rust formatting, Clippy, and tests including the DevHud Tauri crate once it is added to the workspace.
- CEF desktop builds and smoke tests on macOS, Windows, and Ubuntu for x64 and ARM64, including X11 and XWayland Linux coverage.
- iOS and Android Tauri builds; compile-only WidgetKit and `AppWidgetProvider` targets; and shared-state fixture tests.
- Installer, signature, updater, SBOM, and provenance validation.
- Performance measurements must record HUD display latency, cold startup, package size, and idle memory per supported desktop platform, plus mobile startup time. Publish these measurements with the release; `0.1.0` defines no numeric pass threshold.

The isolated DevHud macOS CEF workflow is the only DevHud-specific CI job. It is a feasibility gate and artifact validator, not a release job. A future architecture decision must explicitly unblock the remaining platform and product tasks without weakening existing repository checks.

## Dependencies and Integrations

### Upstream and project boundaries

- Tauri, `tauri-build`, and the directly selected desktop `tauri-runtime-cef` sandbox dependency are pinned to commit `649d4e6b0fbfd0b60cb5f2ed8d83ceef648a6769`; `@tauri-apps/cli-cef` is pinned to `3.0.0-alpha.6`. Do not maintain a Tauri, WRY, or `cef-rs` fork or local patch, and do not replace the revision with `feat/cef` or another moving branch.
- The macOS gate uses exact optional macOS-target dependencies `global-hotkey` `0.8.0` and `auto-launch` `0.5.0` directly behind `macos-gate`. They are probe-only native integrations and do not authorize a product plugin surface.
- DevHud is a local-only app for individual developers. It must remain independent from DeliDev and must not consume DeliDev accounts, catalog, billing, APIs, routes, or contracts. It has no dependency on delibase, Logto, Connect RPC, or any DeliDev service.
- The only runtime network dependency is GitHub Releases for the updater exception defined in Security. No backend, API origin, remote configuration, or online operational service is allowed.

### Release, beta, signing, and publisher contract

- Trigger the target release from `devhud@v0.1.0`. Publish it as a regular GitHub Release, not a prerelease, after privately building and validating every platform.
- Publish separate x64 and ARM64 macOS DMGs; separate x64 and ARM64 Windows NSIS installers; separate x64 and ARM64 Ubuntu AppImage and deb packages; target-specific signed Tauri updater bundles and manifest; `SHA256SUMS`; platform signatures; SPDX SBOMs; and GitHub artifact provenance.
- Produce signed iOS and Android builds for TestFlight and Google Play beta channels, with minimal `DH` store assets in the corresponding listing material. Open the GitHub Release, TestFlight external group, and Google Play open-testing release in the same release window after private validation. If external beta review rejects the empty mobile foundation, use TestFlight internal testing and Google Play closed testing without adding a sample tool.
- Publication is blocked when any signing or publisher prerequisite is absent; never publish an unsigned public release. Required protected secrets/variables are the Tauri updater signing key and password, macOS Developer ID certificate and password, Windows signing certificate and password, Apple team ID, App Store Connect issuer/key IDs and private key, iOS distribution certificate/password/provisioning profile, Android keystore/store password/key alias/key password, and Google Play service-account credentials.
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
- [Pinned Tauri commit](https://github.com/tauri-apps/tauri/commit/649d4e6b0fbfd0b60cb5f2ed8d83ceef648a6769)
- [Tauri system tray](https://v2.tauri.app/learn/system-tray/)
- [Tauri global shortcut](https://v2.tauri.app/plugin/global-shortcut/)
- [Tauri mobile plugins](https://v2.tauri.app/develop/plugins/develop-mobile/)
- [Apple WidgetKit](https://developer.apple.com/documentation/widgetkit/creating-a-widget-extension/)
- [Android App Widgets](https://developer.android.com/develop/ui/views/appwidgets)

## Out of Scope

- Any production developer tool, sample tool, or test tool visible to users; production registration remains empty.
- Any widget visible in the iOS or Android widget gallery; WidgetKit and `AppWidgetProvider` sources are compile-tested but not embedded or manifest-registered.
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
