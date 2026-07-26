# Project: devhud

## Goal

Define the application contract for DevHud, a local-only developer-tool shell for individual developers. The project is intentionally independent from the DeliDev web platform.

`apps/devhud` contains one active pnpm package: a React/TypeScript/Rsbuild bundled-asset application, a target-selecting Tauri Rust application crate, private Rust/native mobile widget and diagnostics-export bridges, maintained iOS and Android host projects, and build-only WidgetKit/AppWidgetProvider projects. Desktop builds use the exact pinned upstream CEF runtime directly and implement the production tray, window, shortcut, autostart, empty HUD, settings, DevTools, diagnostics export, and sign-ready preview bundle shell. Mobile builds use only standard Tauri WKWebView and Android System WebView and expose stable Home, Widgets, Settings, and Diagnostics screens. The shared UI includes provider-owned local settings/widget state and a closed internal tool registry. Native widget sources compile and test but are neither embedded nor registered in release apps. The application does not create a production tool, visible native widget, updater network implementation, published release workflow, or support/publisher artifact.

## Project ID

`devhud`

## Domain Ownership Map

- `apps/devhud` (`app`): the sole canonical implementation path. It owns the one-package React/TypeScript/Rsbuild plus Tauri application foundation and all future product, mobile/widget, and release sources.

No other repository path may implement DevHud. `servers/`, `protos/`, `crates/`, `cmds/`, `packages/`, and `apps/delidev-app` are not DevHud ownership paths.

## Domain Contract Documents

- [apps-devhud-foundation](apps-devhud-foundation.md)

## Cross-Domain Invariants

