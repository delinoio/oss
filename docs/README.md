# Documentation Catalog

## Purpose
`docs/` is the source of truth for repository contracts.
Each project must have one project index document and one or more domain contract documents.

## Repository Defaults
- Repository-wide default technology choices and workflow defaults are defined in `docs/repository-defaults.md`.
- Repository configuration, stable root development commands, local development modes, environment ownership, startup-generation integrity, and secret classification are defined in `docs/repository-environment-contract.md`.
- Project and domain contracts must document deviations from those defaults when a different language, ID format, search backend, build toolchain, static-site deployment platform, or file storage/access pattern is chosen.

## Documentation Editing Rules
- These rules apply to documentation authoring and editing work, not general conversational summaries.
- Do not arbitrarily omit, delete, or simplify requested or source-backed content during documentation edits unless the user explicitly asks for that outcome.
- If documentation content, scope, or intent is ambiguous, ask the user before deciding what to remove, merge, or reinterpret.
- If a documentation change affects repository or domain policy boundaries, update or create the relevant `AGENTS.md` file in the same change when needed.
- `docs/` remains the internal source of truth for contracts, architecture notes, repo-local paths, and implementation details. Public documentation is owned and built by `apps/public-docs`; the project content roots are `apps/public-docs/docs/{runmoor,nodeup,binpm,async-commit-hook,clibox,pnport}`. Those pages must curate from these contracts without documenting repository-internal implementation details unless the detail is a stable public interface, user-visible behavior, or explicitly public maintainer workflow.

## Naming Rules
- Project index docs: `docs/project-<project-id>.md`
- Domain contract docs: `docs/<domain>-<project-or-component>-<contract>.md`
- Domain prefix must be one of: `apps`, `cmds`, `servers`, `crates`, `protos`, `packages`
- Repository-level contract docs: `docs/repository-<topic>-contract.md`
- Use lowercase kebab-case identifiers and stable enum-style IDs in contract sections.

## Templates
- `docs/repository-defaults.md`: repository-wide default technology choices and workflow defaults
- `docs/project-template.md`: template for project index docs
- `docs/domain-template.md`: template for domain contract docs

## Repository Workflow

- `docs/repository-workflow-contract.md`: CI selection and validation, manual CLI project/version releases, `delino-release-bot` setup, exact-commit recovery, Runmoor stable publication, and consolidated documentation publication.

## Project Catalog

