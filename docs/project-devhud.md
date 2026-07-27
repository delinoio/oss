# Project: devhud

## Purpose

Define DevHud as a developer-tool shell with a usable signed-out, bundled-asset base experience and two explicitly bounded future authenticated features: Deck and RealQA. This contract authorizes implementation for issues #755 and #757; it does not claim that either feature, service, origin, provider registration, catalog entry, extension, widget, or release artifact exists or is active.

The implemented foundation remains under `apps/devhud`: React/TypeScript/Rsbuild, the exact pinned Tauri desktop CEF runtime, standard mobile system webviews, tray/shortcut/autostart behavior, a closed internal tool registry, typed local persistence, bounded diagnostics, device-local reset, and non-distributed native-widget fixtures. Deck may later add authenticated GitHub.com pull-request workflows on desktop, iOS, Android, tray, shortcuts, notifications, and native widgets. RealQA may later add authenticated screenshot capture and new-GitHub.com-issue submission on supported desktop systems plus an exact-origin Chrome MV3/native-host bridge. Neither feature is part of the signed-out base shell.

## Stable Project Identifier

`devhud`

## Domain Ownership Map

- `apps/devhud` (`app`): the sole full DevHud feature client and native-integration path. It owns the signed-out base shell; shared authentication client; Deck desktop/mobile/tray/shortcut/notification/widget UI; RealQA desktop capture/editor, encrypted local drafts, Chrome extension, and native-host source.
- `servers/devhud-deck` (`deck-server`, planned): the Go/PostgreSQL/sqlc Deck service described by [servers-devhud-deck-foundation](servers-devhud-deck-foundation.md).
- `protos/devhud-deck` (`deck-api`, planned): the versioned `devhud.deck.v1` Connect contract described by [protos-devhud-deck-api-contract](protos-devhud-deck-api-contract.md).
- `servers/devhud-realqa` (`realqa-server`, planned): the Go/PostgreSQL/sqlc RealQA service described by [servers-devhud-realqa-foundation](servers-devhud-realqa-foundation.md).
- `protos/devhud-realqa` (`realqa-api`, planned): the versioned `devhud.realqa.v1` Connect contract described by [protos-devhud-realqa-api-contract](protos-devhud-realqa-api-contract.md).

No full DevHud feature client or native implementation belongs under `apps/delidev-app`, `servers/delibase`, `protos/delibase`, `crates/`, `cmds/`, or a public plugin/package path. DeliDev may consume only `DeckIntegrationService` to expose connection management through its existing `/account` and `/o/:orgSlug/settings` sections. It also owns the planned `/auth/devhud/callback` static route and exact-path Apple/Android association artifacts required to verify the Deck mobile link; these narrow responsibilities do not transfer DevHud ownership or authorize another top-level DeliDev feature route.

## Domain Contract Documents

- [apps-devhud-foundation](apps-devhud-foundation.md)
- [servers-devhud-deck-foundation](servers-devhud-deck-foundation.md)
- [protos-devhud-deck-api-contract](protos-devhud-deck-api-contract.md)
- [servers-devhud-realqa-foundation](servers-devhud-realqa-foundation.md)
- [protos-devhud-realqa-api-contract](protos-devhud-realqa-api-contract.md)

## Cross-Domain Invariants

