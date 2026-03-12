# Project: devkit

## Documentation Layout
- Canonical entrypoint for this project: docs/project-devkit/README.md
- Keep this directory flat (no nested directories).
- Add feature contracts as feature-<kebab-id>.md files.

## Goal
`devkit` is a Next.js 16 web platform that hosts many web micro apps inside one shell.
It provides shared navigation, shared auth/session surface, and consistent routing for mini apps.
The shell visual baseline follows Toss Design System-inspired foundations (color, typography, spacing) for consistency.


## Path
- `apps/devkit`
- `apps/devkit/src/apps/*`
- `apps/devkit/src/app/apps/*`


## Runtime and Language
- Next.js 16 (TypeScript)


## Users
- Engineers and operators who need task-focused internal web tools
- Product teams launching small web apps without full standalone setup


## In Scope
- Shared web shell for mini app hosting
- Mini app registration/discovery conventions
- Stable route contract for micro apps
- Common observability and UI baseline across apps
- Integration patterns for backend-coupled mini apps via typed API contracts


## Out of Scope
- Replacing full standalone product websites
- Runtime plugin loading from untrusted remote sources
- Per-mini-app bespoke platform infrastructure


## Document Index
- [feature-architecture.md](./feature-architecture.md)
- [feature-interfaces.md](./feature-interfaces.md)
- [feature-operations.md](./feature-operations.md)
- [feature-roadmap.md](./feature-roadmap.md)

## Documentation Update Rules
- Keep all project contract files in this directory (flat layout).
- Use feature-<kebab-id>.md naming for new capability contracts.
- Update this index whenever feature files are added or removed.
