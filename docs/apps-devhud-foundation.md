# apps-devhud-foundation

## Scope

- Project/component: `devhud` / `app`
- Sole canonical implementation path: `apps/devhud`
- Status: active application; `apps/devhud` contains the bundled-asset package, internal empty tool registry, production desktop shell, sign-ready desktop bundle configuration, stable mobile empty-state UI, maintained native iOS and Android hosts, a private mobile widget-state plugin, and independently built WidgetKit/AppWidgetProvider source targets. It has no production tool, visible or distributed widget, scoped updater network implementation, published release, publisher automation, or public support implementation.
- The current implementation includes tray-resident desktop window lifecycle, transactional global shortcuts, explicit autostart, pointer-monitor HUD placement, preview DevTools, a typed local update action, cross-window theme reconciliation, surfaced startup integration failures, provider-owned persisted System/Light/Dark state, internal registry filtering, target-isolated desktop CEF and mobile system-webview runtimes, scoped native commands, Swift/Kotlin shared-data adapters, non-distributed native widget fixtures, generated native host sources, and deterministic local and host validation commands.

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

The native boundary exposes only scoped Tauri commands required for settings, application lifecycle, diagnostics, updates where supported, and the versioned future widget-state record. Diagnostics export is one record-free command with no destination argument: its native host owns the save picker and returns only `exported` or `cancelled`, while failures return a stable classification without a path or underlying exception. It must not expose a CLI, localhost API, public API, Connect RPC service, webhook, public route, custom URL scheme, universal link, app link, or deep link. Native errors are stable enum-backed classifications, including invalid or conflicting shortcuts, shortcut registration failure, unsupported display server, CEF initialization, corrupt state, updater unavailability or rate limiting, invalid signature, and installation failure.

### Desktop HUD and tray behavior

- Run as a single tray/menu-bar resident application without a persistent Dock or taskbar icon. A per-application process guard is acquired before desktop windows, tray actions, shortcuts, or autostart state are initialized.
- Closing the HUD or settings window hides it while the process remains resident. Only the tray `Quit` action terminates the app, except a fatal CEF initialization failure, which logs and exits immediately.
- Tray actions are `Open DevHud`, `Settings`, `Check for Updates`, `Open DevTools`, and `Quit`.
- Show a skippable first-run settings window that focuses the single shortcut-capture control when local settings are ready, then captures and validates a global shortcut. Tray access remains available until a shortcut is configured.
- Launch-at-login is disabled by default and has an explicit settings toggle. The native desktop integration verifies changes and reports a stable typed failure while preserving the previous setting; if the effective system state cannot be read, Settings keeps the saved value visible and reports that the native state is unknown.
- Store shortcuts as structured modifier and key values, never as an unchecked free-form string. A malformed, conflicting, permission-denied, or failed registration preserves the previous valid binding. If persistence and native rollback both fail after a replacement, report and adopt the effective native binding instead of presenting stale shortcut state.
- A failed persisted shortcut or launch-at-login restoration is surfaced in Settings with the effective native state, and completing first-run setup immediately clears the setup presentation for the retained settings webview.
- Theme changes persist before they are broadcast from the settings webview and are adopted by the retained HUD webview without restarting the application.
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
- Mobile widget configuration crosses the application boundary only through the private standard Tauri mobile plugin: Rust calls `run_mobile_plugin` for the exact Swift/Kotlin read, write-and-refresh, reset preflight, and reset-and-refresh operations. Reset preflight resolves the scoped App Group or DataStore adapter without reading or mutating its value. A separate private diagnostics plugin accepts only an already-sanitized bundle and fixed safe file name, opens the iOS or Android document creation picker, writes only to the selected destination, cleans temporary iOS staging, and returns a closed outcome. Neither plugin has a JavaScript package or permission, generic native store/filesystem method, arbitrary key/path argument, network transport, or public plugin API.

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
- `Reset DevHud` uses an accessible modal confirmation whose initial focus is the destructive action, whose focus stays trapped, whose `Escape` and explicit Cancel actions close without mutation, and whose close restores focus to the Reset trigger. Before any reset mutation, native code resolves and validates the two exact record paths, the exact managed-log directory, the desktop CEF profile target, and the mobile widget adapter. A failed precondition reports a stable value-free storage classification and leaves valid data intact.
- After confirmation, Reset disables the active global shortcut and launch-at-login integration, atomically stages and clears `devhud.settings.v1` and `devhud.widget-configuration.v1`, clears the scoped App Group/DataStore widget value, clears application browsing data, removes the exact retained CEF profile and only its transaction stages, clears rotating managed logs, and clears surfaced startup-integration diagnostics before reporting completion. A native integration or pre-removal persistence failure reports that the reset failed, attempts to restore the previous working integrations, and records any rollback failure with its effective state. A failed record rollback that leaves a stable record present reports a partially retained reset and reloads both providers from the effective records. Cleanup failure after both stable record paths are absent remains a failed reset, discloses that transaction stages or application browsing/profile data may remain, and keeps integrations disabled so runtime state matches the next launch. A native widget deletion failure after application records are removed reports partially retained state. All classifications and logs omit stored values and paths. Repeated reset is idempotent.
- Rotating logs are selected only by the exact `devhud-<timestamp>-<process>-<sequence>.jsonl` pattern in the resolved application log directory. Symlinked log and CEF targets are rejected before deletion, and an existing CEF profile target must be a directory. User-selected diagnostic exports are user-owned and remain untouched even if they use the suggested `DevHud-diagnostics.jsonl` file name.
- Desktop configures the explicit app-owned CEF root `<platform cache>/dev.deli.devhud/cef`, uses off-the-record request contexts, denies downloads, and disables browsing history, persistent cookies, application cache, databases, local/session storage, and sync. Reset validates that exact target before atomically staging it and never removes a cache base, home directory, or sibling application data.
- Do not implement settings import, settings export, migration from another application, account migration, or cloud synchronization.
- The mobile application and build-only native targets use matching typed adapters for the exact widget record. Tests inject isolated App Group suites or temporary DataStores, round-trip the shared fixture, preserve unrelated state on reset, reject corrupt and future-version records without overwrite, and propagate injected refresh failures.
- Diagnostic-session correlation IDs are generated as fresh UUID v7 values for every native process start. They correlate records only within that bounded local diagnostic session and are neither accounts nor persistent user identifiers.

