# Documentation Catalog

## Purpose
`docs/` is the source of truth for repository contracts.
Each project must have one project index document and one or more domain contract documents.

## Repository Defaults
- Repository-wide default technology choices and workflow defaults are defined in `docs/repository-defaults.md`.
- Project and domain contracts must document deviations from those defaults when a different language, ID format, search backend, build toolchain, static-site deployment platform, or file storage/access pattern is chosen.

## Documentation Editing Rules
- These rules apply to documentation authoring and editing work, not general conversational summaries.
- Do not arbitrarily omit, delete, or simplify requested or source-backed content during documentation edits unless the user explicitly asks for that outcome.
- If documentation content, scope, or intent is ambiguous, ask the user before deciding what to remove, merge, or reinterpret.
- If a documentation change affects repository or domain policy boundaries, update or create the relevant `AGENTS.md` file in the same change when needed.
- `docs/` remains the internal source of truth for contracts, architecture notes, repo-local paths, and implementation details. Public documentation surfaces under `apps/*-docs` and `apps/public-docs` must curate from those contracts without documenting repository-internal implementation details unless the detail is a stable public interface, user-visible behavior, or explicitly public maintainer workflow.

## Naming Rules
- Project index docs: `docs/project-<project-id>.md`
- Domain contract docs: `docs/<domain>-<project-or-component>-<contract>.md`
- Domain prefix must be one of: `apps`, `cmds`, `servers`, `crates`, `protos`, `packages`
- Use lowercase kebab-case identifiers and stable enum-style IDs in contract sections.

## Templates
- `docs/repository-defaults.md`: repository-wide default technology choices and workflow defaults
- `docs/project-template.md`: template for project index docs
- `docs/domain-template.md`: template for domain contract docs

## Project Catalog

### binpm
- `docs/project-binpm.md`
- `docs/crates-binpm-foundation.md`
- `docs/apps-binpm-docs-foundation.md` (`apps/binpm-docs`, production URL `https://binpm.delino.io`, routes: `/`, `/installation`, `/getting-started`, `/commands`, `/local-tooling`, `/cache-and-verification`, `/releases`, `/troubleshooting`, `/reference`)

### cargo-mono
- `docs/project-cargo-mono.md`
- `docs/crates-cargo-mono-foundation.md`

### nodeup
- `docs/project-nodeup.md`
- `docs/crates-nodeup-foundation.md`
- `docs/apps-nodeup-docs-foundation.md` (`apps/nodeup-docs` routes: `/`, `/installation`, `/getting-started`, `/commands`, `/runtime-resolution`, `/shims-and-package-managers`, `/output`, `/completions`, `/releases`, `/troubleshooting`, `/reference`)

### with-watch
- `docs/project-with-watch.md`
- `docs/crates-with-watch-foundation.md`

### derun
- `docs/project-derun.md`
- `docs/cmds-derun-foundation.md`

### mpapp
- `docs/project-mpapp.md`
- `docs/apps-mpapp-foundation.md`

### public-docs
- `docs/project-public-docs.md`
- `docs/apps-public-docs-foundation.md`

### thenv
- `docs/project-thenv.md`
- `docs/servers-thenv-server-foundation.md`
- `docs/cmds-thenv-cli-foundation.md`

### serde-feather
- `docs/project-serde-feather.md`
- `docs/crates-serde-feather-core-foundation.md`
- `docs/crates-serde-feather-macros-foundation.md`

### rustia
- `docs/project-rustia.md`
- `docs/crates-rustia-core-foundation.md`
- `docs/crates-rustia-llm-foundation.md`
- `docs/crates-rustia-macros-foundation.md`

### ttl
- `docs/project-ttl.md`
- `docs/cmds-ttl-foundation.md`
- `docs/cmds-ttl-language-contract.md`

### delidev
- `docs/project-delidev.md`
- `docs/apps-delidev-app-foundation.md` (`apps/delidev-app`, future canonical origin `https://deli.dev`, static Cloudflare Pages artifact, and bounded RealQA tracker settings)

