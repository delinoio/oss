# DevHud

DevHud is a local-only Tauri application with one shared English React frontend. Desktop builds retain the exact pinned upstream CEF runtime and sandbox. Mobile builds use only Tauri's standard WKWebView host on iOS and System WebView host on Android; CEF is not a mobile dependency or feature.

## Mobile target contract

- Application identity: `dev.deli.devhud`
- iOS: iOS 17.0 or newer; `arm64` production devices and `x86_64` CI simulators
- Android: Android 10/API 29 or newer; `arm64-v8a` and `armeabi-v7a` production devices and `x86_64` CI emulators
- iOS native project source: `src-tauri/gen/apple/project.yml`; `mobile:generate:ios` uses XcodeGen through the standard Tauri CLI on macOS
- Android native project: `src-tauri/gen/android`; `mobile:generate:android` is safe to rerun and reapplies the package's distribution restrictions

Package-local WidgetKit and Android `AppWidgetProvider` source targets compile and test independently. Production artifacts do not embed the WidgetKit extension, depend on the build-only Android provider module, register an app-widget receiver, or contain an associated domain, URL scheme, universal/app link, browsable activity, network permission, authentication surface, remote tool, telemetry, backend endpoint, or DeliDev integration. The production tool registry, production widget configuration, and visible widget state are empty.

The mobile shell provides stable Home, Widgets, Settings, and Diagnostics screens with loading, error, and empty states. Settings persists System/Light/Dark through the same `devhud.settings.v1` record used by desktop. Navigation uses semantic labels, focus management, safe-area-aware responsive layouts, and minimum touch targets.

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
pnpm check:mobile
pnpm check:widget-artifacts -- --android-apk path/to/release.apk
pnpm check:widget-artifacts -- --ios-app path/to/DevHud.app
pnpm test:mobile
pnpm test:android:native
pnpm test:widget:android
pnpm test:widget:ios
```

Android generation and builds require JDK 17, the Android SDK, NDK, and the Rust Android targets. iOS generation and builds require macOS, Xcode with the iOS 17 SDK, XcodeGen, and the corresponding Rust iOS targets. Production commands build the device architectures above; `:ci` commands build unsigned debug hosts only for the x64 simulator/emulator contract. `build:widget:*` compiles only the non-distributed native foundation. `test:widget:*` also exercises typed fixture round trips and refresh/error handling, compiles the private native plugin into an x64 release application, and passes that built artifact to the fail-closed release guard. `check:widget-artifacts` requires a built release manifest, APK, or `.app` and fails if the iOS application embeds an extension or the Android manifest contains any receiver.

General package validation is `pnpm typecheck`, `pnpm lint`, `pnpm test`, `pnpm test:a11y`, `pnpm build`, `pnpm test:build`, `pnpm check:contracts`, `pnpm check:locks`, and `pnpm check:rust`. Desktop build and smoke commands remain `pnpm build:desktop`, `pnpm build:preview`, and `pnpm smoke:desktop`.

## Native boundary

The frontend loads only bundled assets. Native IPC exposes runtime diagnostics, record-specific reads/writes for `devhud.settings.v1` and `devhud.widget-configuration.v1`, and the confirmed `Reset DevHud` operation that transactionally disables desktop shortcut/autostart integrations before clearing those records plus application browsing data. On mobile, only widget configuration/reset/refresh work crosses the private standard Tauri Swift/Kotlin plugin boundary. iOS uses the future `group.dev.deli.devhud` shared adapter and Android uses an isolated DataStore; both decode the exact `devhud.widget-configuration.v1` schema and reject corrupt or future-version state without overwriting it. Mobile hosts exclude records from iOS device backups and Android cloud backup/device transfer. No arbitrary filesystem path, generic key/value store, public plugin, default-store authority, public route, or remote communication is exposed. Updates are reported as unsupported on mobile until a signed platform update integration exists.

The production desktop shell is single-instance and tray/menu-bar resident, and it creates no persistent Dock/taskbar item. It provides close-to-tray HUD/settings windows, transactional structured shortcuts, opt-in autostart, pointer-monitor HUD placement, cross-window theme reconciliation, surfaced startup restoration failures, focus/toggle/hide behavior, technical-preview DevTools, split least-privilege window capabilities, and a typed update action that has no network implementation yet. The internal typed tool registry has an empty production registration. Local settings retain System/Light/Dark theme, launch-at-login preference, and an optional validated structured shortcut; widget configuration retains validated slot-to-stable-`toolId` references. Future schema versions are left untouched, while corrupt data provides reset guidance without exposing its contents.

No production tool, visible widget, updater network implementation, public release automation, public API, CLI, or deep link is included. See `docs/apps-devhud-foundation.md` for the complete contract.
