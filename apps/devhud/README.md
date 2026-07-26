# DevHud

DevHud is a local-only Tauri application with one shared English React frontend. Desktop builds retain the exact pinned upstream CEF runtime and sandbox. Mobile builds use only Tauri's standard WKWebView host on iOS and System WebView host on Android; CEF is not a mobile dependency or feature.

## Mobile target contract

- Application identity: `dev.deli.devhud`
- iOS: iOS 17.0 or newer; `arm64` production devices and `x86_64` CI simulators
- Android: Android 10/API 29 or newer; `arm64-v8a` and `armeabi-v7a` production devices and `x86_64` CI emulators
- iOS native project source: `src-tauri/gen/apple/project.yml`; `mobile:generate:ios` uses XcodeGen through the standard Tauri CLI on macOS
- Android native project: `src-tauri/gen/android`; `mobile:generate:android` is safe to rerun and reapplies the package's distribution restrictions

Package-local WidgetKit and Android `AppWidgetProvider` source targets compile and test independently. Production artifacts do not embed the WidgetKit extension, depend on the build-only Android provider module, register an app-widget receiver, or contain an associated domain, URL scheme, universal/app link, browsable activity, network permission, authentication surface, remote tool, telemetry, backend endpoint, or DeliDev integration. The production tool registry, production widget configuration, and visible widget state are empty.

The mobile shell provides stable Home, Widgets, Settings, and Diagnostics screens with loading, error, and empty states. Diagnostics stay local, rotate at seven days/20 MB, and export only after the user activates the export control and selects a destination in the native picker. Settings persists System/Light/Dark through the same `devhud.settings.v1` record used by desktop. Navigation uses semantic labels, focus management, safe-area-aware responsive layouts, and minimum touch targets.

## Commands

Run these from `apps/devhud`:

```sh
pnpm mobile:generate:android
pnpm mobile:generate:ios
pnpm build:android
pnpm build:android:ci
pnpm build:ios
pnpm build:ios:ci
pnpm build:widget:android
pnpm build:widget:ios
pnpm check:diagnostics
pnpm check:mobile
pnpm check:widget-artifacts -- --android-apk path/to/release.apk
pnpm check:widget-artifacts -- --ios-app path/to/DevHud.app
pnpm test:mobile
pnpm test:android:native
pnpm test:diagnostics
pnpm test:widget:android
pnpm test:widget:ios
pnpm perf:desktop
pnpm perf:mobile -- android android-emulator
pnpm perf:mobile -- ios ios-simulator
pnpm perf:aggregate
pnpm test:performance
```

## Non-gating performance evidence

`perf:desktop` records cold and warm process-to-ready timing, native HUD
invocation-to-show timing, packaged artifact bytes, and post-ready resident
memory when a desktop artifact can execute on the current host. Package bytes
are still collected independently when runtime profiling is unavailable, and
only an artifact explicitly labeled for the current architecture is used.
`perf:mobile`
captures startup for one explicitly selected Android/iOS device or simulator
target. Each invocation writes a distinct machine-readable result to
`performance/results/`; `perf:aggregate` creates the CI/release artifact
`release-performance.json` and its Markdown release summary.

The stable schema is `performance/result.schema.json`. Results record only
platform, architecture, app version, the exact pinned Tauri/CEF revision,
measurement method, numeric samples, and availability/failure classification.
They intentionally exclude user content, shortcut values, paths, environment
values, device IDs, credentials, and raw diagnostics. Missing host tools,
display servers, artifacts, build provenance, or supported targets are `unavailable`; an attempted
launch/protocol error is `failed`. Mobile collection verifies the installed app's
version and desktop collection verifies the debug binary's profiler provenance
before timing it; if either cannot verify the requested build, it writes
unavailable evidence rather than label a stale build as current. If target
inspection fails, its architecture is recorded as `unknown`, never guessed.
Version 0.1.0 has no performance threshold:
these commands always produce evidence rather than gate a release.
Run `pnpm perf:package` before `pnpm perf:desktop` to build a release artifact
and write its matching provenance sidecar. Desktop profiling reports package
size only when that sidecar's application provenance and SHA-256 match the
selected artifact.

For a physical mobile target, set `DEVHUD_PERF_DEVICE` only for the command
invocation; it selects the connected target but is never written to the result.

Android generation and builds require JDK 17, the Android SDK, NDK, and the Rust Android targets. iOS generation and builds require macOS, Xcode with the iOS 17 SDK, XcodeGen, and the corresponding Rust iOS targets. Production commands build the device architectures above; `:ci` commands build unsigned debug hosts only for the x64 simulator/emulator contract. `build:widget:*` compiles only the non-distributed native foundation. `test:widget:*` also exercises typed fixture round trips and refresh/error handling, compiles the private native plugin into an x64 release application, and passes that built artifact to the fail-closed release guard. `check:widget-artifacts` requires a built release manifest, APK, or `.app` and fails if the iOS application embeds an extension or the Android manifest contains any receiver.

General package validation is `pnpm typecheck`, `pnpm lint`, `pnpm test`, `pnpm test:a11y`, `pnpm build`, `pnpm test:build`, `pnpm check:contracts`, `pnpm check:diagnostics`, `pnpm test:diagnostics`, `pnpm check:locks`, and `pnpm check:rust`. Desktop build and smoke commands remain `pnpm build:desktop`, `pnpm build:preview`, and `pnpm smoke:desktop`.

## Native boundary

The frontend loads only bundled assets. Native IPC exposes runtime diagnostics, a path-free `export_diagnostics` action, record-specific reads/writes for `devhud.settings.v1` and `devhud.widget-configuration.v1`, and the confirmed `Reset DevHud` operation that transactionally disables desktop shortcut/autostart integrations before clearing those records plus application browsing data and managed logs. Desktop owns its save dialog; mobile uses a private diagnostics Swift/Kotlin plugin that accepts only the strict sanitized bundle and fixed file name. Cancellation does not open a destination, selected paths never cross IPC or enter errors, exported files remain user-owned across reset, and no remote diagnostic transport exists. A separate private mobile widget bridge owns only widget configuration/reset/refresh. iOS uses the future `group.dev.deli.devhud` shared adapter and Android uses an isolated DataStore; both decode the exact `devhud.widget-configuration.v1` schema and reject corrupt or future-version state without overwriting it. Mobile hosts exclude retained records/logs from iOS device backups and Android cloud backup/device transfer. No arbitrary filesystem path, generic key/value store, public plugin, default-store authority, public route, or remote communication is exposed. Updates are reported as unsupported on mobile until a signed platform update integration exists.

The production desktop shell is single-instance and tray/menu-bar resident, and it creates no persistent Dock/taskbar item. It provides close-to-tray HUD/settings windows, transactional structured shortcuts, opt-in autostart, pointer-monitor HUD placement, cross-window theme reconciliation, surfaced startup restoration failures, focus/toggle/hide behavior, technical-preview DevTools, split least-privilege window capabilities, and a typed update action that has no network implementation yet. The internal typed tool registry has an empty production registration. Local settings retain System/Light/Dark theme, launch-at-login preference, and an optional validated structured shortcut; widget configuration retains validated slot-to-stable-`toolId` references. Future schema versions are left untouched, while corrupt data provides reset guidance without exposing its contents.

No production tool, visible widget, updater network implementation, public release automation, public API, CLI, or deep link is included. See `docs/apps-devhud-foundation.md` for the complete contract.
