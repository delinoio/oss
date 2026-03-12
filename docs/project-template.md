# Project Documentation Template

## Purpose
This template defines the required folder-based structure for each project contract set under `docs/`.
Use it when creating or restructuring project documentation.

## Required Directory Naming
- Directory format: `docs/project-<project-id>/`
- `project-id` must be lowercase kebab-case.
- `project-id` must be unique inside this repository.
- The directory must remain flat (no nested subdirectories such as `domains/` or `features/`).

## Required Files
Each project documentation set must include:
- `README.md` (canonical entrypoint)
- At least one feature contract file named `feature-<kebab-id>.md`

## README.md Required Sections
`README.md` must include sections in this order:
1. `# Project: <project-id>`
2. `## Documentation Layout`
3. `## Goal`
4. `## Path`
5. `## Runtime and Language`
6. `## Users`
7. `## In Scope`
8. `## Out of Scope`
9. `## Document Index`
10. `## Documentation Update Rules`

## Feature File Contract
- Use `feature-<kebab-id>.md` naming for feature-level contracts.
- Keep one cohesive capability per feature file when possible.
- Feature files can contain any required contract sections (for example: `Interfaces`, `Storage`, `Security`, `Logging`, `Build and Test`, `Roadmap`, `Open Questions`, `References`).
- Keep all feature files discoverable from `README.md` under `## Document Index`.

## Checklist for New Project Docs
- The directory name uses `project-` prefix and kebab-case project ID.
- `README.md` exists and follows the required section order.
- At least one `feature-*.md` file exists.
- No nested directories exist under `docs/project-<project-id>/`.
- Paths in docs exist or are explicitly marked as planned.
- Interfaces use stable enum-style identifiers where possible.
- Integration points reference canonical repository and domain rules in `AGENTS.md` files.
- The document set is updated with every structural project change.
