# DevHud

DevHud is a bundled-shell Tauri application with one shared English React frontend. Its signed-out shell and diagnostics remain local. Deck is a closed internal production tool on desktop, iOS, and Android, using only authenticated native Connect calls and exact GitHub PR browser handoff. RealQA is a closed internal production tool only on macOS, Windows, and Ubuntu; a prior-bound offline account may capture, edit, and retain encrypted local drafts until online reauthentication enables its closed protobuf transport and signed image upload. Deployment and catalog activation remain separate. Desktop builds retain the exact pinned upstream CEF runtime and sandbox. Mobile builds use only Tauri's standard WKWebView host on iOS and System WebView host on Android; CEF is not a mobile dependency or feature.

## Mobile target contract

- Application identity: `dev.deli.devhud`
- iOS: iOS 17.0 or newer; `arm64` production devices and `x86_64` CI simulators
- Android: Android 10/API 29 or newer; `arm64-v8a` and `armeabi-v7a` production devices and `x86_64` CI emulators
- iOS native project source: `src-tauri/gen/apple/project.yml`; `mobile:generate:ios` uses XcodeGen through the standard Tauri CLI on macOS
- Android native project: `src-tauri/gen/android`; `mobile:generate:android` is safe to rerun and reapplies the package's distribution restrictions

Package-local WidgetKit and Android `AppWidgetProvider` source targets compile and test independently. Production artifacts do not embed the WidgetKit extension, depend on the build-only Android provider module, register an app-widget receiver, URL scheme, arbitrary deep link, remote tool, telemetry, or remote UI. The only mobile link is the auto-verified exact `https://deli.dev/auth/devhud/callback` boundary. Native authentication and Deck's closed native transport are the only implemented mobile networking exceptions; RealQA remains desktop-only. Production widget configuration and visible widget state remain empty.

The mobile shell provides stable Home, Deck, Widgets, Settings, and Diagnostics screens with loading, error, and empty states. Diagnostics stay local, rotate at seven days/20 MB, and export only after the user activates the export control and selects a destination in the native picker. Settings persists System/Light/Dark through the same `devhud.settings.v1` record used by desktop. Navigation uses semantic labels, focus management, safe-area-aware responsive layouts, and minimum touch targets.

## Commands

Run these from `apps/devhud`:

```sh
pnpm dev
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
pnpm check:security
pnpm check:widget-artifacts -- --android-apk path/to/release.apk
pnpm check:widget-artifacts -- --ios-app path/to/DevHud.app
pnpm build:realqa:extension
pnpm test:realqa:extension
pnpm test:realqa:native
pnpm test:mobile
pnpm test:security
pnpm test:android:native
pnpm test:diagnostics
pnpm test:auth
pnpm test:widget:android
pnpm test:widget:ios
```

`pnpm dev` binds the frontend to loopback on fixed port `46300`, rejects explicit host and port CLI overrides, and exits if that port is already occupied instead of selecting another port.

Android generation and builds require JDK 17, the Android SDK, NDK, and the Rust Android targets. iOS generation and builds require macOS, Xcode with the iOS 17 SDK, XcodeGen, and the corresponding Rust iOS targets. Production commands build the device architectures above; `:ci` commands build unsigned debug hosts only for the x64 simulator/emulator contract. `build:widget:*` compiles only the non-distributed native foundation. `test:widget:*` also exercises typed fixture round trips and refresh/error handling, compiles the private native plugin into an x64 release application, and passes that built artifact to the fail-closed release guard. `check:widget-artifacts` requires a built release manifest, APK, or `.app` and fails if the exact verified mobile callback is missing or broadened, the iOS application embeds an extension, or the Android manifest contains any receiver.

Set the public `DEVHUD_LOGTO_ENDPOINT` and `DEVHUD_LOGTO_APP_ID` values when compiling a configured mobile host; Rust embeds them in the iOS/Android application. Desktop reads the same values from its launch environment. Neither value is a client secret.

Desktop RealQA GitHub setup additionally reads the public `DEVHUD_REALQA_GITHUB_APP_CLIENT_ID` and `DEVHUD_REALQA_GITHUB_APP_SLUG` values from its launch environment. The native handoff opens only the matching exact GitHub.com OAuth or App-installation target; missing or malformed values fail closed. These identifiers are not client secrets.

General package validation is `pnpm typecheck`, `pnpm lint`, `pnpm test`, `pnpm test:a11y`, `pnpm test:auth`, `pnpm build`, `pnpm test:build`, `pnpm check:contracts`, `pnpm check:security`, `pnpm test:security`, `pnpm check:diagnostics`, `pnpm test:diagnostics`, `pnpm check:locks`, and `pnpm check:rust`. The security task tests malicious navigation/resource decisions, popup/download guards, normal-view and DevTools IPC/filesystem denials, the exact capability manifests, CEF sandbox/network switches, mobile manifests/entitlements, recursive diagnostic redaction coverage, authentication endpoint/token redaction, and generated bundle references. Desktop build and smoke commands remain `pnpm build:desktop`, `pnpm build:preview`, and `pnpm smoke:desktop`.

