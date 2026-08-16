# apps-devhud-foundation

## Scope

`apps/devhud` contains the implemented deterministic React/TypeScript frontend and Rust/Tauri CEF desktop-host foundation for `devhud`. The local shell implements composed Home, RealQA, Deck, Settings, Account, and Diagnostics surfaces; a closed first-party registry; local presentation preferences; and a keyboard-first command palette. Service-backed settings, authentication, capture, GitHub, deep links, mobile shells, and native widgets remain planned.

## Runtime and Language

- Desktop: Tauri `tauri-runtime-cef` from `https://github.com/tauri-apps/tauri`, pinned to `4af26a3f7f8b692d62cca549bbacd93f5ce90b41`; CEF renders bundled resources only with restrictive CSP and no arbitrary navigation, popups, or downloads. Production uses `connect-src 'none'` and has no remote frontend dependency. The fixed development server permits only its `ws://127.0.0.1:46305` HMR endpoint and Rsbuild's injected inline styles. A later authenticated implementation may build a per-session production `connect-src` only from the validated selected API origin, `https://api.github.com`, and validated HTTPS signed-upload origins; loopback HTTP is permitted only for documented development origins.
- Mobile: platform WKWebView/Android System WebView with native Swift/Kotlin widget implementations.
- Targets: macOS 13+, Windows 10 22H2+, Ubuntu 22.04 LTS on X11, iOS 16+, Android 10/API 29+; desktop x64 and arm64.
- Bundle ID: `io.delino.devhud`; deep-link scheme: `devhud`. Fixed frontend port: `46305`.
- English source and Korean/English user-facing UI. The shell selects English or Korean from the system with English fallback and persists a local explicit language override; System/Light/Dark is likewise a local explicit preference. Those preferences are versioned local non-secret state, not synchronized settings. The shell follows Toss Design Guidelines and includes WCAG 2.2 AA-oriented checks for named controls, deterministic focus, reduced motion, high-contrast tokens, and Escape closure for its command palette; formal accessibility qualification remains pending.

## Users and Operators

Guest users, authenticated individual users, maintainers, and platform release operators. RealQA is desktop-only; Deck is desktop/mobile.

## Implemented Desktop Host

- `apps/devhud/src-tauri` is a root Cargo workspace member. It uses only authoritative upstream Tauri git dependencies at revision `4af26a3f7f8b692d62cca549bbacd93f5ce90b41`; branch dependencies, `feat/cef`, forks, Cargo patches that alter the protected DevHUD dependency graph, and vendored upstream modifications are forbidden and mechanically checked. Root patches used only by unrelated workspace crates remain outside the DevHUD verifier boundary.
- `apps/devhud/cef-pins.json` records the resolved Tauri package versions (`tauri` 2.11.5, `tauri-build` 2.6.3, `tauri-cli` 2.11.4, and `tauri-runtime-cef` 0.1.0), CEF Rust crates `cef` and `cef-dll-sys` `150.0.0+150.0.10`, `download-cef` 2.3.2, their registry checksums, upstream source revisions, and every platform archive SHA-1. The verifier owns independent immutable copies of the CEF Rust and download-helper versions, checksums, revisions, and the six archive names and hashes. The runtime archive version is CEF `150.0.10+g8042e43+chromium-150.0.7871.101`.
- The runtime's default sandbox feature remains enabled. The executable uses Tauri's CEF entry point so renderer, GPU, plugin, and other helper processes cannot enter browser-process application initialization. Production startup preflights the platform-specific CEF library, ICU/resource packs, locale, sandbox/bootstrap files, and macOS helper applications relative to the installed executable; Linux package resources are resolved from `<prefix>/share/DevHUD` next to `<prefix>/bin`. Missing material produces a structured `cef_fatal_initialization` diagnostic and exit code 78 before the browser starts.
- Navigation is restricted to bundled `http://tauri.localhost` content in production and exact `http://127.0.0.1:46305` content in development. Popups and downloads are denied. The Rsbuild development launcher binds only `127.0.0.1:46305`, preflights collisions, and never chooses another port. The Tauri command wrapper rejects all `-c` and `--config` overrides, including attached short values, so callers cannot replace the pinned application configuration; it forwards `SIGINT` and `SIGTERM`, mirrors signal-based exits, and on every supported desktop platform terminates and awaits the complete Cargo, Tauri CLI, desktop-host, and Rsbuild process tree. Timed-out platform smokes use the same complete-tree termination on every supported desktop platform before temporary cache and log cleanup.
- Structured diagnostics cover platform/display selection, resource discovery, frontend readiness, renderer termination, fatal initialization, smoke shutdown, and clean host shutdown. After React mounts, it changes the document title to `DevHUD` and invokes the native `frontend_ready` command; the host atomically accepts that CEF-supported readiness signal before dispatching smoke behavior. An independent five-second wall-clock watchdog emits `frontend_readiness_timeout` and terminates with exit code 1 if readiness does not complete. macOS uses the typed CEF renderer-termination callback; every packaged Windows and Linux launch enables the CEF Inspector domain and registers the DevTools protocol crash listener, while the deliberate `Page.crash` request and its watchdog remain exclusive to the renderer-crash smoke. Desktop release hosts retain timestamped, redacted JSON diagnostics in UTC-daily `devhud.YYYY-MM-DD.jsonl` files under `%LOCALAPPDATA%\io.delino.devhud\logs` on Windows, `~/Library/Logs/io.delino.devhud` on macOS, and `${XDG_STATE_HOME:-~/.local/state}/io.delino.devhud/logs` on Linux, keeping at most seven daily files while preserving stderr output for managed launches and smoke capture; operator `RUST_LOG` filters may tune dependency verbosity but cannot suppress error-level DevHUD diagnostics. Debug builds and CEF subprocesses remain stderr-only, and a file-sink setup failure falls back to stderr without logging the resolved path.