### delidev
- [Project index](project-delidev.md)
- [CLI/server/Worker contract](cmds-delidev-contract.md)
- [Connect protocol](protos-delidev-v1-contract.md)
- [Worker workspace preparation](cmds-delidev-workspace-contract.md)
- [Owned process contract](cmds-delidev-process-contract.md)
- [Native harness adapter contract](cmds-delidev-harness-contract.md)
- [Protected credential storage](cmds-delidev-credentials-contract.md)
- [Account lifecycle](cmds-delidev-accounts-contract.md)
- [Provider inspection](cmds-delidev-providers-contract.md)
- [Provider and model catalog](cmds-delidev-catalog-contract.md)
- [Native API relay](cmds-delidev-proxy-contract.md)
- [Session acceptance and input queue](cmds-delidev-sessions-contract.md)
- [Complete issue #964 requirements](cmds-delidev-requirements.md)
- [Implementation and evidence ledger](cmds-delidev-evidence.md)

### binpm
- `docs/project-binpm.md`
- `docs/crates-binpm-foundation.md`
- `docs/apps-binpm-docs-foundation.md` (`apps/public-docs/docs/binpm`, canonical URL `https://oss.delino.io/binpm`, routes published below `/binpm`: `/`, `/installation`, `/getting-started`, `/commands`, `/local-tooling`, `/cache-and-verification`, `/releases`, `/troubleshooting`, `/reference`)

### cargo-mono
- `docs/project-cargo-mono.md`
- `docs/crates-cargo-mono-foundation.md`

### clibox
- `docs/project-clibox.md`
- `docs/crates-clibox-foundation.md` (five private Rust crates: CLI composition, configuration, OS utilities, offline transformations, and TCP/HTTP/file readiness; npm/native distribution only)
- `docs/packages-clibox-distribution-contract.md`
- `docs/apps-clibox-docs-foundation.md` (`apps/public-docs/docs/clibox`, canonical URL `https://oss.delino.io/clibox`, twelve user-guide routes)

### nodeup
- `docs/project-nodeup.md`
- `docs/crates-nodeup-foundation.md`
- `docs/apps-nodeup-docs-foundation.md` (`apps/public-docs/docs/nodeup`, canonical URL `https://oss.delino.io/nodeup`, routes published below `/nodeup`: `/`, `/installation`, `/getting-started`, `/commands`, `/runtime-resolution`, `/shims-and-package-managers`, `/output`, `/completions`, `/releases`, `/troubleshooting`, `/reference`)

### with-watch
- `docs/project-with-watch.md`
- `docs/crates-with-watch-foundation.md`

### runmoor
- `docs/project-runmoor.md`
- `docs/cmds-runmoor-foundation.md`
- `docs/apps-runmoor-docs-foundation.md` (`apps/public-docs/docs/runmoor`, canonical URL `https://oss.delino.io/runmoor`, routes published below `/runmoor`: `/`, `/install`, `/configuration`, `/commands`, `/docker`, `/tart`, `/operations`)

### derun
- `docs/project-derun.md`
- `docs/cmds-derun-foundation.md`

### public-docs
- `docs/project-public-docs.md`
- `docs/apps-public-docs-foundation.md` (single `oss.delino.io` publisher, six project subpaths, site selector, and stable `/devhud` section)
- `docs/packages-docs-site-switcher-contract.md` (shared accessible documentation site selector package and fixed site registry)

### serde-feather
- `docs/project-serde-feather.md`
- `docs/crates-serde-feather-core-foundation.md`
- `docs/crates-serde-feather-macros-foundation.md`

### rustia
- `docs/project-rustia.md`
- `docs/crates-rustia-core-foundation.md`
- `docs/crates-rustia-llm-foundation.md`
- `docs/crates-rustia-macros-foundation.md`

### devhud
The deterministic bilingual frontend, target-isolated Tauri desktop CEF plus iOS/Android system-webview hosts, production WidgetKit/AppWidgetProvider Deck widgets with backup-excluded coordinated iOS state and trusted Android configuration entry, synchronized Settings boundary, direct-client GitHub.com provider/setup and RealQA issue submission, desktop RealQA capture/editor/encrypted drafts/direct uploads, least-privilege Chrome context picker, authenticated Native Messaging broker, administrator SPA, and Admin API are implemented; other product-result surfaces and the remaining DevHUD domains are planned.

- `docs/project-devhud.md`
- `docs/apps-devhud-foundation.md`
- `docs/apps-devhud-security-contract.md`
- `docs/apps-devhud-updater-contract.md`
- `docs/apps-devhud-chrome-extension-contract.md`
- `docs/apps-devhud-admin-contract.md`
- `docs/servers-devhud-api-contract.md`
- `docs/servers-devhud-release-controller-contract.md`
- `docs/protos-devhud-v1-contract.md`
- `docs/packages-devhud-api-client-contract.md`
- `docs/crates-devhud-native-messaging-host-contract.md`
- `docs/apps-devhud-operations-contract.md` (internal maintainer release, recovery, support, and high-severity runbooks)
- `docs/apps-devhud-support-contract.md` (administrator support, diagnostics, retention, and high-severity triage)
- `docs/repository-workflow-contract.md` (repository-level workflow and read-only CEF review contract)

- [Linux CLI package repositories](repository-linux-packages-contract.md): APT/DNF build compatibility, signing, publication, recovery and rollout.

### async-commit-hook
- [Project index](project-async-commit-hook.md)
- [Command](cmds-async-commit-hook-contract.md), [app](apps-async-commit-hook-contract.md), [protocol](protos-async-commit-hook-v1-contract.md), [client](packages-async-commit-hook-api-client-contract.md)
- [Complete requirements](cmds-async-commit-hook-requirements.md)
- [Release and recovery](cmds-async-commit-hook-release-contract.md)

- `apps-async-commit-hook-docs-foundation.md`: Rspress public documentation owned at `apps/public-docs/docs/async-commit-hook`, published below `/async-commit-hook`, with installer routes, the `/docs` migration route, and validation contract.

### pnport
- [Project index](project-pnport.md)
- [Rust foundation](crates-pnport-foundation.md)
- [Complete issue #958 requirements](crates-pnport-requirements.md)
- [npm/native distribution](packages-pnport-distribution-contract.md)
- [Public documentation](apps-pnport-docs-foundation.md) (ten guides under `/pnport`, with 0.1.0 marked unreleased)