`build:realqa:extension` creates an unpacked Chrome 150 MV3 fixture and its exact-origin Native Messaging manifest under ignored `build/realqa-extension`; it does not publish to the Chrome Web Store. The fixture uses extension ID `neiiglibncgobmehenjkhicabgfpggff` and an ignored build-tree native-host path on macOS. Release checking is intentionally fail-closed: `pnpm check:realqa:package` requires a different valid `DEVHUD_CHROME_EXTENSION_ID` that is also present in externally injected `DEVHUD_APPROVED_CHROME_EXTENSION_IDS`. macOS release packaging also requires the signed installer to inject the absolute `DEVHUD_NATIVE_HOST_PATH`; no Linux path is assumed for macOS. A release build must compile `devhud-native-host` with the same extension ID and install the generated platform manifest through the signed desktop installer.

## Native boundary

The frontend loads only bundled assets. Every webview uses the same fail-closed bundled-navigation, popup, download, resource-response, CSP, permissions, referrer, and cross-origin policy. Native IPC exposes runtime diagnostics, a path-free `export_diagnostics` action, record-specific reads/writes for `devhud.settings.v1` and `devhud.widget-configuration.v1`, and the accessible confirmed `Reset DevHud` operation. Reset traps focus, supports `Escape`/Cancel without mutation, restores trigger focus, and preflights exact application-owned record, rotating-log, CEF-profile, and mobile-widget targets before transactionally disabling desktop shortcut/autostart integrations and clearing retained state. The desktop CEF runtime uses the explicit `<platform cache>/dev.deli.devhud/cef` root with off-the-record contexts, deny-by-default host resolution, and disabled background/component network services; downloads, history, persistent cookies, application cache, databases, local/session storage, and sync are disabled. Desktop owns its diagnostics save dialog; mobile uses a private diagnostics Swift/Kotlin plugin that accepts only the strict sanitized bundle and fixed file name. Cancellation does not open a destination, selected paths never cross IPC or enter errors, exported files remain user-owned across reset, and no remote diagnostic transport exists. A separate private mobile widget bridge owns only widget configuration/reset preflight/reset/refresh. iOS uses the future `group.dev.deli.devhud` shared adapter and Android uses an isolated DataStore; both decode the exact `devhud.widget-configuration.v1` schema and reject corrupt or future-version state without overwriting it. Mobile hosts exclude retained records/logs from iOS device backups and Android cloud backup/device transfer. No arbitrary filesystem path, generic key/value store, public plugin, default-store authority, public route, or remote communication is exposed. Updates are reported as unsupported on mobile until a signed platform update integration exists.

The production desktop shell is single-instance and tray/menu-bar resident, and it creates no persistent Dock/taskbar item. It provides close-to-tray HUD/settings windows, transactional structured shortcuts, opt-in autostart, pointer-monitor HUD placement, cross-window theme reconciliation, surfaced startup restoration failures, focus/toggle/hide behavior, technical-preview DevTools, and a typed update action that has no network implementation yet. Capabilities are split into exact desktop HUD, desktop settings, RealQA composer, and mobile-main manifests: HUD DevTools cannot mutate records, export diagnostics, reset data, manage integrations, access widget writes, or invoke filesystem/network/process plugins; settings, RealQA, and mobile retain only the commands their surfaces implement. The internal typed tool registry registers authenticated Deck across desktop and mobile and RealQA only on supported desktop operating systems; signed-out rendering exposes no server data. Local settings retain System/Light/Dark theme, launch-at-login preference, and an optional validated structured shortcut; widget configuration retains validated slot-to-stable-`toolId` references. Future schema versions are left untouched, while corrupt data provides reset guidance without exposing its contents.

No production tool beyond closed internal Deck and RealQA, visible widget, updater network implementation, public release automation, public API, CLI, or arbitrary deep link is included. RealQA registration does not publish the Chrome extension or activate deployment, DNS, R2, GitHub App, billing catalog, or store rollout. See `docs/apps-devhud-foundation.md` for the complete contract.

The unpublished RealQA Chrome bridge is the only extension exception. It captures only the active visible viewport, rechecks the captured tab/window/exact URL after capture and after user-activated DOM selection, requests optional access only for that HTTP(S) origin, and removes the one-shot origin grant after the selection attempt succeeds, fails, is cancelled, or reaches its 60-second timeout. It retains the active capture in the service worker while Chrome closes the popup for page selection, preserves the selected target metadata in the composer, and offers a user-confirmed pointer-free primary-display fallback with no default URL on restricted pages. Its Rust `devhud-native-host` binary revalidates the exact caller origin, closed metadata schema, redaction, image/message limits, and local pairing. After a prior RealQA device binding, it hands captures over private local IPC directly to the exact `realqa-composer` window's bounded in-memory queue; the React composer keeps queued PNG data in base64 form until bounded native decoding instead of expanding it into a JavaScript number array, and capture pixels and page metadata are never persisted in native-host state. Logout and reset clear queued captures and all accepted composer sources; reset also clears pairing. Any independently retained Chrome-owned site permissions must be reviewed separately in `chrome://extensions`.