## Security

- Render only bundled frontend assets. Enable the CEF sandbox in every signed desktop build.
- Block external navigation, popups, downloads, and remote frontend resources.
- Keep Tauri capabilities window-specific and least-privileged: the HUD and settings webviews use separate capability documents containing only the record and shell commands each surface invokes. DevTools in the technical preview does not relax these boundaries.
- Do not implement authentication, an account system, tenant data, billing, cloud storage, backend services, DeliDev integration, analytics, crash telemetry, remote logs, advertising, or user tracking.
- The sole network exception is desktop updater discovery and download: use the unauthenticated GitHub Releases API, filter releases to `delinoio/oss` tags beginning with `devhud@v`, ignore drafts, unrelated releases, invalid semantic versions, unsupported targets, and releases without a valid signed DevHud updater manifest, and download manifests/assets only from GitHub Releases. Never ship a GitHub token. The mobile runtime reports updates as unsupported and has no network permission or endpoint.
- Check for updates at startup and every 24 hours. Offline, unavailable, and rate-limited checks are non-fatal. Require user confirmation before install/restart; invalid updater signatures leave the installed version unchanged.
- The app has no CLI, backend, public API, plugin SDK, deep link, telemetry, account system, DeliDev integration, localhost service, webhook, route, or remote extension surface.

## Logging