- DevHud's stable project identifier is `devhud`; its sole canonical path is `apps/devhud`.
- The application is one pnpm package: a React and TypeScript frontend built with Rsbuild, a Tauri Rust application crate, and private mobile plugin crates under `src-tauri`. Root package scripts remain delegators; deterministic validation tasks are package-local.
- Desktop uses Tauri's pinned upstream CEF runtime. Mobile uses standard Tauri iOS WKWebView and Android System WebView runtimes. The target-specific Cargo features and independently pinned CLIs prevent either runtime model from leaking into the other.
- Tauri, `tauri-build`, `tauri-runtime-cef`, `tauri-runtime-wry`, `@tauri-apps/cli-cef`, and the standard aliased mobile CLI use the exact versions defined by the app foundation contract. DevHud must not carry a Tauri, WRY, or `cef-rs` fork or local patch and must not follow a moving upstream branch.
- Desktop validation covers the CEF sandbox, bundled assets, deny-by-default host resolution, guarded navigation/resources/popups/downloads, scoped IPC shared unchanged by DevTools, lifecycle behavior, package formats, updater artifacts, helper cleanup, supported architectures, and the Linux display matrix.
- The minimal visual asset foundation is source-controlled at `apps/devhud/assets/source/devhud-lettermark.svg`; generated desktop, installer, tray, iOS, Android, launch, and English store assets are reproducible with the package asset command and guarded by the package asset validator. This boundary contains no production-tool or widget artwork.
- Asset regeneration cleanup is fail-closed: obsolete manifest paths are removed only within the approved generated-asset directories.
- The active immutable identifiers are application ID `dev.deli.devhud`, build-only widget ID `dev.deli.devhud.widget`, future shared App Group `group.dev.deli.devhud`, settings key `devhud.settings.v1`, and widget-state key `devhud.widget-configuration.v1`. The App Group may be present on the iOS application solely for scoped shared state; the widget extension identity and Android receiver metadata are absent from distributed mobile artifacts.
- The frontend persistence boundary is limited to record-specific read/write commands for those two keys. Both desktop CEF and standard Tauri mobile runtimes use the same dependency-injected adapter contract; no broad filesystem, default-store, arbitrary-key, account, or remote-storage authority is exposed.
- Tauri authority is partitioned into exact desktop HUD, desktop settings, and mobile-main capability manifests. Each is local-origin-only and grants explicit application commands rather than plugin defaults. The desktop HUD can identify/read its render state and manage HUD lifecycle only; settings owns desktop mutations, diagnostics/reset, shortcut/autostart, and the typed updater request; mobile owns only its record, diagnostics, and reset boundary. DevTools inherits the containing desktop window's same capability.
- Desktop integration failures surface the effective native shortcut and launch-at-login state. An accessible confirmed reset preflights exact application-owned record, log, CEF-profile, and mobile-widget targets, rejecting an existing CEF target unless it is a directory; reconciles both retained providers with the effective records; clears startup diagnostics when record removal starts; preserves user-owned diagnostic exports; and distinguishes partially retained stable records from post-record cleanup failure that may retain transaction stages or CEF profile data.
- Mobile persistence remains device-local: iOS excludes the record directory from device backups, and Android disables cloud backup and device transfer.
- Production tool registration, production widget configuration, and user-visible mobile widget state are empty in `0.1.0`; stable fixture `toolId` references and fixture widget state may exist only in tests. Package-local WidgetKit and Android `AppWidgetProvider` source targets compile independently. The iOS release application has no extension dependency, embedded `.appex`, or extension provisioning payload, and the Android release application has no provider module dependency or receiver metadata.
- DevHud has no CLI, backend, public API, Connect RPC service, route, deep link, plugin SDK, remote plugin surface, telemetry, account system, cloud synchronization, or DeliDev integration. In particular, it must not consume DeliDev accounts, catalog, billing, APIs, routes, or contracts.
- Networking is deny-by-default. The only future application network exception is separately scoped unauthenticated, GitHub Releases-only update discovery and download for compatible `devhud@v*` releases. The current typed updater has no network implementation, and no GitHub token or other service credential may ship in the app.
- Desktop release publication requires signed architecture-specific installers, updater material, checksums, SPDX SBOMs, provenance, and all required publisher credentials. Mobile publication uses TestFlight and Google Play beta channels; store rejection uses the documented internal/closed-testing fallback.
- Diagnostic data uses a strict typed allowlist, fresh per-process UUID v7 correlation, seven-day/20 MB local rotation, and native user-selected export only after explicit action. Cancellation leaves the destination unchanged, paths and exceptions never cross IPC or enter the bundle, reset preserves user-owned exports, and no diagnostic transport exists. There is no remote telemetry, crash reporting, online dashboard, remote alert, feature flag service, or kill switch.

## Change Policy

- Update this index, [apps-devhud-foundation](apps-devhud-foundation.md), `docs/README.md`, `README.md`, root `AGENTS.md`, and `apps/AGENTS.md` together when DevHud ownership, identifiers, runtime boundaries, supported platforms, security exclusions, or policy changes.
- The application package, production desktop shell, sign-ready bundle configuration, native mobile hosts, Cargo workspace membership, deterministic Turborepo output contract, build-only native widget sources, and native widget CI jobs are present. Add product behavior, widget distribution, full matrix CI, updater networking, packaging, release workflows, signing/publisher material, and support material only with their corresponding documented contracts and validation.
- Any route, command, API, plugin, account, telemetry, DeliDev, widget-registration, or network exception proposal requires an explicit contract change before implementation; none is authorized by this project index.
- Changes to the pinned Tauri CEF commit, `@tauri-apps/cli-cef` version, or supported OS/architecture matrix require architecture and release-policy review. Do not track a moving upstream branch or patch Tauri, WRY, or `cef-rs` locally.
- Changes to release tags, artifacts, beta channels, signing prerequisites, updater selection, rollback, or upstream monitoring must update this index, the app foundation contract, root/app policy, and the relevant workflow or runbook contract together.

## References

- [Project template](project-template.md)
- [Domain contract](domain-template.md)
- [DevHud app foundation](apps-devhud-foundation.md)
- [Documentation catalog](README.md)
- [Repository defaults](repository-defaults.md)
- [Issue #729](https://github.com/delinoio/oss/issues/729)
