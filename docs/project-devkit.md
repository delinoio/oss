# Project: devkit

## Goal
`devkit` is a Next.js 16 web platform that hosts many web micro apps inside one shell.
It provides shared navigation, shared auth/session surface, and consistent routing for mini apps.

## Path
- `apps/devkit`
- `apps/devkit/src/apps/*`
- `apps/devkit/src/app/*`

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

## Architecture
- Platform shell handles layout, navigation, and global providers.
- Mini apps live under `src/apps/<id>`.
- App Router route entries live under `src/app/apps/<id>`.
- Route entries delegate to mini app modules in `src/apps/<id>` so mini app ownership stays stable.
- Shared services layer exposes standard platform utilities.
- Backend-coupled mini apps consume backend APIs while preserving shell-owned auth/session/navigation behavior.

Implemented mini app modules:
- `thenv`: `src/apps/thenv` with route `/apps/thenv`.
- `remote-file-picker`: contract document and README only in this repository stage.

## Interfaces
Canonical mini app IDs:

```ts
enum DevkitMiniAppId {
  CommitTracker = "commit-tracker",
  RemoteFilePicker = "remote-file-picker",
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
- `integrationMode` (`shell-only` or `backend-coupled`)

Backend-coupled mini app example:
- `thenv` uses server-side Connect RPC adapters in `apps/devkit/src/server/thenv-api.ts`.
- `commit-tracker` uses Connect RPC APIs from `servers/commit-tracker` (planned).
- Devkit shell remains the owner of global auth/session/navigation concerns.

## Storage
- Session-level web state in browser storage as needed.
- `thenv` mini app stores no secret payload in browser storage and uses metadata-only server responses.
- Server-backed state depends on each mini app and is documented per mini-app file.
- Shared platform config kept in repository configuration files.

## Security
- Enforce route-level access control through shared platform guards.
- Keep mini-app boundaries explicit to avoid accidental cross-app data access.
- Do not hardcode secrets in mini-app frontend code.
- `thenv` route must never render plaintext secret file payloads.
- Connect RPC calls for `thenv` are made from server-side adapters, not directly from browser business logic.

## Logging
Required baseline logs:
- Mini app route resolution and load failures
- Shared shell errors
- API request failures with request correlation identifiers

## Build and Test
Current commands:
- Build: `pnpm --dir apps/devkit build`
- Test: `pnpm --dir apps/devkit test`
- Dev server: `pnpm --dir apps/devkit dev`

## Roadmap
- Phase 1: Platform shell and route conventions.
- Phase 2: Expand mini apps (Commit Tracker, Remote File Picker) on top of the implemented thenv console.
- Phase 3: Introduce shared app registration and diagnostics tooling.
- Phase 4: Scale to many mini apps with stronger governance.

## Open Questions
- Final mini app manifest format and static typing strategy.
- Shared authentication integration approach.
- Ownership model for each mini app in larger organization scaling.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
