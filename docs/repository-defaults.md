# Repository Defaults

## Purpose
This document defines default technology choices and repository workflow defaults for new repository work when a more specific project or domain contract does not already choose a different approach.

## Default Technology Choices
- New persisted entities should use UUID v7 identifiers by default. Use another identifier shape only when there is a documented compatibility, storage, protocol, or product reason.
- AI-based search should use Cloudflare AI Search by default. Use another search backend only when the project contract documents the reason and migration boundary.
- When a new project does not specify its primary language, default to Golang.
- Build tooling should prefer the Rspack family when it fits the runtime and deployment target, including Rsbuild and Rspress for app and documentation surfaces.
- Static sites under `apps/` should use Rsbuild/Rspress-style toolchains and deploy to Cloudflare Pages by default. Any exception must be documented in its project contract.
- File handling should use Cloudflare R2 for object storage plus signed URLs for upload and download access by default. Use another storage or access pattern only when the project contract documents the reason, trust boundary, and migration considerations.

## Documentation Requirements
- Project index docs must record deviations from these defaults in `Cross-Domain Invariants` or `Change Policy`.
- Domain contract docs must record deviations in the relevant `Runtime and Language`, `Storage`, `Security`, `Build and Test`, or `Dependencies and Integrations` sections.
- Repository and domain `AGENTS.md` files must stay aligned with this document when these defaults change.

## Repository Workflow Defaults
- Newly created pull requests must use Conventional Commit-style titles with a required scope: `<type>(<scope>): <description>`.
- Pull request title scopes should use stable lowercase project, component, domain, or tooling identifiers from repository contracts when one applies.
- Pull request titles must not omit the scope and must not use bracket-style project prefixes.
- `pnpm install` installs Lefthook through `scripts/install-git-hooks.sh`. When a linked worktree inherits a `core.hooksPath` that exactly names Git's common-directory `hooks` path, the script may use Lefthook's `--force` mode for that exact default path; unrelated custom hook directories remain untouched and follow Lefthook's normal validation.
- Root `pnpm dev` keeps development servers as package-owned persistent Turbo tasks and runs Turbo through Derun. It probes both IPv4 and IPv6 loopback listeners, preserves every documented default port that is available, and assigns only conflicting apps a free temporary port while preserving fixed preview ports.
- The package-local development port variables are `PUBLIC_DOCS_DEV_PORT`, `NODEUP_DOCS_DEV_PORT`, `BINPM_DOCS_DEV_PORT`, `DELIDEV_APP_DEV_PORT`, and `DEVHUD_DEV_PORT`. Explicit values are validated and fail if occupied or duplicated; automatic selections are emitted as structured `port_remap` diagnostics and passed through only to the `dev` task.

## References
- `docs/README.md`
- `docs/project-template.md`
- `docs/domain-template.md`
- `AGENTS.md`
- `apps/AGENTS.md`