### delibase
- `docs/project-delibase.md`
- `docs/servers-delibase-server-foundation.md` (`servers/delibase`, future API origin `https://delibase.deli.dev`)
- `docs/protos-delibase-api-contract.md` (`protos/delibase/v1`, `delibase.v1`)
- `docs/servers-internal-foundation.md` (repository-shared `servers/internal`
  boundary consumed by delibase and explicitly reviewed DevHud subsets)

### devhud
- `docs/project-devhud.md`
- `docs/apps-devhud-foundation.md` (`apps/devhud`; signed-out base shell, Deck
  dependency-injected client-owned refresh controller, RealQA encrypted
  local-draft/capture foundations, and all Deck/RealQA client/native/Chrome
  surfaces)
- `docs/servers-devhud-deck-foundation.md` (implemented inactive
  `servers/devhud-deck` foundation and GitHub.com provider, client-owned
  refresh/live billing, snapshot, and notification-resolution slices; future
  inactive `https://deck.deli.dev`)
- `docs/protos-devhud-deck-api-contract.md` (implemented private
  `protos/devhud-deck/v1`; `devhud.deck.v1`, isolated descriptor,
  Go/TypeScript Connect artifacts, typed refresh client/freshness/outcome/billing
  state, device/candidate reads, mutations, and non-dispatching all-origin
  refresh preflight)
- `docs/servers-devhud-realqa-foundation.md` (implemented inactive preset/tracker/auth/deletion, internal GitHub.com provider with stable-ID repository rename synchronization, R2-backed image-transfer/public-delivery, replay-safe online submission/live-transfer/initial-storage authorization, and recurring storage/rebind/grace/terminal cleanup at `servers/devhud-realqa`; inactive `https://realqa.deli.dev` / `https://assets.realqa.deli.dev` origins)
- `docs/protos-devhud-realqa-api-contract.md` (implemented `protos/devhud-realqa/v1`; `devhud.realqa.v1`, isolated descriptor and private generated package)

DevHud's implemented signed-out base shell remains bundled, usable without an
account, and protected by the exact pinned sandboxed Tauri CEF runtime, standard
mobile system webviews, closed internal registry, least-privilege capabilities,
typed seven-day/20 MB local diagnostics/export, device-local reset, and backup
exclusions. The private `devhud.deck.v1` and `devhud.realqa.v1` sources, isolated
descriptors, generated Go/TypeScript artifacts, and private
`@delinoio/devhud-deck-connect` and `@delinoio/devhud-realqa-connect` workspace
exports are implemented, including Deck's structured request-only shortcut
configurations and server-authored shortcut/widget state; Deck's inactive
server provider/client-owned refresh slices and dependency-injected client
controller are implemented, while live native transport registration and push
delivery remain planned. RealQA's inactive preset/tracker/auth/deletion
foundation is implemented. Issues #755/#757 authorize two bounded
authenticated exceptions: Deck on
desktop/mobile/tray/shortcuts/notifications/native widgets, and desktop-only
RealQA plus its signed exact-origin Chrome MV3 native host. Authentication is
limited to Logto/DeliDev; bundled webviews remain offline while exact future
feature RPCs and RealQA signed upload PUTs cross closed native transports.
Provider work stays behind separate least-privilege GitHub Apps; RealQA's
private schema preserves issue types, Issue Form text prefills, textarea render
languages, and dropdown multiplicity; full RealQA feature management stays in
DevHud while DeliDev exposes only bounded `RealQATrackerService` connection and
destination settings in its existing account/organization sections; and
delibase account/organization deletion durably
invokes both features' service-authenticated cleanup mode. No public plugin SDK,
arbitrary remote UI, client/extension telemetry, DNS/deployment, production app
registration, catalog activation, widget/extension/store publication, or
rollout is claimed. Production-facing Deck and RealQA catalog records remain
disabled.