The architecture definitions in `apps/devhud/platforms.json` are authoritative for the desktop foundation:

| Platform | Architectures | Product minimum | Native CI definition |
| --- | --- | --- | --- |
| macOS | x64, arm64 | 13.0 | `macos-15-intel`, `macos-15` |
| Windows | x64, arm64 | 10 22H2 | `windows-2022`, `windows-11-arm` |
| Ubuntu X11 | x64, arm64 | 22.04 LTS | `ubuntu-22.04`, `ubuntu-22.04-arm` |

XWayland is best effort. Native Wayland is unsupported and rejected before CEF initialization. CI hosts newer than the macOS and Windows product minimums prove current native build/smoke compatibility, not the exact minimum OS; release qualification on macOS 13 and Windows 10 22H2 remains required. Ubuntu smoke certification is intentionally limited to Ubuntu 22.04+ with an X11 `DISPLAY`.

## Interfaces and Contracts

- First-party mini-app IDs: `realqa`, `deck`. Action IDs include the five `realqa.capture.*` identifiers defined in the project index. The registry is closed and enum-backed, includes navigation/settings actions, filters actions by platform capability before rendering navigation or the command palette, and cannot load third-party mini-apps.
- The command palette opens with the localized visible right Command/Control shortcut after first-run onboarding, contains its keyboard focus while open, offers only capability-available actions, and returns focus to its trigger on Escape or completion. The Account surface focuses its sole API-origin input when opened. Capture and service actions remain visibly unavailable until their native/service integrations exist.
- Local shell links use the system browser only through a native closed allowlist: the validated configured authentication origin and the approved GitHub PAT and issue destinations. Documentation remains unavailable until a curated public DevHud destination exists. Arbitrary browser navigation is rejected.
- First run exposes editable `https://devhud.api.delino.io`, Sign in, and Continue locally. Non-loopback endpoints require HTTPS; loopback HTTP is allowed; no TLS bypass. A selected self-hosted API origin is validated before it is added to the session CSP, and signed-upload origins are validated from the server response.
- The Diagnostics surface states that redacted host diagnostics are recorded locally but are not yet viewable in the shell; it must not claim that the current packaged session has no records.
- Logto uses system-browser Authorization Code with PKCE and the exact native callback URI `devhud://auth/callback`. Bootstrap advertises protocol/API version, Logto data, public client IDs keyed as `desktop`, `ios`, `android`, and `admin`, the exact deployment-configured admin redirect URI, asset base URL, capabilities, and enforced limits; clients select the matching key and it is not a remote feature-flag mechanism. Browser/API requests use the documented exact development and pinned Tauri CORS origins and Connect preflight policy.
- Local guest settings and authenticated synchronized settings use whole-snapshot import choice, schema version, monotonic revision, expected-revision writes, typed conflict, and explicit reapply. Offline authenticated settings are read-only.
- RealQA supports display/window/all-display/region/toolbar capture, editing, encrypted drafts, URL mappings, optional sanitized Chrome context, GitHub issue creation, official/BYO R2, and explicit Codex/Claude Code/OpenCode draft/direct modes. The first `CreateUpload` creates a server-owned UUID v7 submission and upload group for one issue workflow; later groups and finalizations must carry that submission, with at most 10 finalized images across it. Upload checksums are 32 raw bytes and are encoded as standard Base64 only for the R2 checksum header. The server reserves the signed-URL issuance quota before returning each URL and finalization does not charge that reservation again. It excludes mobile capture, screen recording, full-page/console/network/cookie/storage capture, comments, assignees, milestones, projects, and updating existing issues.
- Deck calls GitHub directly, preserves raw `is:pr` queries, caches at most 100 results/Deck, permits 25 Decks/user, and offers 1/5/15/30-minute refresh intervals while a desktop or mobile client is active and able to poll. Widgets and local notifications use the operating system's best-effort background schedule; WidgetKit or the Android scheduler may delay refreshes and cannot honor an app-selected one- or five-minute interval while the app is suspended. The iOS widget target is `io.delino.devhud.widget`; it shares App Group `group.io.delino.devhud` and Keychain access group `$(AppIdentifierPrefix)io.delino.devhud.shared` with the main app, and both targets must provision those entitlements. Widgets show one Deck and three PRs, deep-link to `devhud://deck/<deck-id>`, and visibly show the last successful refresh when data is stale. Notifications must not imply that suspended-widget data is current.
- Keyboard hooks distinguish right Command/Control; default shortcuts are right modifier + `K`, then `1`–`5`. Only configured chords are processed; every binding is editable/disableable and conflicts are diagnosed.