- Use the typed Rust `tracing`-compatible local diagnostics facade for troubleshooting. The sink rotates individual files and prunes managed records at both the exact seven-day boundary and a total 20 MB cap on desktop and mobile.
- The complete record allowlist is application/build version, OS/architecture enums, exact pinned upstream Tauri and CEF versions where applicable, ephemeral diagnostic-session UUID v7, safe event ID, timestamp, optional duration, severity enum, and closed classification. Unknown fields, invalid metadata, non-v7 IDs, nested objects, and arbitrary strings fail closed.
- Stable event/classification enums cover desktop and mobile runtime, widget bridge, persistence, display, shortcut, launch-at-login, updater, signature, installation, and diagnostics-export outcomes. Underlying I/O, Tauri, CEF, platform, updater, signature, and installation exceptions are never serialized.
- Never log search text, clipboard contents, user files, arbitrary filesystem paths, shortcut keys, credentials, signing data, raw process environment values, invitation/account data, or tokens.
- CEF initialization failure produces exactly one safe fatal structured diagnostic, flushes it synchronously, and is followed by immediate process exit.
- Diagnostics export occurs only after the user activates `Export diagnostics` and completes the native save picker. Cancelling does not open or mutate a destination. Export re-parses every stored line through the strict record schema, omits invalid/adversarial records, never returns or logs the selected path, and never transmits remotely. Export files are created outside the managed-log namespace and remain user-owned, including when `Reset DevHud` clears managed local logs.
- Public English `PRIVACY.md` and `SUPPORT.md` files at stable GitHub paths, plus a DevHud GitHub issue template without default metadata, are required support material when the app is implemented. Their absence today is not an implementation claim.

## Build and Test

The package provides local `dev`, `build`, `typecheck`, `lint`, `test`, `test:a11y`, `check:diagnostics`, `test:diagnostics`, deterministic rebuild, contract/pin, lockfile, Rust, debug desktop, sign-ready preview bundle, and host-appropriate repeated desktop lifecycle smoke commands. `check:diagnostics` statically validates the allowlist, rotation constants/tests, exact runtime versions, native picker scope, capability exclusions, fatal/redaction coverage, and absence of remote transport; `test:diagnostics` adds the focused frontend and Rust suites. Mobile hosts add `mobile:generate:android`, `mobile:generate:ios`, `build:android`, `build:android:ci`, `build:ios`, `build:ios:ci`, `check:mobile`, `test:mobile`, and `test:android:native`. Native widget commands are `build:widget:android`, `build:widget:ios`, `test:widget:android`, `test:widget:ios`, and `check:widget-artifacts`. The artifact command checks canonical projects plus discovered release merged manifests and accepts explicit Android manifest/APK and iOS `.app` paths; it requires at least one built release artifact and fails if any receiver is registered or an extension/provisioning identity is embedded. Deterministic frontend output is declared in `apps/devhud/turbo.json`. `test:a11y` exercises component keyboard/focus and screen-reader semantics plus automated WCAG checks with `axe-core`; the full desktop OS/architecture/display matrix, signature validation, and release-validation tasks must be added with their corresponding implementations and must not be represented by passing placeholders.

Required validation coverage is:

- React type checking, linting, unit/component tests, Rsbuild output, keyboard/screen-reader behavior, and WCAG automation.
- Root Rust formatting, Clippy, and tests including the DevHud application and private mobile plugin crates.
- CEF desktop builds and smoke tests on macOS, Windows, and Ubuntu for x64 and ARM64, including X11 and XWayland Linux coverage.
- Clean iOS and Android Tauri generation/builds, runtime-feature separation, native architecture declarations, mobile navigation and empty/error/loading states, settings/theme persistence, Diagnostics entry, and accessibility.
- Static and built-artifact checks for absent deep-link handlers, associated domains, Android network permission, unintended endpoints, release widget target dependencies/embedding/registration, production tools/configuration, and visible sample widget state.
- Diagnostic rotation age/size limits, recursive fail-closed redaction with adversarial values, UUID v7 session freshness, single fatal CEF events, explicit export and cancellation, path non-disclosure, user-owned export preservation, no remote transport, least-privilege capabilities, and desktop/mobile native picker integration.
- Installer, signature, updater, SBOM, and provenance validation.
- Performance measurements must record HUD display latency, cold startup, package size, and idle memory per supported desktop platform, plus mobile startup time. Publish these measurements with the release; `0.1.0` defines no numeric pass threshold.

DevHud participates in the existing change-scoped Rust formatting, Clippy, and test jobs. The `node-devhud` CI job runs the frontend typecheck, lint, unit, accessibility, build, diagnostics contract, and portable mobile contract commands when DevHud inputs change. Change-scoped `devhud-widget-android` and `devhud-widget-ios` jobs compile and test the build-only foundations, compile both private Kotlin/Swift plugins into an x64 release application on the native host, and pass the built application artifact to the fail-closed release-absence guard. Android uses JDK 17 and API 36; iOS uses macOS, Xcode/XCTest, an available simulator, and XcodeGen. No dedicated DevHud release job exists.

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
