# Project: public-docs

## Documentation Layout
- Canonical entrypoint for this project: docs/project-public-docs/README.md
- Keep this directory flat (no nested directories).
- Add feature contracts as feature-<kebab-id>.md files.

## Goal
`public-docs` provides a Mintlify-based public documentation app for user-facing guidance across Delino OSS projects.
It delivers curated onboarding and project overview content while keeping detailed engineering contracts in `docs/`.


## Path
- `apps/public-docs`


## Runtime and Language
- Mintlify documentation app (MDX + JSON configuration)


## Users
- External developers exploring Delino OSS projects
- Internal teams publishing user-facing documentation updates


## In Scope
- Public-facing documentation pages and navigation
- Curated project overview content derived from internal contracts
- Documentation onboarding and contribution workflow for public docs
- Broken-link validation for published pages


## Out of Scope
- Replacing internal project contracts in `docs/project-<id>/*.md`
- Product runtime APIs or backend service implementation
- Auto-generated synchronization from internal docs to public pages


## Document Index
- [feature-architecture.md](./feature-architecture.md)
- [feature-interfaces.md](./feature-interfaces.md)
- [feature-operations.md](./feature-operations.md)
- [feature-roadmap.md](./feature-roadmap.md)

## Documentation Update Rules
- Keep all project contract files in this directory (flat layout).
- Use feature-<kebab-id>.md naming for new capability contracts.
- Update this index whenever feature files are added or removed.
