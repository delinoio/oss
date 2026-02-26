# Project: devkit

## Goal
`devkit` is a Next.js 16 web platform that hosts many web micro apps inside one shell.
It provides shared navigation, shared auth/session surface, and consistent routing for mini apps.
The shell visual baseline follows Toss Design System-inspired foundations (color, typography, spacing) for consistency.

## Path
- `apps/devkit`
- `apps/devkit/src/apps/*`
- `apps/devkit/src/app/apps/*`
- `apps/devkit/e2e/*`
- `apps/devkit/playwright.visual.config.ts`
- `scripts/run-devkit-visual-qa.sh`

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
- Shared UI tokens map to Toss-style foundation colors and typography, then flow to shell and mini-app surfaces.
- Mini apps live under `src/apps/<id>`.
- Static route pages map each mini app to `/apps/<id>`.
- Shared services layer exposes standard platform utilities.
- Enum-based registration lives in `src/lib/mini-app-registry.ts`.
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
- Visual QA browser setup: `pnpm --filter devkit... qa:visual:install-browser`
- Visual QA run: `pnpm --filter devkit... qa:visual`
- Full automation run (Midscene + OpenRouter + Codex CLI summary): `pnpm qa:visual:devkit`

## Visual QA Automation
Visual QA uses Midscene with Playwright to validate visual regressions and layout quality on key Devkit routes.
The automation runner is `scripts/run-devkit-visual-qa.sh`.

Required environment contract:
- `MIDSCENE_MODEL_BASE_URL` (default: `https://openrouter.ai/api/v1`)
- `MIDSCENE_MODEL_API_KEY` (or `OPENROUTER_API_KEY` fallback)
- `MIDSCENE_MODEL_NAME` (default: `openai/gpt-4.1-mini`)
- `MIDSCENE_MODEL_FAMILY` (default: `openai`)
- `VISUAL_QA_BASE_URL` (default: `http://127.0.0.1:3100`)
- `VISUAL_QA_SKIP_WEBSERVER` (`1` skips local dev server startup)
- `VISUAL_QA_SKIP_CODEX_SUMMARY` (`1` skips Codex CLI report generation)

Template file:
- `apps/devkit/.env.visual-qa.example`

Generated artifacts:
- Playwright HTML/JSON reports: `apps/devkit/playwright-report/visual-qa/*`
- Playwright result artifacts: `apps/devkit/test-results/visual-qa/*`
- Codex summary report: `apps/devkit/playwright-report/visual-qa/codex/*.md`

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