## Storage

Persist local secrets and device state only in platform secure storage or local encrypted storage: tokens, PATs, R2 keys, agent paths/versions, drafts, clone caches, Deck result caches, widget state, Native Messaging pairing, and browser permissions. iOS widget state and credentials use the shared entitlements above so a background WidgetKit refresh can read the local Deck snapshot without requiring the main app process. Auto-save unsubmitted captures as encrypted drafts for 30 days with a default 10 GiB quota; never evict before deadline and reject new saves recoverably when full. Delete drafts after successful issue creation, explicit deletion, or logout. Synchronized settings contain no secrets or device state. UUID v7 is the default for Decks, submissions, drafts, and other new persisted entities.

## Security

Logout deletes local credentials, cached settings, Deck data, clones, drafts, and extension pairing data. Account deletion blocks access immediately, allows 30-day recovery, then purges synchronized data and official image bytes. Deletion/quarantine replaces the origin object before purging or revalidating the public CDN URL and is not effective while a cached original remains retrievable. Public-image warnings are localized. Local-agent direct mode requires per-submission consent, isolated managed clones, ephemeral `GH_TOKEN`, a 15-minute limit, schema validation, and no automatic fallback.

## Logging

Use redacted structured diagnostics. Never log tokens, headers, DOM, screenshots, URL fragments, form values, full paths, agent environments, or issue bodies. Crash reports are off by default, previewed/redacted, authenticated, and retained 30 days.

## Build and Test

Package-local commands are:

The package-level Turbo configuration disables caching for DevHud's native `build` task because its executable and installer outputs live under the root Cargo `target/` directory and must be regenerated rather than restored as frontend-only output.

- `pnpm --filter devhud dev` — launch the pinned Tauri CLI and strict-port frontend.
- `pnpm --filter devhud build` — produce a production desktop build with bundled frontend and CEF material.
- `pnpm --filter devhud test` — type-check, reject Tauri CLI configuration overrides, validate English/Korean locale selection and light/dark eyebrow contrast, and compare two clean frontend builds by path, mode, and SHA-256 while rejecting remote HTML, JavaScript, and CSS loads, including protocol-relative references and WebSocket, EventSource, XMLHttpRequest, Worker, SharedWorker, and Beacon connections.
- `pnpm --filter devhud verify:pins` — verify exact git revisions, registry checksums, archive hashes, frontend versions, production CSP directives, all-target Cargo features, exact CI target/runner pairs, and the source of every resolved dependency reachable from DevHUD so branch, fork, and patch dependencies cannot enter the protected graph.
- `pnpm --filter devhud smoke:platform` — validate helper/resource discovery, sandboxed browser startup, three independent startup/shutdown cycles, renderer-failure diagnostics, fatal missing-resource diagnostics, and every expected marker in an isolated persisted JSONL sink against a production artifact. Linux requires `--artifact` to reference an installed or root-prepared package layout whose CEF SUID sandbox is owned by `root:root` with mode `4755`; macOS and Windows retain their default production-artifact paths.

`.github/workflows/CI.yml` provides native x64 and arm64 build/smoke definitions for macOS, Windows, and Ubuntu 22.04 X11. Ubuntu jobs validate that the Debian archive records `chrome-sandbox` as `root:root` with mode `4755` before repairing ownership solely in the non-root smoke extraction. Credential-free macOS development and CI bundles use Tauri's ad hoc signing identity with hardened runtime disabled so CEF helpers can execute; release signing remains a separate release gate that must override that identity and supply the production hardened-runtime entitlements. Root Rust validation remains `cargo fmt --all --check`, `cargo clippy --workspace --all-targets --all-features -- -D warnings`, and `cargo test --workspace --all-targets`.

Current proven limitations are deliberate: the frontend is a static local shell with `connect-src 'none'`; product APIs, dynamic CSP, deep links, capture/shortcut abstractions, accessibility qualification, installers/signing, mobile/widget builds, and shared iOS entitlements are not implemented. The hosted Linux development environment may validate compilation but cannot claim the Ubuntu platform smoke unless it is Ubuntu 22.04+ under X11. Minimum-version and signed-release qualification remain release gates.

## Dependencies and Integrations

Consumes `protos/devhud/v1` through `packages/devhud-api-client`; talks to `servers/devhud-api` only for bootstrap, settings, uploads, account, diagnostics, and administration. GitHub and user-owned R2 are direct client integrations. Native capture, widgets, and Native Messaging are platform integrations.

## Change Triggers

Update `docs/project-devhud.md`, related domain contracts, `apps/AGENTS.md`, and root `AGENTS.md` for shell, UI, persistence, platform, shortcut, mini-app, or security changes.

## References

- [DevHud project index](project-devhud.md)
- [Chrome extension contract](apps-devhud-chrome-extension-contract.md)
- [API contract](servers-devhud-api-contract.md)
- [Repository defaults](repository-defaults.md)
