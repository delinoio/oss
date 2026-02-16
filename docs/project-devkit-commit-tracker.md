# Project: devkit-commit-tracker

## Goal
`devkit-commit-tracker` is the first Devkit mini app for tracking commit activity and visibility.
It provides a focused UI for commit-oriented workflows inside Devkit.

## Path
- `apps/devkit/src/apps/commit-tracker`

## Runtime and Language
- Next.js 16 mini app module (TypeScript)

## Users
- Developers tracking commit progress
- Team leads reviewing commit trends and activity status

## In Scope
- Commit list and commit detail views
- Basic filtering and search
- Status-oriented summaries for recent activity
- Integration with backend commit data endpoints

## Out of Scope
- Full repository analytics platform
- Code review tooling replacement
- Release management automation

## Architecture
- UI module for list/detail pages.
- Data adapter for commit endpoint integration.
- Shared Devkit shell integration for navigation and routing.

## Interfaces
Canonical mini app identifier:

```ts
enum MiniAppId {
  CommitTracker = "commit-tracker",
}
```

Route contract:
- `/apps/commit-tracker`

Conceptual data contract:
- Commit summary list item
- Commit detail object
- Filter query parameters

## Storage
- Client-side filter and view preferences.
- Server-backed commit data owned by backend source.
- No standalone database in mini app scope.

## Security
- Respect Devkit auth/session controls.
- Mask sensitive metadata if backend marks fields restricted.

## Logging
Required baseline logs:
- Data fetch lifecycle and failures
- Route/load failures
- Filter application errors

## Build and Test
Planned commands:
- `pnpm --filter devkit... test`
- Targeted tests for commit tracker module when test project layout is available.

## Roadmap
- Phase 1: List/detail MVP with basic filtering.
- Phase 2: Activity summaries and richer query controls.
- Phase 3: Team-focused views and export options.

## Open Questions
- Final backend endpoint contract and pagination format.
- Priority sort rules for timeline and summary cards.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
- `docs/project-devkit.md`
