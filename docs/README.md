# Documentation Catalog

## Purpose

`docs/` is the source of truth for repository contracts.
Each project must have one project index document and one or more domain contract documents.

## Finding the owning contract

Start with the [root task navigation](../AGENTS.md#task-navigation) and the project catalog below. Each project index lists its canonical paths, domain contracts and cross-domain invariants. Read the domain contracts for the paths and consumers affected by the task.

For a known source path, search project indexes with `rg -n -F '<source-path>' docs/project-*.md`. If the full file path has no match, repeat with its owning directory. Repository-wide changes use the defaults and workflow links below. Scoped `AGENTS.md` files provide additional directory rules.

Keep project-specific entries under their project heading and shared repository contracts under the repository sections. Preserve existing requirement, availability and validation annotations when moving entries.

## Repository Defaults

- Repository-wide default technology choices and workflow defaults are defined in `docs/repository-defaults.md`.
- Repository licensing and third-party notice boundaries are defined in `docs/repository-license-contract.md`.
- Dependency security updates, audit evidence, and unresolved upstream constraints are recorded in [the dependency security contract](repository-dependency-security-contract.md).
- Repository configuration, stable root development commands, local development modes, environment ownership, startup-generation integrity, and secret classification are defined in `docs/repository-environment-contract.md`.
- Project and domain contracts must document deviations from those defaults when a different language, ID format, search backend, build toolchain, static-site deployment platform, or file storage/access pattern is chosen.

## Instruction Update Policy

Instruction files contain development procedures, directory ownership and contract navigation only. Ordinary feature additions, bug fixes, tests and behavior-contract changes do not by themselves require an `AGENTS.md` update. Update those files only when development procedures, directory ownership or repository/domain development rules change. Feature requirements belong in the owning contract and must not be duplicated in instruction files. Do not add issue-specific feature paragraphs to `AGENTS.md`.

Record new implementation status, validation results and unresolved limits in pull requests, issues and CI logs/artifacts. Include the source revision, commands, results and unperformed checks. Distinguish fixtures, builds and packaging from actual native/account/platform acceptance. Exclude secrets, user state and raw native content. Preserve existing validation records.

## Documentation Editing Rules

- Do not add issue or pull-request links or identifying numbers to repository instruction files or documentation. Describe requirements directly and link to the owning repository-local contracts. Domain, service and technical-documentation links remain allowed; record issue/PR traceability in pull requests, issues or CI records instead.
- These rules apply to documentation authoring and editing work, not general conversational summaries.
- Do not arbitrarily omit, delete, or simplify requested or source-backed content during documentation edits unless the user explicitly asks for that outcome.
- If documentation content, scope, or intent is ambiguous, ask the user before deciding what to remove, merge, or reinterpret.
- Update or create the relevant `AGENTS.md` in the same change only when development procedures, directory ownership or repository/domain development rules change; follow the [instruction-update policy](#instruction-update-policy).
- `docs/` remains the internal source of truth for contracts, architecture notes, repo-local paths, and implementation details. Consolidated OSS public documentation is owned and built by `apps/public-docs`; the project content roots are `apps/public-docs/docs/{runmoor,nodeup,binpm,async-commit-hook,clibox,pnport}`. Those pages must curate from these contracts without documenting repository-internal implementation details unless the detail is a stable public interface, user-visible behavior, or explicitly public maintainer workflow. Independent Labs documentation is owned by `delinoio/apps:apps/labs-docs`; its external ownership is defined in [the Labs app boundary](apps-labs-docs-foundation.md).

## Naming Rules

- Project index docs: `docs/project-<project-id>.md`
- Domain contract docs: `docs/<domain>-<project-or-component>-<contract>.md`
- Domain prefix must be one of: `apps`, `cmds`, `servers`, `crates`, `protos`, `packages`
- Repository-level contract docs: `docs/repository-<topic>-contract.md`
- Use lowercase kebab-case identifiers and stable enum-style IDs in contract sections.

## Templates

- [repository-defaults.md](repository-defaults.md): repository-wide default technology choices and workflow defaults
- [project-template.md](project-template.md): template for project index docs
- [domain-template.md](domain-template.md): template for domain contract docs

## Repository Workflow

- [Linux CLI package repositories](repository-linux-packages-contract.md): APT/DNF build compatibility, signing, publication, recovery and rollout.

- [Prebuilt dependency distribution](repository-prebuilt-dependencies-contract.md): shared immutable host executables and consumer installation.

- [repository-workflow-contract.md](repository-workflow-contract.md): CI selection and validation, manual CLI project/version releases, `delino-release-bot` setup, exact-commit recovery, Runmoor stable publication, and consolidated documentation publication.

## Project Catalog

### binpm

- [project-binpm.md](project-binpm.md)
- [crates-binpm-foundation.md](crates-binpm-foundation.md)
- [apps-binpm-docs-foundation.md](apps-binpm-docs-foundation.md) (`apps/public-docs/docs/binpm`, canonical URL `https://oss.delino.io/binpm`, routes published below `/binpm`: `/`, `/installation`, `/getting-started`, `/commands`, `/local-tooling`, `/cache-and-verification`, `/releases`, `/troubleshooting`, `/reference`)

### cargo-mono

- [project-cargo-mono.md](project-cargo-mono.md)
- [crates-cargo-mono-foundation.md](crates-cargo-mono-foundation.md)

### clibox

- [project-clibox.md](project-clibox.md)
- [crates-clibox-foundation.md](crates-clibox-foundation.md) (five private Rust crates: CLI composition, configuration, OS utilities, offline transformations, and TCP/HTTP/file readiness; npm/native distribution only)
- [crates-clibox-fspy-contract.md](crates-clibox-fspy-contract.md) (file-access workflows and observation requirements; implementation in progress)
- [packages-clibox-distribution-contract.md](packages-clibox-distribution-contract.md) (npm, signed native archives, Linux packages and macOS Homebrew publication)
- [apps-clibox-docs-foundation.md](apps-clibox-docs-foundation.md) (`apps/public-docs/docs/clibox`, canonical URL `https://oss.delino.io/clibox`, thirteen user-guide routes including the source-only fspy guide)

### nodeup

- [project-nodeup.md](project-nodeup.md)
- [crates-nodeup-foundation.md](crates-nodeup-foundation.md)
- [apps-nodeup-docs-foundation.md](apps-nodeup-docs-foundation.md) (`apps/public-docs/docs/nodeup`, canonical URL `https://oss.delino.io/nodeup`, routes published below `/nodeup`: `/`, `/installation`, `/getting-started`, `/commands`, `/runtime-resolution`, `/shims-and-package-managers`, `/output`, `/completions`, `/releases`, `/troubleshooting`, `/reference`)

### with-watch

- [project-with-watch.md](project-with-watch.md)
- [crates-with-watch-foundation.md](crates-with-watch-foundation.md)

### runmoor

- [project-runmoor.md](project-runmoor.md)
- [cmds-runmoor-foundation.md](cmds-runmoor-foundation.md)
- [apps-runmoor-docs-foundation.md](apps-runmoor-docs-foundation.md) (`apps/public-docs/docs/runmoor`, canonical URL `https://oss.delino.io/runmoor`, routes published below `/runmoor`: `/`, `/install`, `/configuration`, `/commands`, `/docker`, `/tart`, `/host`, `/operations`)

### labs-site

- [Project index](project-labs-site.md)
- [Marketing site foundation](apps-labs-site-foundation.md) (external `delinoio/apps:apps/labs-site`, canonical URL `https://labs.delino.io`)

- [Labs localization](apps-labs-localization-contract.md) (external localization ownership)

### labs-docs

- [Project index](project-labs-docs.md)
- [Labs documentation foundation](apps-labs-docs-foundation.md) (standalone Rspress site at `https://docs.labs.delino.io`)

### public-docs

- [project-public-docs.md](project-public-docs.md)
- [apps-public-docs-foundation.md](apps-public-docs-foundation.md) (single `oss.delino.io` publisher, six project subpaths, site selector)
- [packages-docs-site-switcher-contract.md](packages-docs-site-switcher-contract.md) (shared accessible documentation site selector package and fixed site registry)

### serde-feather

- [project-serde-feather.md](project-serde-feather.md)
- [crates-serde-feather-core-foundation.md](crates-serde-feather-core-foundation.md)
- [crates-serde-feather-macros-foundation.md](crates-serde-feather-macros-foundation.md)

### jsonllm

- [Project index](project-jsonllm.md)
- [Rust foundation](crates-jsonllm-foundation.md)

### rustia

- [project-rustia.md](project-rustia.md)
- [crates-rustia-core-foundation.md](crates-rustia-core-foundation.md)
- [crates-rustia-llm-foundation.md](crates-rustia-llm-foundation.md)
- [crates-rustia-macros-foundation.md](crates-rustia-macros-foundation.md)

### async-commit-hook

- [Project index](project-async-commit-hook.md)
- [Command](cmds-async-commit-hook-contract.md), [app](apps-async-commit-hook-contract.md), [protocol](protos-async-commit-hook-v1-contract.md), [client](packages-async-commit-hook-api-client-contract.md)
- [Complete requirements](cmds-async-commit-hook-requirements.md)
- [Release and recovery](cmds-async-commit-hook-release-contract.md)

- [apps-async-commit-hook-docs-foundation.md](apps-async-commit-hook-docs-foundation.md): Rspress public documentation owned at `apps/public-docs/docs/async-commit-hook`, published below `/async-commit-hook`, with installer routes, the `/docs` migration route, and validation contract.

### pnport

- [Project index](project-pnport.md)
- [Rust foundation](crates-pnport-foundation.md)
- [fspy source fork and licensing](crates-fspy-vendor-contract.md)
- [Complete feature requirements and staged 0.1.0/0.2.0 releases](crates-pnport-requirements.md)
- [npm/native distribution](packages-pnport-distribution-contract.md)
- [Public documentation](apps-pnport-docs-foundation.md) (ten guides under `/pnport`, with 0.1.0 marked unreleased)

### cachecow

- [Project index](project-cachecow.md)
- [Complete requirements](cmds-cachecow-requirements.md) (full cache/shim requirements and native acceptance gates)
- [Go command foundation](cmds-cachecow-foundation.md) (`cmds/cachecow`)
- [npm and native distribution](packages-ccow-distribution-contract.md) (`packages/ccow`, package `@delino/ccow`)
- [Public documentation](apps-cachecow-docs-foundation.md) (`apps/public-docs/docs/cachecow`)

## Transferred projects

Delcore, DeliDev, and DeliTools, including their contracts and historical DeliDev validation records, are maintained in [delino-apps](https://github.com/delinoio/delino-apps). This repository no longer owns their runtime paths or CI jobs.

Bundlewhy, including its Rust CLI, web UI, adapters, protocol and public guides, is maintained in [delinoio/bundlewhy](https://github.com/delinoio/bundlewhy).

Typeopt, including its Rust CLI, fixtures, Vite example, benchmark tools and contracts, is maintained in [delinoio/typeopt](https://github.com/delinoio/typeopt).
