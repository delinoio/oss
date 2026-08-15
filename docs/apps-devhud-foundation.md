# apps-devhud-foundation

## Scope

`apps/devhud` is the planned shared React/TypeScript UI and desktop/mobile shell for `devhud`. It owns first-party UI composition, guest/authenticated settings, RealQA, Deck, local persistence, native capability adapters, accessibility, and platform filtering. It does not exist yet.

## Runtime and Language

- Desktop: Tauri `tauri-runtime-cef` pinned to `4af26a3f7f8b692d62cca549bbacd93f5ce90b41`; CEF renders bundled resources only with restrictive CSP and no arbitrary navigation, popups, or downloads.
- Mobile: platform WKWebView/Android System WebView with native Swift/Kotlin widget implementations.
- Targets: macOS 13+, Windows 10 22H2+, Ubuntu 22.04 LTS on X11, iOS 16+, Android 10/API 29+; desktop x64 and arm64.
- Bundle ID: `io.delino.devhud`; deep-link scheme: `devhud`. Fixed frontend port: `46305`.
- English source and Korean/English user-facing UI; system language first, synchronized override later. Follow Toss Design Guidelines and WCAG 2.2 AA.

## Users and Operators

Guest users, authenticated individual users, maintainers, and platform release operators. RealQA is desktop-only; Deck is desktop/mobile.

## Interfaces and Contracts

- First-party mini-app IDs: `realqa`, `deck`. Action IDs include the five `realqa.capture.*` identifiers defined in the project index.
- First run exposes editable `https://devhud.api.delino.io`, Sign in, and Continue locally. Non-loopback endpoints require HTTPS; loopback HTTP is allowed; no TLS bypass.
- Logto uses system-browser Authorization Code with PKCE and `devhud` callback. Bootstrap advertises protocol/API version, Logto data, public client IDs, asset base URL, capabilities, and enforced limits; it is not a remote feature-flag mechanism.
- Local guest settings and authenticated synchronized settings use whole-snapshot import choice, schema version, monotonic revision, expected-revision writes, typed conflict, and explicit reapply. Offline authenticated settings are read-only.
- RealQA supports display/window/all-display/region/toolbar capture, editing, encrypted drafts, URL mappings, optional sanitized Chrome context, GitHub issue creation, official/BYO R2, and explicit Codex/Claude Code/OpenCode draft/direct modes. It excludes mobile capture, screen recording, full-page/console/network/cookie/storage capture, comments, assignees, milestones, projects, and updating existing issues.
- Deck calls GitHub directly, preserves raw `is:pr` queries, caches at most 100 results/Deck, permits 25 Decks/user, and offers 1/5/15/30-minute refresh intervals while a desktop or mobile client is active and able to poll. Widgets and local notifications use the operating system's best-effort background schedule; WidgetKit or the Android scheduler may delay refreshes and cannot honor an app-selected one- or five-minute interval while the app is suspended. Widgets show one Deck and three PRs, deep-link to `devhud://deck/<deck-id>`, and visibly show the last successful refresh when data is stale. Notifications must not imply that suspended-widget data is current.
- Keyboard hooks distinguish right Command/Control; default shortcuts are right modifier + `K`, then `1`–`5`. Only configured chords are processed; every binding is editable/disableable and conflicts are diagnosed.

## Storage

Persist local secrets and device state only in platform secure storage or local encrypted storage: tokens, PATs, R2 keys, agent paths/versions, drafts, clone caches, Deck result caches, widget state, Native Messaging pairing, and browser permissions. Auto-save unsubmitted captures as encrypted drafts for 30 days with a default 10 GiB quota; never evict before deadline and reject new saves recoverably when full. Delete drafts after successful issue creation, explicit deletion, or logout. Synchronized settings contain no secrets or device state. UUID v7 is the default for Decks, submissions, drafts, and other new persisted entities.

## Security

Logout deletes local credentials, cached settings, Deck data, clones, drafts, and extension pairing data. Account deletion blocks access immediately, allows 30-day recovery, then purges synchronized data and official image bytes. Public-image warnings are localized. Local-agent direct mode requires per-submission consent, isolated managed clones, ephemeral `GH_TOKEN`, a 15-minute limit, schema validation, and no automatic fallback.

## Logging

Use redacted structured diagnostics. Never log tokens, headers, DOM, screenshots, URL fragments, form values, full paths, agent environments, or issue bodies. Crash reports are off by default, previewed/redacted, authenticated, and retained 30 days.

## Build and Test

The future app contract must validate frontend tests/lint, platform capability filtering, capture/shortcut abstractions, deep links, accessibility, mobile/widget builds, and desktop builds for every supported OS/architecture. Fixed port `46305` must be preflighted and never remapped.

## Dependencies and Integrations

Consumes `protos/devhud/v1` through `packages/devhud-api-client`; talks to `servers/devhud-api` only for bootstrap, settings, uploads, account, diagnostics, and administration. GitHub and user-owned R2 are direct client integrations. Native capture, widgets, and Native Messaging are platform integrations.

## Change Triggers

Update `docs/project-devhud.md`, related domain contracts, `apps/AGENTS.md`, and root `AGENTS.md` for shell, UI, persistence, platform, shortcut, mini-app, or security changes.

## References

- [DevHud project index](project-devhud.md)
- [Chrome extension contract](apps-devhud-chrome-extension-contract.md)
- [API contract](servers-devhud-api-contract.md)
- [Repository defaults](repository-defaults.md)
