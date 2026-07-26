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

## CI Validation Defaults
- Repository CI is defined in `.github/workflows/CI.yml`; domain jobs self-gate with in-job path filters and `workflow_dispatch` runs every domain check.
- Artifact-producing changes must validate deterministic generated output, build artifacts, and relevant tests without public activation, DNS changes, runtime deployment, image push, or release publication.
- Delibase validation covers root `protos/delibase/v1` Go/TypeScript generation, Buf lint/compatibility, `servers/internal`, sqlc and PostgreSQL migration/concurrency tests, Go quality, and non-pushing `linux/amd64`/`linux/arm64` image builds.
- DeliDev validation covers frozen dependency installation, generated-client compatibility, typecheck/lint/unit-component/WCAG tests, deterministic `dist` production output, PWA manifest/service-worker and sensitive-cache policy, and Playwright smoke coverage across public, signed-out, authenticated/onboarding, empty, validation, permission-denied, offline, and dependency-error states at desktop and mobile sizes.
- `ci-result` must depend on every required domain job and fail when any required job fails or is cancelled.

## References
- `docs/README.md`
- `docs/project-template.md`
- `docs/domain-template.md`
- `AGENTS.md`
- `apps/AGENTS.md`