- The signed-out base shell remains usable without an account or network service. Authentication gates Deck and RealQA entry points and must not turn base settings, diagnostics, reset, tray access, or the empty foundation shell into signed-in-only behavior.
- The internal tool/view/tracker registries remain closed, source-controlled, and enum-backed. Deck initially permits only `GITHUB_PULL_REQUESTS`; RealQA initially permits only GitHub.com. No public plugin SDK, third-party provider adapter, user-authored runtime code, remotely supplied UI, arbitrary remote frontend asset, or server-defined component tree is authorized.
- Tauri, `tauri-build`, `tauri-runtime-cef`, `tauri-runtime-wry`, `@tauri-apps/cli-cef`, and the standard aliased mobile CLI retain the exact versions in the app contract. In particular, the upstream revision remains `f49ebda2fdba5755456b0f049e32593ca0ea331a`, `@tauri-apps/cli-cef` remains `3.0.0-alpha.6`, and `@tauri-apps/cli-mobile` remains exactly `2.11.4`; no Tauri, WRY, or `cef-rs` fork, local patch, or moving branch is allowed.
- Existing bundled-resource, sandbox, least-privilege capability, diagnostics, redaction, backup exclusion, and destructive-path validation guarantees remain in force. Feature implementation must add narrow window/command/network policies; it must not replace them with generic HTTP, filesystem, screen, process, store, shell, or environment authority exposed to frontend JavaScript.
- The only authenticated account boundary is DeliDev identity through Logto Authorization Code with PKCE in the system browser. Desktop uses a one-shot random `127.0.0.1` callback; Deck mobile uses the exact verified `https://deli.dev/auth/devhud/callback` universal/app link. Only a refresh token and device-session key may persist in the OS secure vault; access and ID tokens remain memory-only; one DeliDev account is active per OS user or device.
- Feature calls use a feature-audience bearer plus a memory-only delibase-audience forwarded bearer. Servers verify matching subjects and required scopes and never persist or log either token. Deck and RealQA use separate GitHub Apps and user authorization tokens; neither production GitHub App is registered by this documentation change.
- Future canonical origins are `https://deck.deli.dev`, `https://realqa.deli.dev`, and `https://assets.realqa.deli.dev`. They are contract identifiers only: no DNS, deployment, R2 production bucket, production secret, public image, or service activation is claimed or authorized here.
- Application networking remains deny-by-default. Authorized future exceptions are the existing signed GitHub Releases updater boundary; Logto/DeliDev authentication; Connect calls to the exact Deck or RealQA origin from the corresponding authenticated feature; RealQA same-origin signed upload PUTs and public-image delivery at its exact asset origin; verified Deck mobile auth callbacks; and the exact-origin RealQA Chrome native-host bridge. GitHub.com provider calls, delibase usage calls, and R2 administration are server-side feature boundaries. No arbitrary browsing, remote UI, remote configuration, client-side provider token use, or wildcard origin is allowed.
- Deck is the only feature authorized on desktop, iOS, Android, tray, shortcuts, notifications, WidgetKit, and Android widgets. RealQA is desktop-only on macOS 14+, Windows 11, and Ubuntu 24.04 (X11/XWayland plus native Wayland capture through `xdg-desktop-portal`) and may use only its signed Chrome MV3 native host; RealQA must not appear on iOS or Android.
- Remote client and extension telemetry, crash reporting, analytics, advertising, and user tracking remain prohibited. Feature servers may emit redacted structured operational logs, metrics, traces, and audit events; tokens, URLs, repositories, titles, queries, screenshots, issue text, and other user content are never telemetry fields.
- Production-facing Deck and RealQA delibase catalog records remain stable but disabled until a separate activation change. There is no deployment, production app registration, catalog activation, widget publication, extension publication, image publication, store release, or operational rollout in issues #755/#757.

## Offline, Reset, Disconnect, and Deletion Boundaries

- Base-shell local settings, diagnostics, and `Reset DevHud` retain the exact app-foundation behavior. User-selected diagnostic exports are never deleted by reset.
- Deck's only offline PR-data exception is a minimal encrypted widget snapshot containing freshness/offline state. The ordinary Deck UI shows connection/offline state instead of a cached PR list. Logout and `Reset DevHud` revoke the server push registration before deleting local credentials; an offline/ambiguous revoke disables the local/platform registration, leaves a credential-free cleanup tombstone, and must retry before any later registration. Logout deletes device tokens, PR data, and widget snapshots. Provider disconnect immediately deletes provider tokens, cached PR results, notification state, and widget snapshots but retains view definitions as disconnected records. Reset deletes device tokens, Deck snapshots, and shortcut effective state without deleting server views or connections. Owner-scoped idempotent `DeckViewService.DeleteFeatureData` removes personal Deck data for its personal caller or organization Deck data for an organization Owner, immediately blocks new scope access, and starts asynchronous hard deletion. Account/organization deletion has the same immediate access block and asynchronous deletion boundary, retaining only minimal pseudonymized financial/security records required by delibase.
- RealQA permits offline capture/edit and encrypted account-bound local drafts only after a prior successful login and device binding; first-time offline entry is forbidden and upload/submission requires online reauthentication. Successful submission and explicit draft deletion remove the local raw draft. Logout hides and locks drafts without deleting them, and only the same account may unlock them. Provider disconnect deletes tokens while retaining presets/mappings as disconnected records. `Reset DevHud` deletes local drafts, tokens, shortcut state, and extension pairing but cannot revoke Chrome-owned permissions. Personal feature deletion removes personal presets, submissions, and assets; an organization Owner removes organization RealQA data. Account/organization deletion blocks access immediately and asynchronously deletes feature data/assets, retaining only required pseudonymized financial/security records.
- RealQA private staging uploads expire after 24 hours. Submitted images remain until explicit image/range deletion, a GitHub issue-deletion webhook, account/organization deletion, or storage-billing grace expiry. Deleted public-image URLs return only the generic `Image removed` placeholder. Image deletion never depends on GitHub access; issue-body cleanup is best effort when valid authorization remains. Failed storage billing blocks new submissions, keeps existing images public for 30 days, does not back-bill grace days after recovery, and deletes unrecovered images after the grace period.

