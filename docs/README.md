# Documentation Catalog

## Purpose
`docs/` is the source of truth for repository contracts.
Each project must have one project index document and one or more domain contract documents.

## Naming Rules
- Project index docs: `docs/project-<project-id>.md`
- Domain contract docs: `docs/<domain>-<project-or-component>-<contract>.md`
- Domain prefix must be one of: `apps`, `cmds`, `servers`, `crates`, `protos`, `packages`
- Use lowercase kebab-case identifiers and stable enum-style IDs in contract sections.

## Templates
- `docs/project-template.md`: template for project index docs
- `docs/domain-template.md`: template for domain contract docs

## Project Catalog

### cargo-mono
- `docs/project-cargo-mono.md`
- `docs/crates-cargo-mono-foundation.md`

### nodeup
- `docs/project-nodeup.md`
- `docs/crates-nodeup-foundation.md`

### derun
- `docs/project-derun.md`
- `docs/cmds-derun-foundation.md`

### devmon
- `docs/project-devmon.md`
- `docs/cmds-devmon-foundation.md`

### mpapp
- `docs/project-mpapp.md`
- `docs/apps-mpapp-foundation.md`

### devkit
- `docs/project-devkit.md`
- `docs/apps-devkit-foundation.md`

### devkit-remote-file-picker
- `docs/project-devkit-remote-file-picker.md`
- `docs/apps-devkit-remote-file-picker-foundation.md`

### public-docs
- `docs/project-public-docs.md`
- `docs/apps-public-docs-foundation.md`

### devkit-commit-tracker
- `docs/project-devkit-commit-tracker.md`
- `docs/apps-devkit-commit-tracker-web-app-foundation.md`
- `docs/servers-devkit-commit-tracker-api-server-foundation.md`
- `docs/cmds-devkit-commit-tracker-collector-foundation.md`

### thenv
- `docs/project-thenv.md`
- `docs/apps-thenv-web-console-foundation.md`
- `docs/servers-thenv-server-foundation.md`
- `docs/cmds-thenv-cli-foundation.md`

### serde-feather
- `docs/project-serde-feather.md`
- `docs/crates-serde-feather-core-foundation.md`
- `docs/crates-serde-feather-macros-foundation.md`

### dexdex
- `docs/project-dexdex.md`
- `docs/apps-dexdex-desktop-app-foundation.md`
- `docs/servers-dexdex-main-server-foundation.md`
- `docs/servers-dexdex-worker-server-foundation.md`
- `docs/protos-dexdex-v1-contract.md`

### ttl
- `docs/project-ttl.md`
- `docs/cmds-ttl-foundation.md`
- `docs/cmds-ttl-language-contract.md`
