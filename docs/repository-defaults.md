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
- Root `pnpm install` must support linked worktrees when the effective Git hooks path resolves to the repository's shared common-directory hooks directory, without overriding an unrelated custom hooks path. Source archives and other workspaces without Git metadata skip hook installation while continuing app preparation.
- Root `pnpm dev` is reserved for the DevHud team workflow. Documentation apps retain root entry points through `pnpm dev:public-docs`, `pnpm dev:nodeup-docs`, and `pnpm dev:binpm-docs`; package-local development continues to use `pnpm dev`. Every development command uses its app's documented fixed loopback host and port, prevents CLI address overrides from changing that binding, and fails when that port is occupied rather than automatically selecting another port. Development wrappers forward termination signals to the child server and wait for it to exit so stopped commands do not leave orphaned listeners.

## References
- `docs/README.md`
- `docs/project-template.md`
- `docs/domain-template.md`
- `AGENTS.md`
- `apps/AGENTS.md`
