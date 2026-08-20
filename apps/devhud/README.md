# DevHUD host foundation

This workspace contains one bilingual React/Rsbuild shell and target-isolated Rust/Tauri hosts. Desktop uses pinned CEF. iOS 16+ uses WKWebView, and Android 10/API 29+ uses Android System WebView; mobile artifacts never select CEF. `cef-pins.json` and `platforms.json` define the desktop contract, while `mobile-platforms.json` defines mobile identity, versions, architectures, and resolved dependency closures.

From the repository root:

```sh
pnpm --filter devhud dev
pnpm --filter devhud build
pnpm --filter devhud test
pnpm --filter devhud verify:pins
pnpm --filter devhud mobile:generate
pnpm --filter devhud verify:mobile
pnpm --filter devhud smoke:platform
```

Mobile generation uses the repository-pinned Tauri CLI, reapplies byte-checked host manifests and native Kotlin/Swift sources, injects and embeds the `io.delino.devhud.widget` WidgetKit extension, and regenerates Xcode after the Swift overlay. The Android plugin manifest merges the production `DevHudWidgetProvider` and one-Deck configuration activity into every Android artifact. Run generation on macOS for Apple projects or with Android SDK 36 and NDK 29 installed for Android projects. Package-local builds are `build:ios`, `build:ios:sim:arm64`, `build:ios:sim:x64`, `build:android`, and `build:android:emulator:x64`; each build includes its platform widget. Production mobile targets are iOS arm64 plus Android arm64/armv7; arm64 and x64 iOS simulators and the Android x64 emulator are covered separately. The x64 iOS command starts the pinned Tauri build in project-open mode to keep its CLI-owned options server alive, then builds the generated workspace explicitly against the x64 simulator SDK instead of Tauri's device-oriented archive path. Android builds preserve each target's requested APK/AAB outputs under the repository `target/devhud-mobile/android/<tauri-target>/` directory so aggregate builds cannot overwrite an earlier architecture.

Widgets are opt-in per Deck. The app shows the English/Korean private-title exposure warning before it copies the selected Deck configuration, bounded 100-result cache, and only that Deck's local PAT into the isolated widget boundary. WidgetKit and AppWidgetProvider request a refresh on the OS best-effort 30-minute schedule, show total plus Open/Draft/Merged/Closed counts and the first three GitHub-ordered PRs, mark data stale after 60 minutes, preserve last-success time across offline/rate/error failures, and open `devhud://deck/<deck-id>`. Restore without a widget secret shows setup required and never chooses another profile.

Development binds only `127.0.0.1:46305` and fails if the port is occupied. Desktop platform smoke requires a native production artifact and certifies only macOS 13+, Windows 10 22H2+, or Ubuntu 22.04+ X11 on the matching architecture. On Linux, pass `-- --artifact /usr/bin/devhud` for an installed package or an equivalent root-prepared layout; the CEF SUID sandbox must be owned by `root:root` with mode `4755`. XWayland is best effort; native Wayland is unsupported.

See [`docs/apps-devhud-foundation.md`](../../docs/apps-devhud-foundation.md) for the full contract and current limitations.
