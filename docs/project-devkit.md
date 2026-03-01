# Project: devkit

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

## Architecture
- Platform shell handles layout, navigation, and global providers.
- Platform shell uses a responsive navigation layout:
  - desktop: persistent left sidebar
  - mobile (`max-width: 960px`): hamburger-triggered off-canvas drawer
- Shared UI tokens map to Toss-style foundation colors and typography, then flow to shell and mini-app surfaces.
- Mini apps live under `src/apps/<id>`.
- Static route pages map each mini app to `/apps/<id>`.
- Shared services layer exposes standard platform utilities.
- Enum-based registration lives in `src/lib/mini-app-registry.ts`.
- Shell navigation menu order is fixed as `Home (/)` first, then registered mini apps from `MINI_APP_REGISTRATIONS`.
- Current route maturity mix: `commit-tracker`, `remote-file-picker`, and `thenv` are live.
- Backend-coupled mini apps consume backend APIs while preserving shell-owned auth/session/navigation behavior.

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

Shell navigation contract:
- Includes `Home` route (`/`) and all mini app routes.
- Uses route-aware active state (`aria-current="page"`) for the current page.
- Keeps mini app link entries sourced from enum-backed registration (`MINI_APP_REGISTRATIONS`).

Mini app directory contract:

```txt
apps/devkit/src/apps/<id>
```

Mini app registration contract (conceptual):
- `id` (enum-style stable identifier)
- `title`
- `route`
- `status` (`placeholder` or `live`)
- `integrationMode` (`shell-only` or `backend-coupled`)

Backend-coupled mini app example:
- `commit-tracker` route is live as an operational dashboard backed by Devkit proxy routes and `servers/commit-tracker` Connect RPC endpoints.
- `remote-file-picker` route is implemented for Phase 1 signed URL uploads (local file/mobile camera) with callback return bridge behavior.
- `thenv` route is implemented as metadata management UI backed by Devkit API proxy routes to `servers/thenv` Connect RPC endpoints.
- Devkit shell remains the owner of global auth/session/navigation concerns.

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
- Navigation and route render events with stable route and mini-app identifiers
- API request failures with request correlation identifiers

## Build and Test
Current commands:
- Build: `pnpm --filter devkit... build`
- Test: `pnpm --filter devkit... test`
- Test runner: Vitest (`apps/devkit/vitest.config.ts`)

## Roadmap
- Phase 1: Platform shell and route conventions.
- Phase 2: Add initial mini apps (Commit Tracker, Remote File Picker, thenv console).
- Phase 3: Introduce shared app registration and diagnostics tooling.
- Phase 4: Scale to many mini apps with stronger governance.

## Open Questions
- Final mini app manifest format and static typing strategy.
- Shared authentication integration approach.
- Ownership model for each mini app in larger organization scaling.

## References
- `docs/project-template.md`
- `docs/monorepo.md`