## Validation Contract

Documentation-only changes run link/path/status consistency checks and `git diff --check`. Once implementation exists, the synchronized validation set is:

- app: `pnpm --filter devhud typecheck`, `pnpm --filter devhud lint`, `pnpm --filter devhud test`, `pnpm --filter devhud test:a11y`, `pnpm --filter devhud build`, `pnpm --filter devhud test:security`, `pnpm --filter devhud test:diagnostics`, `pnpm --filter devhud test:deck`, `pnpm --filter devhud test:deck:widgets`, `pnpm --filter devhud test:realqa`, `pnpm --filter devhud test:realqa:native`, `pnpm --filter devhud test:realqa:extension`, and `pnpm --filter devhud check:realqa:package`;
- shared APIs: `pnpm generate:proto`, `pnpm check:proto`, Go test/vet for both proto roots, and TypeScript typechecks for both generated Connect packages;
- servers: Go format/vet/test, sqlc reproducibility, PostgreSQL migration/integration/concurrency tests, provider fixtures, and non-root multi-architecture image/SBOM/signature/attestation checks for both feature services.

Validation artifacts are fixtures only. Passing them must not publish an image, register either GitHub App, inject a production Chrome extension ID, deploy an origin, create DNS/R2 infrastructure, embed/register a release widget, publish an extension, enable a catalog record, or release an app.

## Change Triggers

- Update this index, every affected domain contract, `docs/README.md`, `README.md`, and root plus relevant `apps/`, `servers/`, and `protos/` `AGENTS.md` files together when ownership, authentication, provider, origin, client platform, offline behavior, deletion, service, API, registry, network, widget, extension, catalog, or activation status changes.
- Update the app contract before adding feature UI, native commands, capabilities, mobile links, network allowlists, widgets, Chrome host behavior, draft storage, or account behavior.
- Update the relevant server and proto contracts before adding RPCs, HTTP handlers, provider permissions, persistence, billing, retention, or generated clients.
- Activation is a separate change requiring explicit contracts and implementation for DNS, deployment, production secrets, GitHub App registration, catalog enablement, widget embedding/publication, extension publication, store release, and operational rollout.

## References

- [Issue #729](https://github.com/delinoio/oss/issues/729)
- [Issue #755](https://github.com/delinoio/oss/issues/755)
- [Issue #757](https://github.com/delinoio/oss/issues/757)
- [Issue #756](https://github.com/delinoio/oss/issues/756)
- [App foundation](apps-devhud-foundation.md)
- [Deck server](servers-devhud-deck-foundation.md)
- [Deck API](protos-devhud-deck-api-contract.md)
- [RealQA server](servers-devhud-realqa-foundation.md)
- [RealQA API](protos-devhud-realqa-api-contract.md)
- [Repository defaults](repository-defaults.md)

## Out of Scope

- Any feature outside Deck and RealQA, or any network/account/provider exception not enumerated above.
- GHES, custom GitHub hosts, on-premises connectors, non-GitHub trackers, or additional tracker/view kinds in v1.
- Public plugin/tracker SDKs, third-party view plugins, arbitrary remote UI, runtime code downloads, or remote configuration.
- Server-initiated Deck polling; guaranteed five-minute widget execution; ordinary offline Deck PR caches.
- Mobile RealQA, Incognito/full-page Chrome capture, comments or updates to existing issues, and automatic multi-issue splitting.
- Production deployment, DNS/R2 provisioning, GHCR publication, production GitHub App or extension registration, Chrome Web Store publication, widget/store publication, catalog activation, production SLOs/alerts, or rollout.
