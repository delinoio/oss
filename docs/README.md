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
- `docs/apps-binpm-docs-foundation.md` (`apps/binpm-docs`, production URL `https://binpm.delino.io`, routes: `/`, `/installation`, `/getting-started`, `/commands`, `/local-tooling`, `/cache-and-verification`, `/troubleshooting`, `/reference`)

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
