# Project: devhud

## Goal

Define the documentation-first foundation for DevHud, a local-only developer-tool shell for individual developers. The project is intentionally independent from the DeliDev web platform and establishes the desktop, mobile, widget-boundary, security, diagnostic, support, CI, and release contracts before runtime implementation begins.

This issue does not create an application, package, CI job, release workflow, or user-visible tool. `0.1.0` is the target foundation preview: production tools and user-visible widgets remain empty.

## Project ID

`devhud`

## Domain Ownership Map

- `apps/devhud` (`app`): the sole canonical implementation path for the future one-package React/TypeScript/Rsbuild frontend, Tauri Rust application, desktop CEF shell, mobile system-webview shell, and native widget foundation sources.

No other repository path may implement DevHud. `servers/`, `protos/`, `crates/`, `cmds/`, `packages/`, and `apps/delidev-app` are not DevHud ownership paths.

## Domain Contract Documents

- [apps-devhud-foundation](apps-devhud-foundation.md)

## Cross-Domain Invariants

- DevHud's stable project identifier is `devhud`; its sole canonical path is `apps/devhud`.
- The future app is one package: a React and TypeScript frontend built with Rsbuild, a Tauri Rust application under `src-tauri`, and platform-native widget foundation sources. Root package scripts remain delegators; package-local scripts own app behavior.
- Desktop uses Tauri's pinned upstream CEF runtime. Mobile uses standard Tauri iOS WKWebView and Android WebView runtimes. Desktop and mobile must not silently substitute one another's runtime model.
- The CEF feasibility gate must pass on macOS, Windows, and Ubuntu for x64 and ARM64 before product-foundation and release work proceeds. A required fork, local runtime patch, or failed gate stops the work and requires a separate architecture decision.
- The exact identifiers are immutable contracts: application ID `dev.deli.devhud`, settings key `devhud.settings.v1`, widget configuration key `devhud.widget-configuration.v1`, iOS App Group `group.dev.deli.devhud`, and build-only widget identifier `dev.deli.devhud.widget`.
- Production tool registration and user-visible mobile widgets are empty in `0.1.0`; fixture tools and fixture widget state may exist only in tests.
- DevHud has no CLI, backend, public API, Connect RPC service, route, deep link, plugin SDK, remote plugin surface, telemetry, account system, cloud synchronization, or DeliDev integration. In particular, it must not consume DeliDev accounts, catalog, billing, APIs, routes, or contracts.
- The only application network exception is unauthenticated, GitHub Releases-only update discovery and download for compatible `devhud@v*` releases. No GitHub token or other service credential may ship in the app.
- Desktop release publication requires signed architecture-specific installers, updater material, checksums, SPDX SBOMs, provenance, and all required publisher credentials. Mobile publication uses TestFlight and Google Play beta channels; store rejection uses the documented internal/closed-testing fallback.
- Diagnostic data is local, redacted, bounded, and user-exported only after explicit action. There is no remote telemetry, crash reporting, online dashboard, remote alert, feature flag service, or kill switch.

## Change Policy

- Update this index, [apps-devhud-foundation](apps-devhud-foundation.md), `docs/README.md`, `README.md`, root `AGENTS.md`, and `apps/AGENTS.md` together when DevHud ownership, identifiers, runtime boundaries, supported platforms, security exclusions, or policy changes.
- When the implementation is authorized, update the app package manifest, workspace membership, Turborepo task graph, CI workflows, release workflows, signing/publisher material, support material, and this documentation in the same change. This documentation-only change does not add those artifacts.
- Any route, command, API, plugin, account, telemetry, DeliDev, widget-registration, or network exception proposal requires an explicit contract change before implementation; none is authorized by this project index.
- Changes to the pinned Tauri CEF commit, `@tauri-apps/cli-cef` version, supported OS/architecture matrix, or CEF gate criteria require a separate architecture and release-policy review. Do not track a moving upstream branch or patch Tauri, WRY, or `cef-rs` locally.
- Changes to release tags, artifacts, beta channels, signing prerequisites, updater selection, rollback, or upstream monitoring must update this index, the app foundation contract, root/app policy, and the relevant workflow or runbook contract together.

## References

- [Project template](project-template.md)
- [Domain contract](domain-template.md)
- [DevHud app foundation](apps-devhud-foundation.md)
- [Documentation catalog](README.md)
- [Repository defaults](repository-defaults.md)
- [Issue #729](https://github.com/delinoio/oss/issues/729)
