# Project: devkit

## Goal
`devkit` is a Next.js 16 web platform that hosts many web micro apps inside one shell.
It provides shared navigation, shared auth/session surface, and consistent routing for mini apps.

## Path
- `apps/devkit`
- `apps/devkit/src/apps/*`

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

## Out of Scope
- Replacing full standalone product websites
- Runtime plugin loading from untrusted remote sources
- Per-mini-app bespoke platform infrastructure

## Architecture
- Platform shell handles layout, navigation, and global providers.
- Mini apps live under `src/apps/<id>`.
- Router maps each mini app to `/apps/<id>`.
- Shared services layer exposes standard platform utilities.

## Interfaces
Canonical mini app IDs:

```ts
enum DevkitMiniAppId {
  CommitTracker = "commit-tracker",
  RemoteCamera = "remote-camera",
  Thenv = "thenv",
}
```

Routing contract:

```txt
/apps/<id>
```

Mini app directory contract:

```txt
apps/devkit/src/apps/<id>
```

Mini app registration contract (conceptual):
- `id` (enum-style stable identifier)
- `title`
- `route`
- `entry`

## Storage
- Session-level web state in browser storage as needed.
- Server-backed state depends on each mini app and is documented per mini-app file.
- Shared platform config kept in repository configuration files.

## Security
- Enforce route-level access control through shared platform guards.
- Keep mini-app boundaries explicit to avoid accidental cross-app data access.
- Do not hardcode secrets in mini-app frontend code.

## Logging
Required baseline logs:
- Mini app route resolution and load failures
- Shared shell errors
- API request failures with request correlation identifiers

## Build and Test
Planned commands:
- Build: `pnpm --filter devkit... build`
- Test: `pnpm --filter devkit... test`
- Lint: `pnpm --filter devkit... lint`

## Roadmap
- Phase 1: Platform shell and route conventions.
- Phase 2: Add initial mini apps (Commit Tracker, Remote Camera, thenv console).
- Phase 3: Introduce shared app registration and diagnostics tooling.
- Phase 4: Scale to many mini apps with stronger governance.

## Open Questions
- Final mini app manifest format and static typing strategy.
- Shared authentication integration approach.
- Ownership model for each mini app in larger organization scaling.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
