# Repository Defaults

## Purpose
This document defines default technology choices for new repository work when a more specific project or domain contract does not already choose a different approach.

## Default Technology Choices
- New persisted entities should use UUID v7 identifiers by default. Use another identifier shape only when there is a documented compatibility, storage, protocol, or product reason.
- AI-based search should use Cloudflare AI Search by default. Use another search backend only when the project contract documents the reason and migration boundary.
- When a new project does not specify its primary language, default to Golang.
- Build tooling should prefer the Rspack family when it fits the runtime and deployment target, including Rsbuild and Rspress for app and documentation surfaces.
- Static sites under `apps/` should use Rsbuild/Rspress-style toolchains and deploy to Cloudflare Pages by default. Existing documented exceptions, such as `apps/public-docs` using Mintlify, remain valid until their project contract changes.
- File handling should use Cloudflare R2 for object storage plus signed URLs for upload and download access by default. Use another storage or access pattern only when the project contract documents the reason, trust boundary, and migration considerations.

## Documentation Requirements
- Project index docs must record deviations from these defaults in `Cross-Domain Invariants` or `Change Policy`.
- Domain contract docs must record deviations in the relevant `Runtime and Language`, `Storage`, `Security`, `Build and Test`, or `Dependencies and Integrations` sections.
- Repository and domain `AGENTS.md` files must stay aligned with this document when these defaults change.

## References
- `docs/README.md`
- `docs/project-template.md`
- `docs/domain-template.md`
- `AGENTS.md`
- `apps/AGENTS.md`
